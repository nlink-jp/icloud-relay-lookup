// Package apple fetches Apple's iCloud Private Relay egress IP ranges list.
//
// The endpoint is public and requires no authentication, so there is no
// secret to redact. Apple serves the list with an ETag and
// cache-control: max-age=3600, so Fetch supports conditional revalidation:
// callers pass the previously stored ETag and an unchanged list comes back as
// a 304 with no body, costing almost no bandwidth. Every request carries a
// descriptive User-Agent, and callers are expected to cache the result rather
// than re-fetch per lookup.
package apple

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// userAgent identifies this client to Apple's servers.
const userAgent = "icloud-relay-lookup (+https://github.com/nlink-jp/icloud-relay-lookup)"

// FetchResult is the outcome of a conditional Fetch. When NotModified is true
// the cached copy is still current: Body is nil and ETag echoes the request's
// validator. Otherwise Body streams the new list (the caller must close it)
// and ETag carries the new validator ("" if the server sent none).
type FetchResult struct {
	Body        io.ReadCloser
	ETag        string
	NotModified bool
}

// Fetcher retrieves the egress list, revalidating against etag when non-empty.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL, etag string) (FetchResult, error)
}

// HTTPFetcher is the production Fetcher.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher with a timeout sized for the ~12MB list.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{Client: &http.Client{Timeout: 5 * time.Minute}}
}

// Fetch performs the (conditional) GET. On a non-200/304 response it returns
// an error including a short prefix of the body.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL, etag string) (FetchResult, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("download %s: %w", rawURL, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return FetchResult{Body: resp.Body, ETag: resp.Header.Get("ETag")}, nil
	case http.StatusNotModified:
		resp.Body.Close()
		return FetchResult{ETag: etag, NotModified: true}, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return FetchResult{}, fmt.Errorf("download %s: HTTP %d: %s", rawURL, resp.StatusCode, trimBody(body))
	}
}

func trimBody(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
