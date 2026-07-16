package feed

import (
	_ "time/tzdata"

	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
)

// TestParseLooseDateGovstack pins the rescue against pubDate strings taken
// verbatim from a live Govstack feed, all of which gofeed returns nil for: full
// month name, no timezone.
func TestParseLooseDateGovstack(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Time
	}{
		{"Tue, 14 July 2026 23:00:00", time.Date(2026, 7, 14, 23, 0, 0, 0, time.UTC)},
		{"Mon, 06 June 2022 16:00:00", time.Date(2022, 6, 6, 16, 0, 0, 0, time.UTC)},
		{"Wed, 11 October 2023 16:00:00", time.Date(2023, 10, 11, 16, 0, 0, 0, time.UTC)},
		{"Wed, 11 February 2026 17:05:00", time.Date(2026, 2, 11, 17, 5, 0, 0, time.UTC)},
		{"Fri, 12 December 2025 23:00:00", time.Date(2025, 12, 12, 23, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, ok := parseLooseDate(tc.raw, time.UTC)
		if !ok {
			t.Errorf("%q: not parsed", tc.raw)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%q: got %v want %v", tc.raw, got, tc.want)
		}
	}
	if _, ok := parseLooseDate("not a date at all", time.UTC); ok {
		t.Error("garbage must not parse")
	}
	if _, ok := parseLooseDate("", time.UTC); ok {
		t.Error("empty must not parse")
	}
}

// TestGovstackFeedEndToEnd runs a govstack-shaped feed through the real Run
// pipeline: rescued dates land on the items (config timezone)
func TestGovstackFeedEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title/>
<item><title>Foo Title</title><link>http://%s/a</link>
<pubDate>Tue, 14 July 2026 23:00:00</pubDate></item>
<item><title>New Foo in Fooville</title><link>http://%s/b</link>
<pubDate>Mon, 06 June 2022 16:00:00</pubDate></item>
</channel></rss>`, r.Host, r.Host)
	}))
	defer srv.Close()

	fd := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{fd})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 {
		t.Fatalf("want exactly the 2026 item stored (2022 falls to retention), got %+v", h.upserts)
	}
	it := h.upserts[0][0]
	if it.Title != "Foo Title" {
		t.Fatalf("wrong survivor: %s", it.Title)
	}
	want := time.Date(2026, 7, 14, 23, 0, 0, 0, time.UTC)
	if !it.Published.Equal(want) {
		t.Errorf("rescued date wrong: got %v want %v", it.Published, want)
	}
}

// TestResolvePublishedTiers pins the single resolution chain's verdicts:
// standard dates report DateFromFeed, Govstack dates DateRescued, garbage
// and dateless entries DateNone.
func TestResolvePublishedTiers(t *testing.T) {
	cases := []struct {
		name string
		body string
		tier string
	}{
		{"standard", `<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate>`, DateFromFeed},
		{"govstack", `<pubDate>Tue, 14 July 2026 23:00:00</pubDate>`, DateRescued},
		{"garbage", `<pubDate>whenever</pubDate>`, DateNone},
		{"dateless", ``, DateNone},
	}
	for _, tc := range cases {
		p := &Probe{}
		body := `<rss version="2.0"><channel><title>T</title><item><title>X</title>` +
			`<link>https://x.example/a</link>` + tc.body + `</item></channel></rss>`
		if _, err := analyzeProbeBody(p, []byte(body), "https://x.example/feed", ""); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if p.First == nil || p.First.PublishedTier != tc.tier {
			t.Errorf("%s: tier = %q, want %q", tc.name, p.First.PublishedTier, tc.tier)
		}
	}
}

// TestProbeDateStats pins the feed-wide visibility: a mixed feed reports
// how many items resolved per tier, so a date-blind feed announces itself
// at onboarding.
func TestProbeDateStats(t *testing.T) {
	body := `<rss version="2.0"><channel><title>T</title>
<item><title>A</title><link>https://x.example/a</link><pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate></item>
<item><title>B</title><link>https://x.example/b</link><pubDate>Tue, 14 July 2026 23:00:00</pubDate></item>
<item><title>C</title><link>https://x.example/c</link><pubDate>eventually</pubDate></item>
<item><title>D</title><link>https://x.example/d</link></item>
</channel></rss>`
	p := &Probe{}
	if _, err := analyzeProbeBody(p, []byte(body), "https://x.example/feed", ""); err != nil {
		t.Fatal(err)
	}
	if p.DatesFeed != 1 || p.DatesRescued != 1 || p.DatesUnparsed != 2 {
		t.Errorf("stats = feed:%d rescued:%d unparsed:%d, want 1/1/2",
			p.DatesFeed, p.DatesRescued, p.DatesUnparsed)
	}
	if p.First.PublishedRaw == "" {
		t.Error("first item's raw date string must be captured for diagnostics")
	}
}

// TestParseLooseDateInLocation pins timezone-aware rescue: the same
// zoneless string lands differently by zone, and DST is honored (a July
// date in a DST zone differs from a January one).
func TestParseLooseDateInLocation(t *testing.T) {
	edm, err := time.LoadLocation("America/Edmonton")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	july, ok := parseLooseDate("Mon, 13 July 2026 12:00:00", edm)
	if !ok {
		t.Fatal("rescue failed")
	}
	if got := july.UTC().Hour(); got != 18 { // MDT = UTC-6
		t.Errorf("July noon Edmonton = %d UTC, want 18", got)
	}
	jan, ok := parseLooseDate("Mon, 12 January 2026 12:00:00", edm)
	if !ok {
		t.Fatal("rescue failed")
	}
	if got := jan.UTC().Hour(); got != 19 { // MST = UTC-7
		t.Errorf("January noon Edmonton = %d UTC, want 19", got)
	}
	utc, _ := parseLooseDate("Mon, 13 July 2026 12:00:00", time.UTC)
	if utc.Hour() != 12 {
		t.Errorf("UTC parse moved: %v", utc)
	}
}
