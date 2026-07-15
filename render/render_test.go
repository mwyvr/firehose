package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/firehose/mock"
	"github.com/mwyvr/kid"
)

func testConfig(t *testing.T) *firehose.Config {
	t.Helper()
	loc, _ := time.LoadLocation("UTC")
	return &firehose.Config{
		Settings: firehose.Settings{
			OutputDir:     filepath.Join(t.TempDir(), "out"),
			DisplayWindow: firehose.Duration(14 * 24 * time.Hour),
			Body:          firehose.BodyExcerpt,
			ExcerptWords:  10,
			ExcerptImage:  firehose.ExcerptImageNone,
			ReadingTime:   true,
		},
		Outputs: []firehose.OutputConf{
			{Name: "all", File: "index.html", Title: "Everything", Categories: []string{"*"}},
			{Name: "tech", File: "tech.html", Title: "Tech", Categories: []string{"tech"}, Body: firehose.BodyExcerptExpand},
		},
		Feeds: []firehose.FeedConf{
			{URL: "https://a.example/feed", Categories: []string{"tech"}},
		},
		Location: loc,
	}
}

func fixedItem(title string, full bool) *firehose.Item {
	return &firehose.Item{
		ID:          kid.New(),
		FeedID:      1,
		Title:       title,
		URL:         "https://a.example/posts/1",
		Published:   time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
		BodyHTML:    "<p>one two three four five six seven eight nine ten eleven twelve</p>",
		FullContent: full,
		WordCount:   240,
		Categories:  []string{"tech"},
	}
}

func newRenderer(t *testing.T, cfg *firehose.Config, items []*firehose.Item) *Renderer {
	t.Helper()
	is := &mock.ItemService{
		FindItemsFn: func(ctx context.Context, f firehose.ItemFilter) ([]*firehose.Item, int, error) {
			if len(f.Categories) == 0 {
				return items, len(items), nil
			}
			var out []*firehose.Item
			for _, it := range items {
				for _, c := range it.Categories {
					for _, w := range f.Categories {
						if c == w {
							out = append(out, it)
						}
					}
				}
			}
			return out, len(out), nil
		},
	}
	fs := &mock.FeedService{
		FindFeedsFn: func(ctx context.Context, f firehose.FeedFilter) ([]*firehose.Feed, int, error) {
			return []*firehose.Feed{{
				ID: 1, URL: "https://a.example/feed", Title: "A Feed",
				Categories:  []string{"tech"},
				LastFetched: time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC),
				LastSuccess: time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC),
			}}, 1, nil
		},
	}
	r, err := New(cfg, is, fs)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	r.Now = func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }
	return r
}

