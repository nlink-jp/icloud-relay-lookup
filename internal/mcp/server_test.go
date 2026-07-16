package mcp

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
`

type fakeFetcher struct {
	body   string
	etag   string
	notMod bool
	err    error
}

func (f *fakeFetcher) Fetch(_ context.Context, _, etag string) (apple.FetchResult, error) {
	if f.err != nil {
		return apple.FetchResult{}, f.err
	}
	if f.notMod {
		return apple.FetchResult{ETag: etag, NotModified: true}, nil
	}
	return apple.FetchResult{Body: io.NopCloser(strings.NewReader(f.body)), ETag: f.etag}, nil
}

func newEngine(t *testing.T, f apple.Fetcher, preload bool) *engine.Engine {
	t.Helper()
	cfg := &config.Config{
		URL:      "https://example.test/egress.csv",
		StoreDir: t.TempDir(),
		TTL:      time.Hour,
	}
	e := engine.New(cfg, f)
	if preload {
		if _, err := e.Update(context.Background()); err != nil {
			t.Fatalf("preload: %v", err)
		}
	}
	return e
}

// rpc drives one full Serve session over the given raw JSON lines and returns
// the decoded responses.
func rpc(t *testing.T, e *engine.Engine, lines ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out strings.Builder
	if err := Serve(context.Background(), e, "test", in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []map[string]any
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		resps = append(resps, m)
	}
	return resps
}

// callTool invokes one tool and returns (text, isError).
func callTool(t *testing.T, e *engine.Engine, name, args string) (string, bool) {
	t.Helper()
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	resps := rpc(t, e, req)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	result, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resps[0])
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func TestInitializeAndToolsList(t *testing.T) {
	e := newEngine(t, &fakeFetcher{}, false)
	resps := rpc(t, e,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (notification must be silent)", len(resps))
	}
	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want the client's echoed", init["protocolVersion"])
	}
	if info := init["serverInfo"].(map[string]any); info["name"] != "icloud-relay-lookup" {
		t.Errorf("serverInfo = %v", info)
	}
	if instr, _ := init["instructions"].(string); !strings.Contains(instr, "get_usage") {
		t.Error("instructions should point at get_usage")
	}
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"get_usage", "check_ip", "update_list", "cache_status"} {
		if !names[want] {
			t.Errorf("tools/list missing %q (have %v)", want, names)
		}
	}
}

func TestCheckIP(t *testing.T) {
	e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`}, true)
	text, isErr := callTool(t, e, "check_ip", `{"ips":["172.224.226.34","8.8.8.8","bogus"]}`)
	if isErr {
		t.Fatalf("check_ip errored: %s", text)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("result not JSON: %v\n%s", err, text)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	hit := entries[0]
	if hit["is_private_relay"] != true || hit["city"] != "Oxford" || hit["prefix"] != "172.224.226.34/31" {
		t.Errorf("hit = %v", hit)
	}
	if entries[1]["is_private_relay"] != false {
		t.Errorf("miss = %v", entries[1])
	}
	if entries[2]["error"] != "invalid address" {
		t.Errorf("invalid = %v", entries[2])
	}
}

func TestCheckIPWithoutArgs(t *testing.T) {
	e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`}, true)
	text, isErr := callTool(t, e, "check_ip", `{}`)
	if !isErr || !strings.Contains(text, "provide 'ip'") {
		t.Errorf("empty args: isErr=%v text=%q", isErr, text)
	}
}

func TestCheckIPWithoutList(t *testing.T) {
	e := newEngine(t, &fakeFetcher{}, false)
	text, isErr := callTool(t, e, "check_ip", `{"ip":"8.8.8.8"}`)
	if !isErr || !strings.Contains(text, "update_list") {
		t.Errorf("no list: isErr=%v text=%q", isErr, text)
	}
}

func TestUpdateAndCacheStatus(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, false)

	text, isErr := callTool(t, e, "update_list", `{}`)
	if isErr {
		t.Fatalf("update_list errored: %s", text)
	}
	var upd map[string]any
	_ = json.Unmarshal([]byte(text), &upd)
	if upd["ranges"] != float64(3) || upd["not_modified"] != false {
		t.Errorf("update result = %v", upd)
	}

	text, isErr = callTool(t, e, "cache_status", `{}`)
	if isErr {
		t.Fatalf("cache_status errored: %s", text)
	}
	var st map[string]any
	_ = json.Unmarshal([]byte(text), &st)
	if st["ranges"] != float64(3) || st["stale"] != false || st["etag"] != `"v1"` {
		t.Errorf("status = %v", st)
	}

	// 304 revalidation surfaces not_modified.
	f.notMod = true
	text, _ = callTool(t, e, "update_list", `{}`)
	_ = json.Unmarshal([]byte(text), &upd)
	if upd["not_modified"] != true {
		t.Errorf("304 update result = %v", upd)
	}
}

func TestUpdateFailureIsToolError(t *testing.T) {
	e := newEngine(t, &fakeFetcher{err: errors.New("network down")}, false)
	text, isErr := callTool(t, e, "update_list", `{}`)
	if !isErr || !strings.Contains(text, "network down") {
		t.Errorf("isErr=%v text=%q", isErr, text)
	}
}

func TestListCacheReloadsAfterUpdate(t *testing.T) {
	f := &fakeFetcher{body: sampleCSV, etag: `"v1"`}
	e := newEngine(t, f, true)
	s := &server{e: e, version: "test"}

	l1, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if l2, _ := s.load(); l2 != l1 {
		t.Error("unchanged store was reparsed")
	}

	// Rewrite the store with one more row and a newer mtime.
	time.Sleep(10 * time.Millisecond) // ensure a distinct mtime
	f.body = sampleCSV + "104.28.30.0/26,JP,JP-13,Tokyo,\n"
	f.etag = `"v2"`
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("Update: %v", err)
	}
	l3, err := s.load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if l3.Len() != 4 {
		t.Errorf("reloaded list has %d ranges, want 4", l3.Len())
	}
}

func TestGetUsageAndUnknowns(t *testing.T) {
	e := newEngine(t, &fakeFetcher{}, false)
	text, isErr := callTool(t, e, "get_usage", `{}`)
	if isErr || !strings.Contains(text, "operating manual") {
		t.Errorf("get_usage: isErr=%v", isErr)
	}

	resps := rpc(t, e, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if resps[0]["error"] == nil {
		t.Error("unknown tool did not return a JSON-RPC error")
	}
	resps = rpc(t, e, `{"jsonrpc":"2.0","id":10,"method":"no/such"}`)
	if resps[0]["error"] == nil {
		t.Error("unknown method did not return a JSON-RPC error")
	}
	resps = rpc(t, e, `{"jsonrpc":"2.0","id":11,"method":"ping"}`)
	if resps[0]["error"] != nil {
		t.Error("ping errored")
	}
}

func TestMalformedJSONStops(t *testing.T) {
	e := newEngine(t, &fakeFetcher{}, false)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,`)
	var out strings.Builder
	if err := Serve(context.Background(), e, "test", in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), "-32700") {
		t.Errorf("no parse error emitted: %q", out.String())
	}
}
