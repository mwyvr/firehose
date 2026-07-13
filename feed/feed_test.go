package feed

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/firehose/mock"
)

const rssTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Test Feed</title>
<item>
  <title>First item</title>
  <link>%s/posts/one</link>
  <guid>guid-1</guid>
  <pubDate>Mon, 06 Jul 2026 10:00:00 GMT</pubDate>
  <description>&lt;p&gt;Hello &lt;a href="/rel"&gt;relative&lt;/a&gt; world&lt;/p&gt;</description>
</item>
</channel></rss>`

func testConfig() *firehose.Config {
	return &firehose.Config{
		Settings: firehose.Settings{
			CacheRetention: firehose.Duration(30 * 24 * time.Hour),
		},
		Fetch: firehose.FetchConfig{
			Concurrency: 2,
			Timeout:     firehose.Duration(5 * time.Second),
			UserAgent:   "firehose-test/0",
		},
	}
}

// harness wires a Fetcher to mocks that record upserts and feed updates.
type harness struct {
	fetcher *Fetcher
	mu      sync.Mutex
	upserts [][]*firehose.Item
	updates []firehose.FeedUpdate
}

func newHarness(t *testing.T, due []*firehose.Feed) *harness {
	t.Helper()
	h := &harness{}
	feeds := &mock.FeedService{
		FindFeedsFn: func(ctx context.Context, filter firehose.FeedFilter) ([]*firehose.Feed, int, error) {
			return due, len(due), nil
		},
		UpdateFeedFn: func(ctx context.Context, id int64, upd firehose.FeedUpdate) (*firehose.Feed, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.updates = append(h.updates, upd)
			return nil, nil
		},
	}
	items := &mock.ItemService{
		UpsertItemsFn: func(ctx context.Context, items []*firehose.Item) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.upserts = append(h.upserts, items)
			return nil
		},
	}
	h.fetcher = NewFetcher(testConfig(), feeds, items)
	return h
}

func (h *harness) lastUpdate(t *testing.T) firehose.FeedUpdate {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.updates) == 0 {
		t.Fatal("no feed update recorded")
	}
	return h.updates[len(h.updates)-1]
}

func TestFetchParsesAndSanitizes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		_, _ = fmt.Fprintf(w, rssTemplate, "http://"+r.Host)
	}))
	defer srv.Close()

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL}})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 {
		t.Fatalf("want 1 upserted item, got %v", h.upserts)
	}
	it := h.upserts[0][0]

	if it.GUID != "guid-1" || it.Title != "First item" {
		t.Errorf("item basics wrong: %+v", it)
	}
	// Relative link resolved against the item's link host.
	if !strings.Contains(it.BodyHTML, srv.URL+"/rel") {
		t.Errorf("relative href not absolutized: %s", it.BodyHTML)
	}
	// rel=nofollow applied by policy.
	if !strings.Contains(it.BodyHTML, "nofollow") {
		t.Errorf("nofollow missing: %s", it.BodyHTML)
	}
	if it.WordCount < 3 {
		t.Errorf("word count: got %d", it.WordCount)
	}
	if it.FullContent {
		t.Error("description-only item must not claim full content")
	}

	upd := h.lastUpdate(t)
	if upd.ETag == nil || *upd.ETag != `"v1"` {
		t.Errorf("etag not captured: %v", upd.ETag)
	}
	if upd.FailCount == nil || *upd.FailCount != 0 {
		t.Error("fail count not reset on success")
	}
	if upd.LastSuccess == nil {
		t.Error("last success not stamped on parsed 200")
	}
	if upd.Title == nil || *upd.Title != "Test Feed" {
		t.Errorf("self-reported title not stored: %v", upd.Title)
	}
}

func TestConditionalGet304(t *testing.T) {
	var sawINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawINM = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL, ETag: `"v1"`, FailCount: 2}})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if sawINM != `"v1"` {
		t.Errorf("If-None-Match not sent: %q", sawINM)
	}
	if len(h.upserts) != 0 {
		t.Errorf("304 must not upsert items: %v", h.upserts)
	}
	upd := h.lastUpdate(t)
	if upd.FailCount == nil || *upd.FailCount != 0 {
		t.Error("304 is a successful contact; fail count must reset")
	}
	if upd.LastSuccess != nil {
		t.Error("304 must not stamp LastSuccess (no items produced)")
	}
}

func TestPermanentRedirectPersisted(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, rssTemplate, srv.URL)
	})

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL + "/old"}})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	upd := h.lastUpdate(t)
	if upd.URL == nil || *upd.URL != srv.URL+"/new" {
		t.Errorf("permanent redirect not persisted: %v", upd.URL)
	}
}

func TestTemporaryRedirectNotPersisted(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/new", http.StatusFound) // 302
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, rssTemplate, srv.URL)
	})

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL + "/old"}})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if upd := h.lastUpdate(t); upd.URL != nil {
		t.Errorf("temporary redirect must not persist URL: %v", *upd.URL)
	}
}

func TestParseFailureSetsEPARSEAndBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "this is not a feed")
	}))
	defer srv.Close()

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL, FailCount: 1}})
	start := time.Now()
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	upd := h.lastUpdate(t)
	if upd.LastStatus == nil || *upd.LastStatus != firehose.EPARSE {
		t.Errorf("want EPARSE, got %v", upd.LastStatus)
	}
	if upd.FailCount == nil || *upd.FailCount != 2 {
		t.Errorf("fail count not incremented: %v", upd.FailCount)
	}
	// Second consecutive failure => 2h backoff.
	if upd.NextEarliest == nil || upd.NextEarliest.Before(start.Add(time.Hour)) {
		t.Errorf("backoff gate missing/short: %v", upd.NextEarliest)
	}
}

func TestNotFoundSetsENOTFOUND(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL}})
	_ = h.fetcher.Run(context.Background())
	upd := h.lastUpdate(t)
	if upd.LastStatus == nil || *upd.LastStatus != firehose.ENOTFOUND {
		t.Errorf("want ENOTFOUND, got %v", upd.LastStatus)
	}
}

// panicTransport forces a panic inside the fetch path to prove per-feed
// isolation: the run continues and the feed records EPANIC.
type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("hostile feed handler")
}

func TestPanicIsolatedToFeed(t *testing.T) {
	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: "http://irrelevant.invalid/feed"}})
	h.fetcher.client.Transport = panicTransport{}

	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run must survive a per-feed panic: %v", err)
	}
	upd := h.lastUpdate(t)
	if upd.LastStatus == nil || *upd.LastStatus != firehose.EPANIC {
		t.Errorf("want EPANIC, got %v", upd.LastStatus)
	}
	if upd.NextEarliest == nil {
		t.Error("panicked feed must be backed off")
	}
}

func TestBackoffProgression(t *testing.T) {
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{1, time.Hour}, {2, 2 * time.Hour}, {3, 4 * time.Hour},
		{6, 24 * time.Hour}, {10, 24 * time.Hour},
	}
	for _, c := range cases {
		if got := backoff(c.fails); got != c.want {
			t.Errorf("backoff(%d) = %v, want %v", c.fails, got, c.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	raw := `<p style="color:red" onclick="evil()">Hi <a href="/x">go</a></p>` +
		`<script>alert(1)</script>` +
		`<img src="pic.jpg" class="wp-image">` +
		`<pre><code class="language-go">x := "don't touch"</code></pre>` +
		`<iframe src="https://tracker.example"></iframe>`

	clean, words := Sanitize(raw, "https://blog.example/posts/1", nil)

	for _, banned := range []string{"style=", "onclick", "<script", "<iframe", "wp-image"} {
		if strings.Contains(clean, banned) {
			t.Errorf("%q survived sanitization: %s", banned, clean)
		}
	}
	if !strings.Contains(clean, `href="https://blog.example/x"`) {
		t.Errorf("relative href not absolutized: %s", clean)
	}
	if !strings.Contains(clean, `src="https://blog.example/posts/pic.jpg"`) {
		t.Errorf("relative img src not absolutized: %s", clean)
	}
	if !strings.Contains(clean, `loading="lazy"`) {
		t.Errorf("lazy loading not applied: %s", clean)
	}
	if !strings.Contains(clean, `class="language-go"`) {
		t.Errorf("declared-language class lost: %s", clean)
	}
	if !strings.Contains(clean, `x := &#34;don&#39;t touch&#34;`) && !strings.Contains(clean, `x := "don't touch"`) {
		t.Errorf("code content altered: %s", clean)
	}
	if words == 0 {
		t.Error("word count zero")
	}
}