func TestRenderAllSemanticOutput(t *testing.T) {
	cfg := testConfig(t)
	it := fixedItem("Hello World", true)
	r := newRenderer(t, cfg, []*firehose.Item{it})

	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatalf("render: %v", err)
	}

	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))

	for _, want := range []string{
		"<h1 class=\"page__title\">Everything</h1>",
		"<h2 class=\"feeditem__title\"><a href=\"https://a.example/posts/1\">Hello World</a></h2>",
		"<time datetime=\"2026-07-06T10:00:00Z\">",
		"<article class=\"feeditem\" id=\"i-" + anchorID("https://a.example/feed", it) + "\">",
		"A Feed", // source attribution
		"~2 min", // 240 words / 220 wpm, rounded up
		"class=\"feeditem__section\">tech</span>", // section label on ALL river
		"ten\u2026", // 10-word excerpt: words 1-10 kept, then ellipsis
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
	if strings.Contains(index, "eleven") || strings.Contains(index, "twelve") {
		t.Error("excerpt not truncated at word budget")
	}
	// ALL river uses excerpt: no expand details here.
	if strings.Contains(index, "<details") {
		t.Error("ALL river must not carry expand details (body=excerpt)")
	}

	// Section page uses excerpt+expand: full body present, collapsed.
	tech := readFile(t, filepath.Join(cfg.Settings.OutputDir, "tech.html"))
	if !strings.Contains(tech, "<details class=\"feeditem__more\">") {
		t.Error("tech.html missing details expand")
	}
	if !strings.Contains(tech, "twelve") {
		t.Error("expanded full body missing from tech.html")
	}
	// Section page omits the section label (redundant there).
	if strings.Contains(tech, "feeditem__section") {
		t.Error("section label must not appear on a section page")
	}

	// Assets written.
	for _, f := range []string{"style.css", "river.js", "firehose.html"} {
		if _, err := os.Stat(filepath.Join(cfg.Settings.OutputDir, f)); err != nil {
			t.Errorf("missing output %s: %v", f, err)
		}
	}
	// Health page is not linked from the nav.
	if strings.Contains(index, "firehose.html") {
		t.Error("health page leaked into the nav")
	}
	// Health page: categories reminder + honest backoff column name.
	health := readFile(t, filepath.Join(cfg.Settings.OutputDir, "firehose.html"))
	if !strings.Contains(health, `<span class="health__cats">tech</span>`) {
		t.Error("health page missing feed categories reminder")
	}
	if !strings.Contains(health, "backoff until") || strings.Contains(health, "next attempt") {
		t.Error("backoff column not renamed honestly")
	}
	// Footer names the build.
	if !strings.Contains(index, `firehose</a> · dev`) {
		t.Error("footer missing version")
	}
	// Icon assets answer the requests browsers make unconditionally.
	for _, f := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png", "apple-touch-icon-precomposed.png"} {
		if _, err := os.Stat(filepath.Join(cfg.Settings.OutputDir, f)); err != nil {
			t.Errorf("icon asset missing: %s", f)
		}
	}
	if !strings.Contains(index, `<link rel="icon" href="favicon.svg" type="image/svg+xml">`) {
		t.Error("svg favicon link missing from head")
	}
	// Operational timestamps are <time datetime> so the client humanizer works.
	if !strings.Contains(health, `<time datetime="`) {
		t.Error("health timestamps missing time elements")
	}
	// One-directional: health links to the rivers (rivers never link back).
	if !strings.Contains(health, `href="index.html"`) || !strings.Contains(health, `href="tech.html"`) {
		t.Error("health page missing nav links to the rivers")
	}
}

func TestExpandGatedByFullContent(t *testing.T) {
	cfg := testConfig(t)
	teaser := fixedItem("Teaser Only", false)
	r := newRenderer(t, cfg, []*firehose.Item{teaser})
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatalf("render: %v", err)
	}
	tech := readFile(t, filepath.Join(cfg.Settings.OutputDir, "tech.html"))
	if strings.Contains(tech, "<details") {
		t.Error("expand offered for teaser-only item; must silently fall back to excerpt")
	}
}

func TestLinklessTitleRendersPlain(t *testing.T) {
	cfg := testConfig(t)
	it := fixedItem("No Link Here", true)
	it.URL = ""
	r := newRenderer(t, cfg, []*firehose.Item{it})
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatalf("render: %v", err)
	}
	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if !strings.Contains(index, "<h2 class=\"feeditem__title\">No Link Here</h2>") {
		t.Error("linkless title not rendered as plain text")
	}
	if strings.Contains(index, "href=\"\"") {
		t.Error("dead anchor rendered for linkless item")
	}
}

func TestDeterministicOutput(t *testing.T) {
	cfg := testConfig(t)
	it := fixedItem("Stable", true)
	r := newRenderer(t, cfg, []*firehose.Item{it})

	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if first != second {
		t.Error("same cache state must render byte-identical output")
	}
}

func TestExcerptImageLead(t *testing.T) {
	it := fixedItem("Pics", true)
	it.LeadImage = "https://a.example/lead.jpg"
	out := excerpt(it, 5, firehose.ExcerptImageLead)
	if !strings.Contains(out, "https://a.example/lead.jpg") || !strings.Contains(out, `loading="lazy"`) {
		t.Errorf("lead image not restored: %s", out)
	}
	if none := excerpt(it, 5, firehose.ExcerptImageNone); strings.Contains(none, "lead.jpg") {
		t.Errorf("image survived excerpt_image=none: %s", none)
	}
}

