package mcp

import (
	"strings"
	"testing"
)

// TestUnknownArgumentIsRefusedByName is the enforcing half of org ADR-021 §4:
// additionalProperties:false only tells a client what is allowed, and a client
// that does not check the schema sends the typo anyway. Every tool must refuse
// it, and the message must name the offending field — a caller that is told
// only "invalid arguments" has to re-read the schema to find its own typo.
//
// `ips` is the one that matters here: a batch sent under a misspelt name used
// to check nothing at all, and the "provide 'ip'" that came back read as a
// missing argument rather than a mistyped one.
func TestUnknownArgumentIsRefusedByName(t *testing.T) {
	cases := []struct {
		tool  string
		args  string
		field string
	}{
		{"check_ip", `{"ip":"1.2.3.4","verbose":true}`, "verbose"},
		{"check_ip", `{"ipx":["1.2.3.4"]}`, "ipx"},
		{"update_list", `{"force":true}`, "force"},
		{"cache_status", `{"detail":true}`, "detail"},
		{"get_usage", `{"topic":"lifecycle"}`, "topic"},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.field, func(t *testing.T) {
			e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`}, true)
			text, isErr := callTool(t, e, tc.tool, tc.args)
			if !isErr {
				t.Fatalf("%s accepted unknown argument %q: %s", tc.tool, tc.field, text)
			}
			// Matching the decoder's own phrasing, not just the field name:
			// "provide 'hash…'" happens to contain "hash", so a bare substring
			// test passes for the wrong reason. The mutation check caught it.
			want := `unknown field "` + tc.field + `"`
			if !strings.Contains(text, want) {
				t.Errorf("%s: error does not name the offending argument: want %s, got %s", tc.tool, want, text)
			}
		})
	}
}

// TestMalformedArgumentsAreRefused covers the other half of the discarded
// error: `_ = json.Unmarshal` left `a` at its zero value when the object did
// not decode, so a wrong-typed argument produced the same call as an absent
// one — and "provide 'ip'" is a misleading answer to a request that did
// provide it.
func TestMalformedArgumentsAreRefused(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"number for string", `{"ip":8}`},
		{"string for array", `{"ips":"1.2.3.4"}`},
		{"array for object", `["1.2.3.4"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`}, true)
			text, isErr := callTool(t, e, "check_ip", tc.args)
			if !isErr {
				t.Fatalf("check_ip accepted malformed arguments: %s", text)
			}
			if strings.Contains(text, "provide 'ip'") {
				t.Errorf("check_ip reported the argument as missing instead of malformed: %s", text)
			}
			if !strings.Contains(text, "arguments:") {
				t.Errorf("error is not a decode error: %s", text)
			}
		})
	}
}

// TestOmittedArgumentsStillMeanNone pins the boundary of the change: strict
// decoding must not turn a legitimately argument-less call into an error.
func TestOmittedArgumentsStillMeanNone(t *testing.T) {
	for _, args := range []string{`{}`, `null`} {
		e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`}, true)
		text, isErr := callTool(t, e, "cache_status", args)
		if isErr {
			t.Errorf("cache_status with arguments %q was refused: %s", args, text)
		}
	}
}
