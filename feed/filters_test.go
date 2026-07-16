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

// TestURLFilters; note: linkless items cannot satisfy a URL keep-filter.
func TestURLFilters(t *testing.T) {
	fd := &firehose.Feed{IncludeURL: []string{"/west/"}}
	if skipByFilters(fd, "", "", "", "https://w.example/parent_region/west/story-1.html") {
		t.Error("matching path must be kept")
	}
	if !skipByFilters(fd, "", "", "", "https://w.example/parent_region/east/story-2.html") {
		t.Error("non-matching path must be dropped")
	}
	if !skipByFilters(fd, "", "", "", "") {
		t.Error("linkless item cannot satisfy include_url")
	}
	fd2 := &firehose.Feed{ExcludeURL: []string{"/sponsored/"}}
	if !skipByFilters(fd2, "", "", "", "https://w.example/sponsored/buy.html") {
		t.Error("excluded path must be dropped")
	}
	if skipByFilters(fd2, "", "", "", "https://w.example/news/real.html") {
		t.Error("clean path wrongly dropped")
	}
	if skipByFilters(&firehose.Feed{}, "", "", "", "https://w.example/a") {
		t.Error("no rules must be identity")
	}
}

// TestURLFiltersThroughPipeline runs the regional-wire scenario through
// real Run: a mixed subcategory feed keeps only items whose link path
// carries the sub-region, and the rule travels the config overlay.
func TestURLFiltersThroughPipeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Wire</title>
<item><guid>a</guid><title>West story</title><link>https://w.example/parent_region/west/story-a.html</link>
<description>TOWNSVILLE - Something happened.</description>
<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate></item>
<item><guid>b</guid><title>East story</title><link>https://w.example/parent_region/east/story-b.html</link>
<description>OTHERTON - Something else happened.</description>
<pubDate>Mon, 13 Jul 2026 09:05:00 -0700</pubDate></item>
</channel></rss>`)
	}))
	defer srv.Close()

	bare := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{bare})
	h.fetcher.cfg.Feeds = []firehose.FeedConf{{URL: srv.URL, IncludeURL: []string{"/west/"}}}
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 {
		t.Fatalf("want exactly the west item, got %+v", h.upserts)
	}
	if h.upserts[0][0].GUID != "a" {
		t.Errorf("wrong survivor: %s", h.upserts[0][0].GUID)
	}
}

// TestIncludeFamiliesComposeAsOR pins the multi-section publisher case:
// a story's URL carries only its PRIMARY section, so a regional story
// filed under a topic vertical matches by text while a text-pattern-free
// story matches by path — either evidence keeps the item; neither drops
// it; excludes still veto regardless.
func TestIncludeFamiliesComposeAsOR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Wire</title>
<item><guid>text-only</guid><title>Nurses talks begin</title><link>https://w.example/national/nurses-talks-begin.html</link>
<description>WESTVILLE - Talks in West Province began Monday.</description>
<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate></item>
<item><guid>url-only</guid><title>Driver sentenced</title><link>https://w.example/parent_region/west/driver-sentenced.html</link>
<description>SMALLTOWN - A driver was sentenced.</description>
<pubDate>Mon, 13 Jul 2026 09:01:00 -0700</pubDate></item>
<item><guid>neither</guid><title>Harvest outlook</title><link>https://w.example/parent_region/east/harvest.html</link>
<description>EASTBURG - Crops improved.</description>
<pubDate>Mon, 13 Jul 2026 09:02:00 -0700</pubDate></item>
<item><guid>vetoed</guid><title>Sponsored: West Province deals</title><link>https://w.example/parent_region/west/deals.html</link>
<description>WESTVILLE - Buy things.</description>
<pubDate>Mon, 13 Jul 2026 09:03:00 -0700</pubDate></item>
</channel></rss>`)
	}))
	defer srv.Close()

	bare := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{bare})
	h.fetcher.cfg.Feeds = []firehose.FeedConf{{
		URL:        srv.URL,
		Include:    []string{"West Province", "WESTVILLE"},
		IncludeURL: []string{"/west/"},
		Exclude:    []string{"sponsored"},
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
	if !got["text-only"] {
		t.Error("text evidence alone must keep (primary section /national/)")
	}
	if !got["url-only"] {
		t.Error("URL evidence alone must keep (no text pattern)")
	}
	if got["neither"] {
		t.Error("no evidence must drop")
	}
	if got["vetoed"] {
		t.Error("exclude must veto even with both evidences present")
	}
	if len(got) != 2 {
		t.Errorf("want exactly 2 survivors, got %v", got)
	}
}