func TestSanitizeEmptyAndGarbageBase(t *testing.T) {
	if clean, words := Sanitize("", "https://x.example/", nil); clean != "" || words != 0 {
		t.Errorf("empty input: %q %d", clean, words)
	}
	// Unparseable/relative base: links stay as-is but sanitization still runs.
	clean, _ := Sanitize(`<p onclick="x">hi <a href="/rel">r</a></p>`, "not a url", nil)
	if strings.Contains(clean, "onclick") {
		t.Errorf("sanitization must not depend on base URL: %s", clean)
	}
}

func TestStripSelectors(t *testing.T) {
	strip, err := CompileStrip([]string{"div.sharedaddy", "p.boiler"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	raw := `<p>keep me</p><div class="sharedaddy"><p>share junk</p></div><p class="boiler">subscribe!</p>`
	clean, _ := Sanitize(raw, "https://x.example/a", strip)
	if strings.Contains(clean, "share junk") || strings.Contains(clean, "subscribe!") {
		t.Errorf("strip selectors not applied: %s", clean)
	}
	if !strings.Contains(clean, "keep me") {
		t.Errorf("kept content lost: %s", clean)
	}
	if _, err := CompileStrip([]string{"div[unclosed"}); err == nil {
		t.Error("invalid selector must fail compilation")
	}
}

func TestNormalizeTypography(t *testing.T) {
	in := `<p>"Hello," she said -- it's a 'test'... done</p><pre><code>x = "raw" -- 'code'...</code></pre>`
	out := NormalizeTypography(in)
	for _, want := range []string{
		"\u201cHello,\u201d", // curly doubles
		"\u2014",             // em dash
		"it\u2019s",          // apostrophe
		"\u2018test\u2019",   // curly singles
		"\u2026 done",        // ellipsis
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	// Code untouched: straight quotes survive inside pre/code (entity-encoded
	// by the renderer; browsers decode them back to straight quotes).
	if !strings.Contains(out, `x = &#34;raw&#34; -- &#39;code&#39;...`) {
		t.Errorf("typography leaked into code: %s", out)
	}
	if strings.Contains(out, "<pre><code>x = \u201c") {
		t.Errorf("curly quotes inside code: %s", out)
	}
}

func TestHighlightDeclaredLanguageOnly(t *testing.T) {
	declared := `<pre><code class="language-go">package main</code></pre>`
	out := Highlight(declared)
	if !strings.Contains(out, "chroma") || !strings.Contains(out, "<span") {
		t.Errorf("declared language not highlighted: %s", out)
	}

	plain := `<pre><code>mystery(); code--here</code></pre>`
	if got := Highlight(plain); got != plain {
		t.Errorf("undeclared code must stay plain (never guess): %s", got)
	}

	unknown := `<pre><code class="language-nosuchlang">???</code></pre>`
	if got := Highlight(unknown); strings.Contains(got, "<span") {
		t.Errorf("unknown declared language must stay plain: %s", got)
	}
}

func TestKeywordFilters(t *testing.T) {
	fd := &firehose.Feed{Exclude: []string{"sponsored"}}
	if !skipByFilters(fd, "A Sponsored Post", "<p>buy things</p>") {
		t.Error("exclude keyword not applied to title")
	}
	if skipByFilters(fd, "Real News", "<p>content</p>") {
		t.Error("clean item wrongly excluded")
	}
	fd2 := &firehose.Feed{Include: []string{"wildfire"}}
	if skipByFilters(fd2, "Wildfire update", "<p>evacuation alert</p>") {
		t.Error("include match wrongly skipped")
	}
	if !skipByFilters(fd2, "Sports scores", "<p>hockey</p>") {
		t.Error("non-matching item must be skipped when include is set")
	}
}

func TestProbeSuccessWithRedirect(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" {
			t.Error("probe must send an Accept header")
		}
		http.Redirect(w, r, srv.URL+"/feed", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"p1"`)
		_, _ = fmt.Fprintf(w, rssTemplate, srv.URL)
	})

	p, err := RunProbe(context.Background(), firehose.DefaultFetchConfig(), ProbeRequest{URL: srv.URL + "/old"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(p.Hops) != 1 || p.Hops[0].Status != 301 {
		t.Errorf("redirect chain not recorded: %+v", p.Hops)
	}
	if p.FinalURL != srv.URL+"/feed" || p.Status != 200 || p.ETag != `"p1"` {
		t.Errorf("probe basics wrong: %+v", p)
	}
	if p.ItemCount != 1 || p.First == nil || p.First.GUID != "guid-1" {
		t.Errorf("first-item analysis wrong: %+v", p.First)
	}
	if p.First.FullContent {
		t.Error("description-only item must probe as teaser")
	}
}

func TestProbeParseFailureCapturesSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<html><body>Access Denied by SomeCDN</body></html>")
	}))
	defer srv.Close()

	p, err := RunProbe(context.Background(), firehose.DefaultFetchConfig(), ProbeRequest{URL: srv.URL})
	if firehose.ErrorCode(err) != firehose.EPARSE {
		t.Fatalf("want EPARSE, got %v", err)
	}
	if !strings.Contains(p.BodySnippet, "Access Denied") {
		t.Errorf("block-page snippet not captured: %q", p.BodySnippet)
	}
}

// TestClientSpeaksHTTP1Only pins the deliberate h2 opt-out: against an
// HTTP/2-capable TLS server, the fetch client must negotiate HTTP/1.1 —
// CDN h2 fingerprinting (the CBC stream-reset failure) is avoided by never
// speaking h2 at all.
func TestClientSpeaksHTTP1Only(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	client := newHTTPClient(firehose.DefaultFetchConfig())
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport type changed; update test")
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test cert only

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "HTTP/1.1" || resp.Proto != "HTTP/1.1" {
		t.Errorf("client negotiated %s / server saw %s; want HTTP/1.1", resp.Proto, body)
	}
}

// TestConfigOverlayReachesFetcher is the regression test for a real bug:
// filters/strips/overrides live only in config, but real runs source feeds
// from the DB, which does not persist them. Run() must overlay config onto
// stored rows — this test hands the fetcher a BARE feed (as the DB would)
// and asserts the config-declared exclude and UA override still apply.
func TestConfigOverlayReachesFetcher(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Sponsored garbage</title><link>http://%s/x</link><guid>s1</guid></item>
<item><title>Real item</title><link>http://%s/y</link><guid>r1</guid></item>
</channel></rss>`, r.Host, r.Host)
	}))
	defer srv.Close()

	// Bare feed, exactly as FindFeeds would return it from the cache.
	bare := &firehose.Feed{ID: 1, URL: srv.URL}
	h := newHarness(t, []*firehose.Feed{bare})
	// Config declares the overrides for that URL.
	h.fetcher.cfg.Feeds = []firehose.FeedConf{{
		URL:       srv.URL,
		Exclude:   []string{"sponsored"},
		UserAgent: "special-agent/9",
	}}
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotUA != "special-agent/9" {
		t.Errorf("per-feed UA override not applied: %q", gotUA)
	}
	if len(h.upserts) != 1 || len(h.upserts[0]) != 1 || h.upserts[0][0].GUID != "r1" {
		t.Errorf("config exclude not applied to DB-sourced feed: %+v", h.upserts)
	}
}

