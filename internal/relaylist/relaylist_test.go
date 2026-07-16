package relaylist

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

// sample mirrors real upstream rows: v4 and v6, a row with an empty city, and
// the fixed trailing empty field.
const sample = `172.224.226.0/27,GB,GB-EN,London,
172.224.226.34/31,GB,GB-EN,Oxford,
2a02:26f7:b000:4000::/64,US,US-AK,Anchorage,
104.28.30.0/26,JP,JP-13,,
`

func mustParse(t *testing.T, in string) []Entry {
	t.Helper()
	entries, skipped, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("Parse skipped %d rows, want 0", skipped)
	}
	return entries
}

func TestParseSample(t *testing.T) {
	entries := mustParse(t, sample)
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(entries))
	}
	got := entries[1]
	if got.Prefix.String() != "172.224.226.34/31" || got.Country != "GB" || got.Region != "GB-EN" || got.City != "Oxford" {
		t.Errorf("entry[1] = %+v, want 172.224.226.34/31 GB GB-EN Oxford", got)
	}
	if entries[3].City != "" {
		t.Errorf("entry[3].City = %q, want empty", entries[3].City)
	}
}

func TestParseTolerance(t *testing.T) {
	in := `# comment

172.224.226.0/27,GB,GB-EN,London,
not-a-prefix,XX,,,
999.1.1.1/24,XX,,,
172.224.226.34
2a02:26f7::/999,US,,,
`
	entries, skipped, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Valid: the /27 and the bare IP (tolerated as a /32).
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (have %+v)", len(entries), entries)
	}
	if skipped != 3 {
		t.Errorf("skipped = %d, want 3", skipped)
	}
	if entries[1].Prefix.String() != "172.224.226.34/32" {
		t.Errorf("bare IP parsed as %s, want 172.224.226.34/32", entries[1].Prefix)
	}
}

func TestParseUnmaskedAndMappedPrefixes(t *testing.T) {
	in := `172.224.226.35/31,GB,GB-EN,Oxford,
::ffff:10.0.0.0/120,ZZ,,,
`
	entries := mustParse(t, in)
	if entries[0].Prefix.String() != "172.224.226.34/31" {
		t.Errorf("unmasked prefix canonicalized to %s, want 172.224.226.34/31", entries[0].Prefix)
	}
	if entries[1].Prefix.String() != "10.0.0.0/24" {
		t.Errorf("v4-mapped prefix canonicalized to %s, want 10.0.0.0/24", entries[1].Prefix)
	}
}

func newList(t *testing.T, in string) *List {
	t.Helper()
	return New(mustParse(t, in), time.Unix(1000, 0).UTC(), `"abc"`, Source)
}

func TestLookup(t *testing.T) {
	l := newList(t, sample)
	cases := []struct {
		ip   string
		hit  bool
		city string
	}{
		{"172.224.226.34", true, "Oxford"}, // /31 low
		{"172.224.226.35", true, "Oxford"}, // /31 high
		{"172.224.226.36", false, ""},      // adjacent, outside both ranges
		{"172.224.226.5", true, "London"},  // inside the /27
		{"2a02:26f7:b000:4000::1234", true, "Anchorage"},
		{"2a02:26f7:b000:4001::1", false, ""},
		{"::ffff:172.224.226.34", true, "Oxford"}, // v4-mapped input
		{"104.28.30.63", true, ""},                // empty-city row still matches
	}
	for _, c := range cases {
		e, ok := l.Lookup(netip.MustParseAddr(c.ip))
		if ok != c.hit {
			t.Errorf("Lookup(%s) hit = %v, want %v", c.ip, ok, c.hit)
			continue
		}
		if ok && e.City != c.city {
			t.Errorf("Lookup(%s).City = %q, want %q", c.ip, e.City, c.city)
		}
	}
}

func TestLookupLongestMatch(t *testing.T) {
	in := `10.0.0.0/8,US,US-CA,Broad,
10.1.0.0/16,JP,JP-13,Narrow,
`
	l := newList(t, in)
	e, ok := l.Lookup(netip.MustParseAddr("10.1.2.3"))
	if !ok || e.City != "Narrow" {
		t.Errorf("Lookup(10.1.2.3) = %+v ok=%v, want the /16 (Narrow)", e, ok)
	}
	e, ok = l.Lookup(netip.MustParseAddr("10.2.0.1"))
	if !ok || e.City != "Broad" {
		t.Errorf("Lookup(10.2.0.1) = %+v ok=%v, want the /8 (Broad)", e, ok)
	}
}

func TestNewDropsDuplicates(t *testing.T) {
	in := `10.0.0.0/24,US,US-CA,First,
10.0.0.0/24,JP,JP-13,Second,
`
	l := newList(t, in)
	if l.Len() != 1 {
		t.Fatalf("Len = %d, want 1", l.Len())
	}
	e, ok := l.Lookup(netip.MustParseAddr("10.0.0.1"))
	if !ok || e.City != "First" {
		t.Errorf("duplicate handling: got %+v, want first occurrence to win", e)
	}
}

func TestCountsAndProvenance(t *testing.T) {
	l := newList(t, sample)
	v4, v6 := l.FamilyCounts()
	if v4 != 3 || v6 != 1 {
		t.Errorf("FamilyCounts = (%d, %d), want (3, 1)", v4, v6)
	}
	if l.Len() != 4 {
		t.Errorf("Len = %d, want 4", l.Len())
	}
	if l.Fetched() != time.Unix(1000, 0).UTC() || l.ETag() != `"abc"` || l.Source() != Source {
		t.Errorf("provenance not carried: %v %q %q", l.Fetched(), l.ETag(), l.Source())
	}
}
