package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/apple"
	"github.com/nlink-jp/icloud-relay-lookup/internal/config"
	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
)

const sampleCSV = `172.224.226.0/27,GB,GB-EN,London,
172.224.226.34/31,GB,GB-EN,Oxford,
2a02:26f7:b000:4000::/64,US,US-AK,Anchorage,
104.28.30.0/26,JP,JP-13,,
`

type fakeFetcher struct {
	body   string
	etag   string
	notMod bool
	err    error
	calls  int
}

func (f *fakeFetcher) Fetch(_ context.Context, _, etag string) (apple.FetchResult, error) {
	f.calls++
	if f.err != nil {
		return apple.FetchResult{}, f.err
	}
	if f.notMod {
		return apple.FetchResult{ETag: etag, NotModified: true}, nil
	}
	return apple.FetchResult{Body: io.NopCloser(strings.NewReader(f.body)), ETag: f.etag}, nil
}

// newEngine returns an engine over a temp store, pre-populated when preload
// is true.
func newEngine(t *testing.T, f *fakeFetcher, preload bool) *engine.Engine {
	t.Helper()
	cfg := &config.Config{
		URL:        "https://example.test/egress.csv",
		StoreDir:   t.TempDir(),
		TTL:        time.Hour,
		AutoUpdate: true,
	}
	e := engine.New(cfg, f)
	if preload {
		if _, err := e.Update(context.Background()); err != nil {
			t.Fatalf("preload: %v", err)
		}
	}
	return e
}

func check(t *testing.T, e *engine.Engine, jsonOut, noUpdate bool, stdin string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, eo strings.Builder
	code = runCheck(context.Background(), &o, &eo, strings.NewReader(stdin), e, jsonOut, noUpdate, args)
	return code, o.String(), eo.String()
}

func TestCheckTristate(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)

	code, out, _ := check(t, e, false, false, "", "172.224.226.34")
	if code != exitIsRelay {
		t.Errorf("hit exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "is an iCloud Private Relay egress IP") ||
		!strings.Contains(out, "Oxford, GB-EN, GB — 172.224.226.34/31") {
		t.Errorf("hit output = %q", out)
	}

	code, out, _ = check(t, e, false, false, "", "8.8.8.8")
	if code != exitNotRelay {
		t.Errorf("miss exit code = %d, want 1", code)
	}
	if !strings.Contains(out, "is not an iCloud Private Relay egress IP") {
		t.Errorf("miss output = %q", out)
	}

	code, _, errOut := check(t, e, false, false, "", "bogus")
	if code != exitError {
		t.Errorf("invalid exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "invalid IP address") {
		t.Errorf("invalid stderr = %q", errOut)
	}
}

func TestCheckBatchAndStdin(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)

	// Multiple args: batch, exit 0 even with a miss and an invalid input.
	code, out, _ := check(t, e, false, false, "", "172.224.226.34", "8.8.8.8", "bogus")
	if code != 0 {
		t.Errorf("batch exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "bogus: invalid address") {
		t.Errorf("batch output = %q", out)
	}

	// Stdin with comments and blank lines.
	stdin := "# log extract\n\n172.224.226.34 8.8.8.8\n2a02:26f7:b000:4000::1\n"
	code, out, _ = check(t, e, false, false, stdin)
	if code != 0 {
		t.Errorf("stdin exit code = %d, want 0", code)
	}
	if got := strings.Count(out, "\n"); got != 3 {
		t.Errorf("stdin produced %d lines, want 3: %q", got, out)
	}
	if !strings.Contains(out, "Anchorage") {
		t.Errorf("v6 hit missing geo hint: %q", out)
	}
}

func TestCheckJSON(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)

	code, out, _ := check(t, e, true, false, "", "172.224.226.34", "8.8.8.8")
	if code != 0 {
		t.Errorf("json exit code = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d JSON lines, want 2", len(lines))
	}
	var hit map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &hit); err != nil {
		t.Fatalf("line 1 not JSON: %v", err)
	}
	if hit["is_private_relay"] != true || hit["city"] != "Oxford" ||
		hit["country"] != "GB" || hit["region"] != "GB-EN" ||
		hit["prefix"] != "172.224.226.34/31" {
		t.Errorf("hit JSON = %v", hit)
	}
	if _, ok := hit["list_fetched_at"]; !ok {
		t.Error("hit JSON missing list_fetched_at")
	}
	var miss map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &miss); err != nil {
		t.Fatalf("line 2 not JSON: %v", err)
	}
	if miss["is_private_relay"] != false {
		t.Errorf("miss JSON = %v", miss)
	}
	if _, ok := miss["prefix"]; ok {
		t.Error("miss JSON carries a prefix")
	}

	// Single IP with --json is still batch (exit 0 on a miss).
	code, _, _ = check(t, e, true, false, "", "8.8.8.8")
	if code != 0 {
		t.Errorf("single-IP --json exit code = %d, want 0", code)
	}
}

