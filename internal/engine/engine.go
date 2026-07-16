// Package engine ties configuration, downloading, and the on-disk list store
// together. Both the CLI and the MCP server drive the same Engine so their
// behaviour cannot diverge.
//
// The store is two files in one directory: the egress CSV kept verbatim as
// downloaded (deterministic — re-serializing ~290k rows would only invite
// drift) and a small meta.json carrying freshness, the ETag, and counts.
// Update revalidates with a conditional GET: a 304 just bumps fetched_at.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/apple"
	"github.com/nlink-jp/icloud-relay-lookup/internal/config"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

// StaleAfter is the age past which the cached list is reported as stale by
// `status` (independent of the auto-revalidation TTL). Apple's egress ranges
// change slowly, so a week-old copy still answers usefully but deserves a
// warning.
const StaleAfter = 7 * 24 * time.Hour

// maxSkipRatio is the format-change guard: the CSV format is unofficial and
// unversioned, so when more than this fraction of non-empty rows fails to
// parse, Update rejects the download and keeps the previous cache instead of
// silently indexing garbage.
const maxSkipRatio = 0.10

// Errors surfaced to callers for friendly handling.
var (
	// ErrNoList means no list has been downloaded yet; the caller should
	// suggest `update`.
	ErrNoList = errors.New("no local egress list")
	// ErrInvalidIP means the queried string is not a valid IP address.
	ErrInvalidIP = errors.New("invalid IP address")
	// ErrFormatChange means the downloaded list did not look like the known
	// CSV format; the previous cache (if any) was kept.
	ErrFormatChange = errors.New("egress list format not recognized")
)

// Engine performs load, update, and lookup operations against the configured
// list store.
type Engine struct {
	Cfg     *config.Config
	Fetcher apple.Fetcher
	Now     func() time.Time // injectable clock; defaults to time.Now
}

// New returns an Engine with the given config and fetcher.
func New(cfg *config.Config, fetcher apple.Fetcher) *Engine {
	return &Engine{Cfg: cfg, Fetcher: fetcher, Now: time.Now}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// LoadList reads the local store (meta + CSV) into an indexed List. It
// returns ErrNoList (wrapped) when either file does not exist.
func (e *Engine) LoadList() (*relaylist.List, error) {
	meta, err := e.loadMeta()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(e.Cfg.CSVPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoList, e.Cfg.CSVPath())
		}
		return nil, err
	}
	defer f.Close()
	entries, _, err := relaylist.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse cached list: %w", err)
	}
	return relaylist.New(entries, meta.FetchedAt, meta.ETag, meta.Source), nil
}

// loadMeta reads meta.json, mapping a missing file to ErrNoList.
func (e *Engine) loadMeta() (*relaylist.Meta, error) {
	data, err := os.ReadFile(e.Cfg.MetaPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoList, e.Cfg.MetaPath())
		}
		return nil, err
	}
	var m relaylist.Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", e.Cfg.MetaPath(), err)
	}
	return &m, nil
}

// UpdateResult reports what an Update produced.
type UpdateResult struct {
	Count       int
	V4Count     int
	V6Count     int
	Skipped     int
	NotModified bool // the server confirmed the cached copy is current (304)
	ETag        string
	Fetched     time.Time
}

