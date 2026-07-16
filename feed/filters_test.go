package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mwyvr/firehose"
)

// TestIncludeFilterSeesDescription pins the wire-service dateline case:
// when an item carries BOTH content:encoded and a description, the
// regional dateline lives in the description — a field the filter
// haystack previously excluded. include keywords must match it.
func TestIncludeFilterSeesDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Wire</title>
<item><guid>a</guid><title>Ferry expansion approved</title><link>https://w.example/a</link>
<description>VICTORIA — The province approved new vessels.</description>
<content:encoded><![CDATA[<p>The province approved new vessels for coastal routes, officials said.</p>]]></content:encoded>
<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate></item>
<item><guid>b</guid><title>Harvest outlook improves</title><link>https://w.example/b</link>
<description>SASKATOON — Crop conditions improved this week.</description>
<content:encoded><![CDATA[<p>Crop conditions improved across the region this week.</p>]]></content:encoded>
<pubDate>Mon, 13 Jul 2026 09:05:00 -0700</pubDate></item>
<item><guid>c</guid><title>Small town waterline fixed</title><link>https://w.example/c</link>
<description>EXAMPLEVILLE, B.C. — Repairs completed ahead of schedule.</description>
<pubDate>Mon, 13 Jul 2026 09:10:00 -0700</pubDate></item>
</channel></rss>`)
	}))
	defer srv.Close()

	bare := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{bare})
	h.fetcher.cfg.Feeds = []firehose.FeedConf{{
		URL:     srv.URL,
		Include: []string{"B.C.", "VICTORIA", "VANCOUVER"},
	}}
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(h.upserts) != 1 {
		t.Fatalf("want one batch, got %+v", h.upserts)
	}
	got := map[string]bool{}
	for _, it := range h.upserts[0] {
		got[it.GUID] = true
	}
	if !got["a"] {
		t.Error("dateline in description (with content:encoded present) must match include")
	}
	if !got["c"] {
		t.Error("description-only item must still match (regression)")
	}
	if got["b"] {
		t.Error("non-matching item must be dropped by include")
	}
}
