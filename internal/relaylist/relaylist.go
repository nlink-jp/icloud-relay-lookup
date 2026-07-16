// Package relaylist parses Apple's iCloud Private Relay egress IP ranges CSV
// into an in-memory prefix index and answers longest-prefix-match lookups.
//
// The upstream file (~290k rows) has one prefix per line:
//
//	172.224.226.34/31,GB,GB-EN,Oxford,
//	2a02:26f7:b000:4000::/64,US,US-AK,Anchorage,
//
// where the trailing fields are geo hints — ISO country, ISO 3166-2 region,
// and city — for where that egress range serves. The format is published by
// Apple but unversioned, so parsing is tolerant: malformed lines are counted
// in skipped rather than failing the whole parse (the engine rejects a parse
// whose skip rate suggests a format change).
//
// Lookups use a map per distinct prefix length (the real list has ~19), so a
// query is at most a handful of hash probes, checked longest-first so a more
// specific prefix wins. The list itself is kept verbatim on disk; freshness
// and provenance travel in a small metadata record injected via New.
package relaylist

import (
	"bufio"
	"io"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Source is the canonical Apple egress IP ranges endpoint (public, no
// authentication).
const Source = "https://mask-api.icloud.com/egress-ip-ranges.csv"

// Entry is one egress range with its geo hints. Geo fields may be empty —
// a few thousand upstream rows carry no city.
type Entry struct {
	Prefix  netip.Prefix
	Country string // ISO 3166-1 alpha-2, e.g. "JP"
	Region  string // ISO 3166-2, e.g. "JP-13"
	City    string // free-form, e.g. "Tokyo"
}

// List is an immutable, indexed set of egress ranges plus provenance.
type List struct {
	entries []Entry
	// index maps prefix-length → masked network address → entry index.
	// v4 and v6 addresses never collide as map keys, so lengths are shared.
	index   map[int]map[netip.Addr]int32
	v4Lens  []int // distinct IPv4 prefix lengths, descending (longest first)
	v6Lens  []int // distinct IPv6 prefix lengths, descending
	v4Count int
	v6Count int
	fetched time.Time
	etag    string
	source  string
}

// New builds a List from parsed entries, stamping provenance. Entries with a
// duplicate network are dropped (first occurrence wins) so the index and the
// entry list stay consistent.
func New(entries []Entry, fetched time.Time, etag, source string) *List {
	l := &List{
		index:   make(map[int]map[netip.Addr]int32),
		fetched: fetched,
		etag:    etag,
		source:  source,
	}
	for _, e := range entries {
		p := e.Prefix.Masked()
		bits := p.Bits()
		byAddr := l.index[bits]
		if byAddr == nil {
			byAddr = make(map[netip.Addr]int32)
			l.index[bits] = byAddr
		}
		if _, dup := byAddr[p.Addr()]; dup {
			continue
		}
		e.Prefix = p
		byAddr[p.Addr()] = int32(len(l.entries))
		l.entries = append(l.entries, e)
		if p.Addr().Is4() {
			l.v4Count++
		} else {
			l.v6Count++
		}
	}
	for bits, byAddr := range l.index {
		var has4, has6 bool
		for a := range byAddr {
			if a.Is4() {
				has4 = true
			} else {
				has6 = true
			}
			if has4 && has6 {
				break
			}
		}
		if has4 {
			l.v4Lens = append(l.v4Lens, bits)
		}
		if has6 {
			l.v6Lens = append(l.v6Lens, bits)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(l.v4Lens)))
	sort.Sort(sort.Reverse(sort.IntSlice(l.v6Lens)))
	return l
}

// Lookup returns the most specific egress range containing addr, if any. The
// address is canonicalized (unmapped) first so a v4-in-v6 input still matches
// an IPv4 range.
func (l *List) Lookup(addr netip.Addr) (Entry, bool) {
	addr = addr.Unmap()
	lens := l.v6Lens
	if addr.Is4() {
		lens = l.v4Lens
	}
	for _, bits := range lens {
		p, err := addr.Prefix(bits)
		if err != nil {
			continue
		}
		if idx, ok := l.index[bits][p.Addr()]; ok {
			return l.entries[idx], true
		}
	}
	return Entry{}, false
}

// Len returns the number of indexed egress ranges.
func (l *List) Len() int { return len(l.entries) }

// FamilyCounts returns how many ranges are IPv4 vs IPv6.
func (l *List) FamilyCounts() (v4, v6 int) { return l.v4Count, l.v6Count }

// Fetched returns when the underlying list was downloaded (or last
// revalidated as unchanged).
func (l *List) Fetched() time.Time { return l.fetched }

// ETag returns the HTTP ETag the cached list was served with, if any.
func (l *List) ETag() string { return l.etag }

// Source returns the endpoint the list was fetched from.
func (l *List) Source() string { return l.source }

// Parse reads an egress-ip-ranges CSV body. Blank lines and '#' comments are
// skipped silently; other malformed lines (bad prefix, no fields) are counted
// in skipped rather than failing the parse. A bare IP address without /bits
// is tolerated as a single-address range.
func Parse(r io.Reader) (entries []Entry, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		prefix, perr := parsePrefix(strings.TrimSpace(fields[0]))
		if perr != nil {
			skipped++
			continue
		}
		e := Entry{Prefix: prefix}
		if len(fields) > 1 {
			e.Country = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			e.Region = strings.TrimSpace(fields[2])
		}
		if len(fields) > 3 {
			e.City = strings.TrimSpace(fields[3])
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, skipped, err
	}
	return entries, skipped, nil
}

// parsePrefix parses "addr/bits", tolerating a bare address as a
// single-address prefix. The result is canonical: masked, with v4-mapped
// addresses unmapped.
func parsePrefix(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if p.Addr().Is4In6() {
		// Canonicalize a v4-mapped prefix to plain v4 so it matches v4 queries.
		bits := p.Bits() - 96
		if bits < 0 {
			return netip.Prefix{}, err
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), bits)
	}
	return p.Masked(), nil
}

// Meta is the on-disk metadata record stored beside the cached CSV. Freshness
// lives here (not in the file mtime) so it survives copies and backups, and
// the ETag enables conditional revalidation.
type Meta struct {
	FetchedAt time.Time `json:"fetched_at"`
	ETag      string    `json:"etag,omitempty"`
	Source    string    `json:"source"`
	Count     int       `json:"count"`
	V4Count   int       `json:"v4_count"`
	V6Count   int       `json:"v6_count"`
	Skipped   int       `json:"skipped,omitempty"`
}
