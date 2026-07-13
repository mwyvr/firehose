package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
)

func TestCanonicalItemURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"utm stripped", "https://x.example/a?utm_source=rss&utm_medium=feed", "https://x.example/a"},
		{"cmp stripped (CBC)", "https://www.cbc.ca/news/story-1.234?cmp=rss", "https://www.cbc.ca/news/story-1.234"},
		{"case-insensitive tracker", "https://x.example/a?CMP=oth_b", "https://x.example/a"},
		{"real params preserved in order", "https://x.example/a?page=2&utm_campaign=x&id=7", "https://x.example/a?page=2&id=7"},
		{"fragment dropped", "https://x.example/a#comments", "https://x.example/a"},
		{"host lowercased", "https://News.Gov.BC.CA/releases/1", "https://news.gov.bc.ca/releases/1"},
		{"trailing slash preserved", "https://x.example/a/", "https://x.example/a/"},
		{"scheme preserved", "http://x.example/a", "http://x.example/a"},
		{"garbage passthrough", "::not a url::", "::not a url::"},
	}
	for _, tc := range cases {
		if got := canonicalItemURL(tc.in); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func dtItem(feedID int64, urlStr string, full bool, age time.Duration) *firehose.Item {
	return &firehose.Item{
		FeedID: feedID, GUID: urlStr + "|" + time.Now().Add(-age).String(),
		URL: urlStr, FullContent: full,
		Published: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC).Add(-age),
	}
}

func dtMeta() (map[int64]feedMeta, map[int64]int) {
	meta := map[int64]feedMeta{
		1: {title: "BC Gov News", conf: firehose.FeedConf{URL: "https://a.example/feed"}},
		2: {title: "MoTT", conf: firehose.FeedConf{URL: "https://b.example/feed"}},
		3: {title: "EMCR", conf: firehose.FeedConf{URL: "https://c.example/feed"}},
	}
	return meta, map[int64]int{1: 0, 2: 1, 3: 2}
}

// TestDedupeBCGovScenario pins the motivating case: one release carried by
// three ministry feeds renders once, attributed to the earliest carrier,
// with the others in "also via" (config order).
func TestDedupeBCGovScenario(t *testing.T) {
	meta, order := dtMeta()
	release := "https://news.gov.bc.ca/releases/2026EMCR0042-001"
	items := []*firehose.Item{
		dtItem(3, release+"?utm_source=rss", false, 0),     // EMCR echo, newest
		dtItem(2, release, false, time.Hour),               // MoTT echo
		dtItem(1, release, false, 2*time.Hour),             // origin: earliest
		dtItem(2, "https://b.example/only-mott", false, 0), // unrelated survives
	}
	kept, also := dedupeItems(items, meta, order)
	if len(kept) != 2 {
		t.Fatalf("want 2 kept, got %d", len(kept))
	}
	var w *firehose.Item
	for _, it := range kept {
		if strings.HasPrefix(it.URL, release) {
			w = it
		}
	}
	if w == nil || w.FeedID != 1 {
		t.Fatalf("winner should be the earliest carrier (feed 1), got %+v", w)
	}
	if got := strings.Join(also[w], ", "); got != "MoTT, EMCR" {
		t.Errorf("also-via wrong: %q", got)
	}
}

func TestDedupeFullContentBeatsTeaser(t *testing.T) {
	meta, order := dtMeta()
	u := "https://x.example/story"
	teaserFirst := dtItem(1, u, false, 2*time.Hour) // earlier but a teaser
	full := dtItem(2, u, true, time.Hour)
	kept, _ := dedupeItems([]*firehose.Item{full, teaserFirst}, meta, order)
	if len(kept) != 1 || kept[0] != full {
		t.Fatalf("full content must win over an earlier teaser")
	}
}

func TestDedupeLinklessNeverDeduped(t *testing.T) {
	meta, order := dtMeta()
	a, b := dtItem(1, "", false, 0), dtItem(2, "", false, time.Hour)
	kept, _ := dedupeItems([]*firehose.Item{a, b}, meta, order)
	if len(kept) != 2 {
		t.Fatalf("linkless items have no identity; both must survive")
	}
}
