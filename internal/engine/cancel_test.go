package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/apple"
)

// cancelOnAnswer answers like fakeFetcher — a 304 or a new list — and cancels
// the update as the answer arrives, before the engine writes anything.
type cancelOnAnswer struct {
	notMod bool
	body   string
	cancel context.CancelFunc
}

func (c *cancelOnAnswer) Fetch(_ context.Context, _, etag string) (apple.FetchResult, error) {
	c.cancel()
	if c.notMod {
		return apple.FetchResult{ETag: etag, NotModified: true}, nil
	}
	return apple.FetchResult{Body: io.NopCloser(strings.NewReader(c.body)), ETag: `"v2"`}, nil
}

// An update cancelled once its answer has arrived writes nothing: neither the
// fetched_at a 304 would bump in meta.json nor a new list.
func TestAnUpdateCancelledOnItsAnswerWritesNothing(t *testing.T) {
	for _, notMod := range []bool{true, false} {
		e := newEngine(t, &fakeFetcher{body: sampleCSV, etag: `"v1"`})
		if _, err := e.Update(context.Background()); err != nil {
			t.Fatal(err)
		}
		csv, _ := os.ReadFile(e.Cfg.CSVPath())
		meta, _ := os.ReadFile(e.Cfg.MetaPath())
		e.Now = func() time.Time { return time.Unix(1_900_000_000, 0) }
		ctx, cancel := context.WithCancel(context.Background())
		e.Fetcher = &cancelOnAnswer{notMod: notMod, body: sampleCSV, cancel: cancel}
		_, err := e.Update(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Errorf("304 %v: err = %v, want context.Canceled", notMod, err)
		}
		csv2, _ := os.ReadFile(e.Cfg.CSVPath())
		meta2, _ := os.ReadFile(e.Cfg.MetaPath())
		if !bytes.Equal(csv, csv2) || !bytes.Equal(meta, meta2) {
			t.Errorf("304 %v: the store was rewritten", notMod)
		}
	}
}