// Update revalidates the local store against the configured URL. When the
// cached copy is intact and the server answers 304, only fetched_at is
// bumped. A full download replaces both files atomically — unless the parse
// trips the format-change guard, in which case the previous cache is kept and
// ErrFormatChange (wrapped) is returned.
func (e *Engine) Update(ctx context.Context) (UpdateResult, error) {
	// Only revalidate when both store files are present; otherwise force a
	// full download by sending no validator.
	etag := ""
	prev, _ := e.loadMeta()
	if prev != nil {
		if _, err := os.Stat(e.Cfg.CSVPath()); err == nil {
			etag = prev.ETag
		}
	}

	res, err := e.Fetcher.Fetch(ctx, e.Cfg.URL, etag)
	if err != nil {
		return UpdateResult{}, err
	}

	now := e.now().UTC().Truncate(time.Second)
	if res.NotModified {
		meta := *prev // res.NotModified implies a validator was sent, so prev != nil
		meta.FetchedAt = now
		if err := e.writeMeta(&meta); err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{
			Count: meta.Count, V4Count: meta.V4Count, V6Count: meta.V6Count,
			Skipped: meta.Skipped, NotModified: true, ETag: meta.ETag, Fetched: now,
		}, nil
	}

	raw, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return UpdateResult{}, fmt.Errorf("download %s: %w", e.Cfg.URL, err)
	}

	entries, skipped, err := relaylist.Parse(bytes.NewReader(raw))
	if err != nil {
		return UpdateResult{}, err
	}
	total := len(entries) + skipped
	if len(entries) == 0 || float64(skipped) > maxSkipRatio*float64(total) {
		return UpdateResult{}, fmt.Errorf(
			"%w: %d of %d rows unparseable (previous cache kept)", ErrFormatChange, skipped, total)
	}

	list := relaylist.New(entries, now, res.ETag, e.Cfg.URL)
	v4, v6 := list.FamilyCounts()
	meta := relaylist.Meta{
		FetchedAt: now, ETag: res.ETag, Source: e.Cfg.URL,
		Count: list.Len(), V4Count: v4, V6Count: v6, Skipped: skipped,
	}
	if err := e.writeFileAtomic(e.Cfg.CSVPath(), raw); err != nil {
		return UpdateResult{}, err
	}
	if err := e.writeMeta(&meta); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{
		Count: meta.Count, V4Count: v4, V6Count: v6,
		Skipped: skipped, ETag: res.ETag, Fetched: now,
	}, nil
}

// writeMeta serializes meta.json atomically.
func (e *Engine) writeMeta(m *relaylist.Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return e.writeFileAtomic(e.Cfg.MetaPath(), append(data, '\n'))
}

// writeFileAtomic writes data via temp + rename so a crash mid-write never
// leaves a truncated file to be read back.
func (e *Engine) writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}

// EnsureFresh returns a usable List, revalidating first when the cached list
// is missing or older than ttl. A refetch failure is non-fatal when a cached
// list already exists: the stale List is returned alongside the error so the
// caller can warn and continue offline. Only a total absence of data (no
// cache AND the fetch failed) is a hard error.
func (e *Engine) EnsureFresh(ctx context.Context, ttl time.Duration) (list *relaylist.List, refreshed bool, err error) {
	list, loadErr := e.LoadList()
	switch {
	case loadErr == nil:
		if e.now().Sub(list.Fetched()) <= ttl {
			return list, false, nil // fresh enough
		}
	case errors.Is(loadErr, ErrNoList):
		list = nil // must fetch
	default:
		return nil, false, loadErr
	}

	res, uerr := e.Update(ctx)
	if uerr != nil {
		return list, false, uerr // list may be a stale fallback, or nil
	}
	if res.NotModified && list != nil {
		return list, true, nil // same content; no reparse needed
	}
	fresh, lerr := e.LoadList()
	if lerr != nil {
		return nil, false, lerr
	}
	return fresh, true, nil
}

// Result is the outcome of a Lookup.
type Result struct {
	IsRelay bool
	Entry   relaylist.Entry // valid only when IsRelay
}

// Lookup reports whether ip is an iCloud Private Relay egress IP using the
// already-loaded list, carrying the matched range and its geo hints on a hit.
// An unparseable input returns ErrInvalidIP (wrapped).
func Lookup(list *relaylist.List, ip string) (Result, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Result{}, fmt.Errorf("%w %q", ErrInvalidIP, ip)
	}
	entry, ok := list.Lookup(addr)
	return Result{IsRelay: ok, Entry: entry}, nil
}

// IsStale reports whether a list fetched at t is older than StaleAfter
// relative to the engine's clock, and the age.
func (e *Engine) IsStale(t time.Time) (bool, time.Duration) {
	age := e.now().Sub(t)
	return age > StaleAfter, age
}