func TestAcceptLanguageSent(t *testing.T) {
	var gotAL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAL = r.Header.Get("Accept-Language")
		_, _ = fmt.Fprintf(w, rssTemplate, "http://"+r.Host)
	}))
	defer srv.Close()
	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL}})
	h.fetcher.cfg.Fetch.AcceptLanguage = "en-CA"
	_ = h.fetcher.Run(context.Background())
	if gotAL != "en-CA" {
		t.Errorf("Accept-Language not sent: %q", gotAL)
	}
}

func TestForceIgnoresBackoffGate(t *testing.T) {
	for _, force := range []bool{false, true} {
		var gotFilter firehose.FeedFilter
		h := newHarness(t, nil)
		feeds := h.fetcher.feeds.(*mock.FeedService)
		inner := feeds.FindFeedsFn
		feeds.FindFeedsFn = func(ctx context.Context, f firehose.FeedFilter) ([]*firehose.Feed, int, error) {
			gotFilter = f
			return inner(ctx, f)
		}
		h.fetcher.Force = force
		if err := h.fetcher.Run(context.Background()); err != nil {
			t.Fatalf("run: %v", err)
		}
		if gotFilter.DueOnly != !force {
			t.Errorf("force=%v: DueOnly=%v, want %v", force, gotFilter.DueOnly, !force)
		}
	}
}

// TestFetchAllItemsAgedOutDoesNotStampLastSuccess pins the honest
// LastSuccess semantics: a feed that parses fine but whose every entry
// falls to the retention gate yields no storable items — LastFetched
// stamps, LastSuccess does not, so quiet-feed detection sees the truth.
func TestFetchAllItemsAgedOutDoesNotStampLastSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Ancient Ministry</title>
<item><guid>old-1</guid><title>Long ago</title><link>https://x.example/old</link>
<pubDate>Tue, 09 Jun 2020 14:30:00 -0700</pubDate><description>ancient</description></item>
</channel></rss>`)
	}))
	defer srv.Close()

	h := newHarness(t, []*firehose.Feed{{ID: 1, URL: srv.URL}})
	if err := h.fetcher.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(h.updates) != 1 {
		t.Fatalf("want 1 update, got %d", len(h.updates))
	}
	upd := h.updates[0]
	if upd.LastFetched == nil {
		t.Error("fetch happened; LastFetched must stamp")
	}
	if upd.LastSuccess != nil {
		t.Error("no storable items; LastSuccess must NOT stamp")
	}
	if len(h.upserts) != 0 {
		t.Errorf("nothing should be stored, got %v", h.upserts)
	}
}
