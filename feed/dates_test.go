package feed

import (
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
	loc, err := time.LoadLocation("America/Dawson_Creek")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		raw  string
		want time.Time
	}{
		{"Tue, 14 July 2026 23:00:00", time.Date(2026, 7, 14, 23, 0, 0, 0, loc)},
		{"Mon, 06 June 2022 16:00:00", time.Date(2022, 6, 6, 16, 0, 0, 0, loc)},
		{"Wed, 11 October 2023 16:00:00", time.Date(2023, 10, 11, 16, 0, 0, 0, loc)},
		{"Wed, 11 February 2026 17:05:00", time.Date(2026, 2, 11, 17, 5, 0, 0, loc)},
		{"Fri, 12 December 2025 23:00:00", time.Date(2025, 12, 12, 23, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		got, ok := parseLooseDate(tc.raw, loc)
		if !ok {
			t.Errorf("%q: not parsed", tc.raw)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%q: got %v want %v", tc.raw, got, tc.want)
		}
	}
	if _, ok := parseLooseDate("not a date at all", loc); ok {
		t.Error("garbage must not parse")
	}
	if _, ok := parseLooseDate("", loc); ok {
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
	if it.Published.Year() != 2026 || it.Published.Month() != time.July || it.Published.Day() != 14 {
		t.Errorf("rescued date wrong: %v", it.Published)
	}
}
