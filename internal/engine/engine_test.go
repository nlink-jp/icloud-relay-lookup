package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/apple"
	"github.com/nlink-jp/icloud-relay-lookup/internal/config"
)

const sampleCSV = `172.224.226.0/27,GB,GB-EN,London,
172.224.226.34/31,GB,GB-EN,Oxford,
2a02:26f7:b000:4000::/64,US,US-AK,Anchorage,
`

// fakeFetcher scripts Fetch responses and records the validators it saw.
type fakeFetcher struct {
	body     string
	etag     string
	notMod   bool
	err      error
	gotETags []string
}

func (f *fakeFetcher) Fetch(_ context.Context, _, etag string) (apple.FetchResult, error) {
	f.gotETags = append(f.gotETags, etag)
	if f.err != nil {
		return apple.FetchResult{}, f.err
	}
	if f.notMod {
		return apple.FetchResult{ETag: etag, NotModified: true}, nil
	}
	return apple.FetchResult{Body: io.NopCloser(strings.NewReader(f.body)), ETag: f.etag}, nil
}

func newEngine(t *testing.T, f apple.Fetcher) *Engine {
	t.Helper()
	cfg := &config.Config{
		URL:        "https://example.test/egress.csv",
		StoreDir:   t.TempDir(),
		TTL:        time.Hour,
		AutoUpdate: true,
	}
	e := New(cfg, f)
	e.Now = func() time.Time { return time.Unix(100000, 0).UTC() }
	return e
}

func TestLoadListWithoutStore(t *testing.T) {
	e := newEngine(t, &fakeFetcher{})
	if _, err := e.LoadList(); !errors.Is(err, ErrNoList) {
		t.Errorf("LoadList on empty dir: %v, want ErrNoList", err)
	}
}

func TestUpdateThenLoadAndLookup(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f)

	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Count != 3 || res.V4Count != 2 || res.V6Count != 1 || res.NotModified {
		t.Errorf("UpdateResult = %+v", res)
	}
	if f.gotETags[0] != "" {
		t.Errorf("first fetch sent validator %q, want none", f.gotETags[0])
	}

	list, err := e.LoadList()
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	if list.ETag() != `"v1"` || !list.Fetched().Equal(e.Now()) {
		t.Errorf("provenance: etag=%q fetched=%v", list.ETag(), list.Fetched())
	}

	r, err := Lookup(list, "172.224.226.35")
	if err != nil || !r.IsRelay || r.Entry.City != "Oxford" {
		t.Errorf("Lookup(172.224.226.35) = %+v, %v", r, err)
	}
	r, err = Lookup(list, "8.8.8.8")
	if err != nil || r.IsRelay {
		t.Errorf("Lookup(8.8.8.8) = %+v, %v; want miss", r, err)
	}
	if _, err = Lookup(list, "not-an-ip"); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("Lookup(not-an-ip): %v, want ErrInvalidIP", err)
	}
}

func TestUpdateNotModifiedBumpsFreshness(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	later := time.Unix(200000, 0).UTC()
	e.Now = func() time.Time { return later }
	f.notMod = true
	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if !res.NotModified || res.Count != 3 || !res.Fetched.Equal(later) {
		t.Errorf("revalidate result = %+v", res)
	}
	if got := f.gotETags[1]; got != `"v1"` {
		t.Errorf("revalidation sent validator %q, want \"v1\"", got)
	}
	list, err := e.LoadList()
	if err != nil {
		t.Fatalf("LoadList: %v", err)
	}
	if !list.Fetched().Equal(later) {
		t.Errorf("fetched_at not bumped: %v", list.Fetched())
	}
}

func TestUpdateFormatChangeKeepsCache(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Second download is mostly garbage — the guard must reject it.
	f.body = "<html>maintenance</html>\ntotal junk\n172.224.226.0/27,GB,GB-EN,London,\n"
	f.etag = `"v2"`
	// Simulate the upstream dropping ETag support so a full body is served.
	metaPath := e.Cfg.MetaPath()
	data, _ := os.ReadFile(metaPath)
	_ = os.WriteFile(metaPath, []byte(strings.Replace(string(data), `"v1"`, `""`, 1)), 0o644)

	_, err := e.Update(context.Background())
	if !errors.Is(err, ErrFormatChange) {
		t.Fatalf("Update on garbage: %v, want ErrFormatChange", err)
	}
	// The previous cache must still load and answer.
	list, err := e.LoadList()
	if err != nil {
		t.Fatalf("LoadList after rejected update: %v", err)
	}
	if r, _ := Lookup(list, "172.224.226.34"); !r.IsRelay {
		t.Error("previous cache lost after rejected update")
	}
}

func TestUpdateEmptyBodyRejected(t *testing.T) {
	f := &fakeFetcher{body: "", etag: `"v1"`}
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); !errors.Is(err, ErrFormatChange) {
		t.Errorf("Update on empty body: %v, want ErrFormatChange", err)
	}
}

func TestEnsureFresh(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f)

	// No cache: must fetch.
	list, refreshed, err := e.EnsureFresh(context.Background(), time.Hour)
	if err != nil || !refreshed || list.Len() != 3 {
		t.Fatalf("EnsureFresh (cold) = len %v refreshed %v err %v", list.Len(), refreshed, err)
	}

	// Fresh cache: no fetch.
	calls := len(f.gotETags)
	list, refreshed, err = e.EnsureFresh(context.Background(), time.Hour)
	if err != nil || refreshed || list == nil {
		t.Fatalf("EnsureFresh (fresh) refreshed=%v err=%v", refreshed, err)
	}
	if len(f.gotETags) != calls {
		t.Error("EnsureFresh fetched despite a fresh cache")
	}

	// Stale cache + 304: reuse the already-loaded list.
	e.Now = func() time.Time { return time.Unix(100000, 0).UTC().Add(2 * time.Hour) }
	f.notMod = true
	list, refreshed, err = e.EnsureFresh(context.Background(), time.Hour)
	if err != nil || !refreshed || list.Len() != 3 {
		t.Fatalf("EnsureFresh (304) = len %v refreshed %v err %v", list.Len(), refreshed, err)
	}

	// Stale cache + network failure: stale list returned with the error.
	// (The 304 above bumped fetched_at, so advance the clock past TTL again.)
	e.Now = func() time.Time { return time.Unix(100000, 0).UTC().Add(4 * time.Hour) }
	f.notMod = false
	f.err = errors.New("network down")
	list, refreshed, err = e.EnsureFresh(context.Background(), time.Hour)
	if err == nil || refreshed || list == nil {
		t.Fatalf("EnsureFresh (net down) = %v refreshed %v err %v", list, refreshed, err)
	}
}

func TestIsStale(t *testing.T) {
	e := newEngine(t, &fakeFetcher{})
	now := e.Now()
	if stale, _ := e.IsStale(now.Add(-time.Hour)); stale {
		t.Error("1h-old list reported stale")
	}
	if stale, _ := e.IsStale(now.Add(-8 * 24 * time.Hour)); !stale {
		t.Error("8-day-old list not reported stale")
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(e.Cfg.StoreDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}