func TestCheckEmptyCityHint(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)
	_, out, _ := check(t, e, false, false, "", "104.28.30.7", "x")
	if !strings.Contains(out, "[JP-13, JP — 104.28.30.0/26]") {
		t.Errorf("empty-city hint = %q, want city omitted", out)
	}
}

func TestCheckNoListHint(t *testing.T) {
	f := &fakeFetcher{err: errors.New("network down")}
	e := newEngine(t, f, false)
	code, _, errOut := check(t, e, false, false, "", "8.8.8.8")
	if code != exitError {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "icloud-relay-lookup update") {
		t.Errorf("stderr = %q, want an update hint", errOut)
	}
}

func TestCheckStaleFallbackWarns(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)
	// Make the cache look stale and the network fail.
	e.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	f.err = errors.New("network down")

	code, out, errOut := check(t, e, false, false, "", "172.224.226.34")
	if code != exitIsRelay {
		t.Errorf("exit code = %d, want 0 (stale cache still answers)", code)
	}
	if !strings.Contains(errOut, "auto-update failed") {
		t.Errorf("stderr = %q, want a fallback warning", errOut)
	}
	if !strings.Contains(out, "Oxford") {
		t.Errorf("output = %q", out)
	}
}

func TestCheckNoUpdateSkipsNetwork(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)
	calls := f.calls
	code, _, _ := check(t, e, false, true, "", "172.224.226.34")
	if code != exitIsRelay {
		t.Errorf("exit code = %d, want 0", code)
	}
	if f.calls != calls {
		t.Error("--no-update still fetched")
	}
}

func TestRunUpdateAndStatus(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, false)

	var out, errOut strings.Builder
	if code := runUpdate(&out, &errOut, e); code != 0 {
		t.Fatalf("runUpdate = %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ranges:   4  (v4: 3, v6: 1)") {
		t.Errorf("update output = %q", out.String())
	}

	// 304 path.
	f.notMod = true
	out.Reset()
	if code := runUpdate(&out, &errOut, e); code != 0 {
		t.Fatalf("runUpdate(304) = %d", code)
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Errorf("304 output = %q", out.String())
	}

	out.Reset()
	if code := runStatus(&out, &errOut, e); code != 0 {
		t.Fatalf("runStatus = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "ranges:  4") || !strings.Contains(s, "status:  OK") || !strings.Contains(s, `etag:    "v1"`) {
		t.Errorf("status output = %q", s)
	}
}

func TestRunStatusNoList(t *testing.T) {
	e := newEngine(t, &fakeFetcher{}, false)
	var out, errOut strings.Builder
	if code := runStatus(&out, &errOut, e); code != exitError {
		t.Errorf("runStatus = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "NO LIST") {
		t.Errorf("status output = %q", out.String())
	}
}

func TestRunDispatch(t *testing.T) {
	if code := Run(nil, "test"); code != exitError {
		t.Errorf("Run() = %d, want 2", code)
	}
	if code := Run([]string{"nonsense"}, "test"); code != exitError {
		t.Errorf("Run(nonsense) = %d, want 2", code)
	}
	if code := Run([]string{"version"}, "test"); code != 0 {
		t.Errorf("Run(version) = %d, want 0", code)
	}
	if code := Run([]string{"help"}, "test"); code != 0 {
		t.Errorf("Run(help) = %d, want 0", code)
	}
}
