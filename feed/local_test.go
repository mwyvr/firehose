package feed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
)

const localTestFeed = `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Scraped Site</title>
<item><guid>s-1</guid><title>Scraped One</title><link>https://site.example/one</link>
<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate><description>hello</description></item>
</channel></rss>`

// TestLocalFeedLifecycle runs a file:// feed through the real Run pipeline:
func TestLocalFeedLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.xml")
	if err := os.WriteFile(path, []byte(localTestFeed), 0o644); err != nil {
		t.Fatal(err)
	}

	fd := &firehose.Feed{ID: 1, URL: "file://" + path}
	h := newHarness(t, []*firehose.Feed{fd})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 || h.upserts[0][0].Title != "Scraped One" {
		t.Fatalf("first pass should store the item: %+v", h.upserts)
	}
	upd := h.updates[0]
	if upd.LastSuccess == nil || upd.LastModified == nil {
		t.Fatalf("first pass must stamp LastSuccess and record mtime: %+v", upd)
	}
	if upd.Title == nil || *upd.Title != "Scraped Site" {
		t.Errorf("self-title not stored: %v", upd.Title)
	}

	// Second pass: unchanged file = local 304.
	fd.LastModified = *upd.LastModified
	h2 := newHarness(t, []*firehose.Feed{fd})
	if err := h2.fetcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h2.upserts) != 0 {
		t.Fatalf("unchanged file must not re-store: %+v", h2.upserts)
	}
	u2 := h2.updates[0]
	if u2.LastFetched == nil || u2.LastSuccess != nil {
		t.Fatalf("local 304: LastFetched stamps, LastSuccess does not: %+v", u2)
	}

	// Third pass: touched file re-parses.
	newer := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, newer, newer); err != nil {
		t.Fatal(err)
	}
	h3 := newHarness(t, []*firehose.Feed{fd})
	if err := h3.fetcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h3.upserts) != 1 {
		t.Fatalf("touched file must re-parse: %+v", h3.upserts)
	}
	if got := *h3.updates[0].LastModified; got == fd.LastModified {
		t.Error("validator not advanced to the new mtime")
	} else if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Errorf("mtime validator not RFC3339Nano: %q", got)
	}
}

// TestLocalFeedMissingIsNotFound pins the not-yet-scraped case: a missing
// file classifies ENOTFOUND and enters normal backoff.
func TestLocalFeedMissingIsNotFound(t *testing.T) {
	fd := &firehose.Feed{ID: 1, URL: "file:///nonexistent/nowhere.xml"}
	h := newHarness(t, []*firehose.Feed{fd})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	upd := h.updates[0]
	if upd.LastStatus == nil || *upd.LastStatus != firehose.ENOTFOUND {
		t.Fatalf("want ENOTFOUND, got %+v", upd)
	}
	if upd.NextEarliest == nil {
		t.Error("missing file must enter backoff like any failure")
	}
}
