package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mwyvr/firehose"
)

// TestRewriteHostThroughPipeline pins the syndicated-CMS fix end to end: a
// feed whose links and GUIDs carry the parent network's hostname stores
// items pointing at the real site — and the rule travels the config
// overlay like every other per-feed setting.
func TestRewriteHostThroughPipeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Local TV</title>
<item><guid>https://www.network.example/news/story-1/</guid><title>Local Story</title>
<link>https://www.network.example/news/story-1/</link>
<pubDate>Mon, 13 Jul 2026 09:00:00 -0700</pubDate></item>
</channel></rss>`)
	}))
	defer srv.Close()

	bare := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{bare})
	h.fetcher.cfg.Feeds = []firehose.FeedConf{{
		URL:         srv.URL,
		RewriteHost: map[string]string{"network.example": "www.localtv.example"},
	}}
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 {
		t.Fatalf("want 1 item, got %+v", h.upserts)
	}
	it := h.upserts[0][0]
	if it.URL != "https://www.localtv.example/news/story-1/" {
		t.Errorf("link not rewritten: %q", it.URL)
	}
	if it.GUID != "https://www.localtv.example/news/story-1/" {
		t.Errorf("guid not rewritten: %q", it.GUID)
	}
	if strings.Contains(it.URL+it.GUID, "network.example") {
		t.Error("wrong hostname survived")
	}
}
