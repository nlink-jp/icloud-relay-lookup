package apple

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchOK(t *testing.T) {
	var gotUA, gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotINM = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `"v2"`)
		io.WriteString(w, "10.0.0.0/24,US,US-CA,San Jose,\n")
	}))
	defer srv.Close()

	f := &HTTPFetcher{Client: srv.Client()}
	res, err := f.Fetch(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer res.Body.Close()
	if res.NotModified {
		t.Error("NotModified = true, want false")
	}
	if res.ETag != `"v2"` {
		t.Errorf("ETag = %q, want %q", res.ETag, `"v2"`)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "San Jose") {
		t.Errorf("body = %q, want the CSV row", body)
	}
	if !strings.Contains(gotUA, "icloud-relay-lookup") {
		t.Errorf("User-Agent = %q, want a descriptive UA", gotUA)
	}
	if gotINM != "" {
		t.Errorf("If-None-Match sent without a stored ETag: %q", gotINM)
	}
}

func TestFetchNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t.Errorf("expected conditional request, got If-None-Match=%q", r.Header.Get("If-None-Match"))
	}))
	defer srv.Close()

	f := &HTTPFetcher{Client: srv.Client()}
	res, err := f.Fetch(context.Background(), srv.URL, `"v1"`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.NotModified {
		t.Error("NotModified = false, want true")
	}
	if res.Body != nil {
		t.Error("Body non-nil on 304")
	}
	if res.ETag != `"v1"` {
		t.Errorf("ETag = %q, want the request validator echoed", res.ETag)
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone fishing", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := &HTTPFetcher{Client: srv.Client()}
	_, err := f.Fetch(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("Fetch succeeded on HTTP 503, want error")
	}
	if !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "gone fishing") {
		t.Errorf("error %q should carry status and body prefix", err)
	}
}