func TestExcerptPrefersSummaryAndDropsCode(t *testing.T) {
	it := fixedItem("Summary", true)
	it.SummaryHTML = "<p>the summary text</p>"
	out := excerpt(it, 50, firehose.ExcerptImageNone)
	if !strings.Contains(out, "the summary text") || strings.Contains(out, "one two three") {
		t.Errorf("excerpt must prefer feed summary: %s", out)
	}

	it2 := fixedItem("Code", true)
	it2.SummaryHTML = ""
	it2.BodyHTML = "<p>intro words</p><pre><code>func main() {}</code></pre><p>after</p>"
	out2 := excerpt(it2, 50, firehose.ExcerptImageNone)
	if strings.Contains(out2, "func main") {
		t.Errorf("code block must be dropped from excerpts, never truncated: %s", out2)
	}
	if !strings.Contains(out2, "intro words") || !strings.Contains(out2, "after") {
		t.Errorf("surrounding prose lost: %s", out2)
	}
}

func TestCheckPasses(t *testing.T) {
	cfg := testConfig(t)
	r := newRenderer(t, cfg, nil)
	if err := r.Check(); err != nil {
		t.Fatalf("check on shipped templates must pass: %v", err)
	}
}

func TestFallbackHealthWriter(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFallbackHealth(dir, "panic: boom", "stack trace here"); err != nil {
		t.Fatalf("fallback write: %v", err)
	}
	out := readFile(t, filepath.Join(dir, "firehose.html"))
	if !strings.Contains(out, "run failed") || !strings.Contains(out, "panic: boom") {
		t.Errorf("fallback page content wrong: %s", out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestStylesheetFontModes(t *testing.T) {
	// Remote default: @import with the URL intact (ampersands unmangled).
	cfg := testConfig(t)
	cfg.Fonts = firehose.FontConfig{
		ContentFamily: "Crimson Pro", ChromeFamily: "IBM Plex Sans",
		CSSURL: "https://fonts.example/css2?family=A&family=B&display=swap",
	}
	r := newRenderer(t, cfg, nil)
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	css := readFile(t, filepath.Join(cfg.Settings.OutputDir, "style.css"))
	if !strings.Contains(css, `@import url("https://fonts.example/css2?family=A&family=B&display=swap");`) {
		t.Errorf("remote font import missing or mangled:\n%s", css[:200])
	}
	if strings.Contains(css, "&amp;") {
		t.Error("font URL HTML-escaped; css must render via text/template")
	}

	// Self-hosted: @font-face, no import.
	cfg2 := testConfig(t)
	cfg2.Fonts = firehose.FontConfig{
		ContentFamily: "Crimson Pro", ChromeFamily: "IBM Plex Sans",
		ContentSrc: "/fonts/CrimsonPro.woff2",
	}
	r2 := newRenderer(t, cfg2, nil)
	if err := r2.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	css2 := readFile(t, filepath.Join(cfg2.Settings.OutputDir, "style.css"))
	if !strings.Contains(css2, `@font-face`) || !strings.Contains(css2, "/fonts/CrimsonPro.woff2") {
		t.Error("self-hosted @font-face missing")
	}
	if strings.Contains(css2, "@import") {
		t.Error("remote import emitted alongside self-hosted fonts")
	}
}

func TestChromaCSSAndThemeValidation(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settings.Highlight = true
	cfg.Settings.HighlightTheme = "github"
	cfg.Settings.HighlightThemeDark = "github-dark"
	r := newRenderer(t, cfg, nil)
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	css := readFile(t, filepath.Join(cfg.Settings.OutputDir, "style.css"))
	if !strings.Contains(css, ".chroma") {
		t.Error("chroma classes missing from stylesheet")
	}
	if !strings.Contains(css, "@media (prefers-color-scheme: dark) {\n/* Background") &&
		!strings.Contains(css, "@media (prefers-color-scheme: dark)") {
		t.Error("dark-mode chroma block missing")
	}

	bad := testConfig(t)
	bad.Settings.Highlight = true
	bad.Settings.HighlightTheme = "no-such-style"
	bad.Settings.HighlightThemeDark = "github-dark"
	if _, err := New(bad, nil, nil); firehose.ErrorCode(err) != firehose.EINVALID {
		t.Errorf("unknown chroma style must be EINVALID, got %v", err)
	}
}

// TestExcerptPreservesInterNodeWhitespace is the regression test for the
// "Route 25takes" fusion: the space separating an inline element from the
// following text lives at the start of the adjacent text node, and the old
// Fields+Join truncation ate it.
func TestExcerptPreservesInterNodeWhitespace(t *testing.T) {
	it := &firehose.Item{
		BodyHTML: `<p>Many apps make it easy to track cards. <a href="https://x.example/">Route 25</a> takes that a step further by bringing a social experience to all of it</p>`,
	}
	out := excerpt(it, 12, firehose.ExcerptImageNone)
	if !strings.Contains(out, `Route 25</a> takes`) {
		t.Errorf("inline element fused with following word:\n%s", out)
	}
	if !strings.Contains(out, "\u2026") {
		t.Errorf("expected a truncated excerpt with ellipsis:\n%s", out)
	}

	// Cut landing mid-node after a bold element: leading space survives too.
	it2 := &firehose.Item{BodyHTML: `<p>alpha <b>beta</b> gamma delta epsilon zeta</p>`}
	out2 := excerpt(it2, 3, firehose.ExcerptImageNone)
	if !strings.Contains(out2, "beta</b> gamma…") {
		t.Errorf("mid-node cut lost leading space:\n%s", out2)
	}
}

// TestAnchorsStableAcrossCacheRebuilds: read markers store anchors in
// localStorage; anchors must derive from item identity, not from kid.IDs
// that a disposable-cache rebuild regenerates.
func TestAnchorsStableAcrossCacheRebuilds(t *testing.T) {
	a := &firehose.Item{ID: kid.New(), GUID: "guid-x", URL: "https://x.example/p"}
	b := &firehose.Item{ID: kid.New(), GUID: "guid-x", URL: "https://x.example/p"}
	if a.ID == b.ID {
		t.Fatal("test premise: distinct kid.IDs")
	}
	if anchorID("https://f.example/rss", a) != anchorID("https://f.example/rss", b) {
		t.Errorf("anchor changed across rebuild")
	}
	if anchorID("https://f.example/rss", a) == anchorID("https://f.example/rss", &firehose.Item{GUID: "other"}) {
		t.Error("distinct items must get distinct anchors")
	}
}

// TestAtomicWriteWorldReadable pins the 0644 chmod inside atomicWrite. The
// output is public web content served by a DIFFERENT user (mox, caddy,
// nginx); os.CreateTemp creates 0600, and losing the explicit chmod in a
// refactor would 403 an entire deployment.
func TestAtomicWriteWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "page.html")
	if err := atomicWrite(path, []byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("output file mode = %o, want 644 (world-readable by design)", got)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o755 {
		t.Errorf("output dir mode = %o, want 755", got)
	}
}

func TestPrefixSelectors(t *testing.T) {
	in := "/* Error */ .chroma .err { color: #f00; }\n/* Two */ .a, .b { x: y; }\nnot a rule\n"
	out := prefixSelectors(in, `:root[data-theme="dark"] `)
	for _, w := range []string{
		`/* Error */ :root[data-theme="dark"] .chroma .err { color: #f00; }`,
		`:root[data-theme="dark"] .a, :root[data-theme="dark"] .b { x: y; }`,
		"not a rule",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestThemeForcedInMarkupAndCSS(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settings.Theme = "dark"
	cfg.Settings.Highlight = true
	cfg.Settings.HighlightTheme = "github"
	cfg.Settings.HighlightThemeDark = "github-dark"
	r := newRenderer(t, cfg, nil)
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if !strings.Contains(index, `<html lang="en" data-theme="dark">`) {
		t.Error("config theme not baked into markup")
	}
	if !strings.Contains(index, `button class="themetoggle"`) {
		t.Error("theme toggle missing from nav")
	}
	if !strings.Contains(index, "data-theme-default") {
		t.Error("pre-paint head script missing")
	}
	css := readFile(t, filepath.Join(cfg.Settings.OutputDir, "style.css"))
	for _, want := range []string{
		`:root[data-theme="dark"] {`,
		`:root:not([data-theme="light"]) {`,
		`:root[data-theme="dark"] .chroma`,
		`:root:not([data-theme="light"]) .chroma`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet missing %q", want)
		}
	}
}

// TestAnchorFeedScoped pins the duplicate-id fix: the same story (same GUID
// and URL) arriving via two different feeds must yield distinct DOM ids.
func TestAnchorFeedScoped(t *testing.T) {
	it := &firehose.Item{GUID: "https://x.example/story", URL: "https://x.example/story"}
	a := anchorID("https://feedA.example/rss", it)
	b := anchorID("https://feedB.example/rss", it)
	if a == b {
		t.Fatalf("same anchor for two feeds: %s", a)
	}
}

// TestPerFeedWindowRendersSlowFeed pins the EMCR case: an item outside the
// global window but inside its feed's own window renders; the health page
// shows it in the items column.
func TestPerFeedWindowRendersSlowFeed(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settings.Dedupe = true
	cfg.Feeds[0].DisplayWindow = firehose.Duration(60 * 24 * time.Hour)
	old := fixedItem("EMCR Release", true)
	// renderer "now" is 2026-07-11 12:00 UTC; 20 days back: outside 14d, inside 60d
	old.Published = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	r := newRenderer(t, cfg, []*firehose.Item{old})
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if !strings.Contains(index, "EMCR Release") {
		t.Error("per-feed window did not rescue the old item")
	}
}

// TestDedupeAlsoViaInMarkup pins the rendered attribution: two feeds, one
// story, one article element, "also via" naming the echo.
func TestDedupeAlsoViaInMarkup(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settings.Dedupe = true
	cfg.Feeds = append(cfg.Feeds, firehose.FeedConf{
		URL: "https://b.example/feed", Categories: []string{"tech"}, Title: "Echo Feed",
	})
	origin := fixedItem("One Story", true)
	echo := fixedItem("One Story", false)
	echo.FeedID = 2
	echo.GUID = "echo-guid"
	echo.URL = origin.URL + "?utm_source=rss"
	echo.Published = origin.Published.Add(time.Hour)
	r := newTwoFeedRenderer(t, cfg, []*firehose.Item{echo, origin})
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if got := strings.Count(index, "<article"); got != 1 {
		t.Fatalf("dedupe failed: %d articles", got)
	}
	if !strings.Contains(index, "also via Echo Feed") {
		t.Error("attribution missing")
	}
}

// newTwoFeedRenderer: the single-feed fixture plus an Echo Feed (ID 2).
func newTwoFeedRenderer(t *testing.T, cfg *firehose.Config, items []*firehose.Item) *Renderer {
	t.Helper()
	is := &mock.ItemService{
		FindItemsFn: func(ctx context.Context, f firehose.ItemFilter) ([]*firehose.Item, int, error) {
			return items, len(items), nil
		},
	}
	fs := &mock.FeedService{
		FindFeedsFn: func(ctx context.Context, f firehose.FeedFilter) ([]*firehose.Feed, int, error) {
			return []*firehose.Feed{
				{ID: 1, URL: "https://a.example/feed", Title: "A Feed", Categories: []string{"tech"}},
				{ID: 2, URL: "https://b.example/feed", Title: "Echo Feed", Categories: []string{"tech"}},
			}, 2, nil
		},
	}
	r, err := New(cfg, is, fs)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}
	r.Now = func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }
	return r
}

// TestLinksNewTab pins the render-time <base target="_blank"> option: off
// by default (standard web behavior), and when enabled the base tag
// appears in the head while site navigation is pinned target="_self" so
// section switching never spawns tabs. Stored body HTML is untouched —
// the whole mechanism is render-time, so toggling takes effect on the
// next generate with no cache implications.
func TestLinksNewTab(t *testing.T) {
	cfg := testConfig(t)
	it := fixedItem("Hello World", true)
	r := newRenderer(t, cfg, []*firehose.Item{it})
	if err := r.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(cfg.Settings.OutputDir, "index.html"))
	if strings.Contains(index, "<base") {
		t.Error("base tag must be absent by default")
	}

	cfg2 := testConfig(t)
	cfg2.Settings.LinksNewTab = true
	r2 := newRenderer(t, cfg2, []*firehose.Item{it})
	if err := r2.RenderAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	index2 := readFile(t, filepath.Join(cfg2.Settings.OutputDir, "index.html"))
	if !strings.Contains(index2, `<base target="_blank">`) {
		t.Error("base tag missing when enabled")
	}
	if !strings.Contains(index2, `target="_self"`) {
		t.Error("nav links must be pinned same-tab when enabled")
	}
	health := readFile(t, filepath.Join(cfg2.Settings.OutputDir, "firehose.html"))
	if !strings.Contains(health, `<base target="_blank">`) {
		t.Error("health page must honor the option too")
	}
}
