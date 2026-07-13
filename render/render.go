// Package render turns cached items into the static site.
//
// One renderer pass (RenderAll) produces every configured river page, the
// unlinked firehose.html health page, and the assets. Output is
// deterministic for a given cache state — absolute timestamps in markup
// (river.js humanizes at VIEW time), stable ordering, content-derived item
// anchors that survive cache rebuilds — and every file is written atomically
// (temp+rename, world-readable 0644/0755 by design: the output is public
// web content served by a different user).
//
// Presentation follows the one-voice rules: excerpts prefer a feed-provided
// summary, truncate the parsed tree (never a serialized string) and
// re-sanitize, and drop code blocks; theme resolution is user toggle over
// config default over OS preference, applied to tokens and the chroma
// palette alike.
//
// Check dry-runs every template against synthetic fixtures so a broken
// template fails `firehose check`, not a live run.
package render

import (
	"context"
	"embed"
	"html/template"
	ttemplate "text/template"
	"time"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/mwyvr/firehose"
)

//go:embed templates/*.tmpl assets/*
var embedded embed.FS

// timeDisplay is the deterministic meta-line format: absolute, in the
// configured location. Relative times ("3h ago") would change every run and
// break the byte-identical no-change property.
const timeDisplay = "Jan 2, 2006 15:04"

// Renderer renders all outputs for one run.
type Renderer struct {
	cfg   *firehose.Config
	items firehose.ItemService
	feeds firehose.FeedService

	river  *template.Template
	health *template.Template

	// css is a text/template on purpose: the stylesheet is not HTML, and
	// html/template's CSS-context escaping would mangle font URLs (the
	// ampersands in a Google Fonts query string).
	css *ttemplate.Template

	// Now is injectable for tests.
	Now func() time.Time
}

// New constructs a Renderer, parsing the embedded templates.
func New(cfg *firehose.Config, items firehose.ItemService, feeds firehose.FeedService) (*Renderer, error) {
	r := &Renderer{cfg: cfg, items: items, feeds: feeds, Now: time.Now}
	var err error
	if r.river, err = template.ParseFS(embedded, "templates/river.tmpl"); err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "render: parse river template: %v", err)
	}
	if r.health, err = template.ParseFS(embedded, "templates/health.tmpl"); err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "render: parse health template: %v", err)
	}
	if r.css, err = ttemplate.ParseFS(embedded, "assets/style.css.tmpl"); err != nil {
		return nil, firehose.Errorf(firehose.EINTERNAL, "render: parse css template: %v", err)
	}
	if cfg.Settings.Highlight {
		for _, name := range []string{cfg.Settings.HighlightTheme, cfg.Settings.HighlightThemeDark} {
			if _, ok := styles.Registry[name]; !ok {
				return nil, firehose.Errorf(firehose.EINVALID,
					"render: unknown chroma style %q (see chroma's style gallery for names)", name)
			}
		}
	}
	return r, nil
}

// feedMeta joins the stored feed row with its config block for scope
// resolution and source attribution.
type feedMeta struct {
	title string
	conf  firehose.FeedConf
}

// RenderAll renders every output (rivers + health) plus the shared assets.
func (r *Renderer) RenderAll(ctx context.Context) error {
	if err := r.writeAssets(); err != nil {
		return err
	}
	meta, err := r.feedMeta(ctx)
	if err != nil {
		return err
	}
	outputs := r.cfg.ToOutputs()
	nav := buildNav(outputs)
	for _, out := range outputs {
		if out.Health {
			if err := r.renderHealth(ctx, out, nav); err != nil {
				return err
			}
			continue
		}
		if err := r.renderRiver(ctx, out, nav, meta); err != nil {
			return err
		}
	}
	return nil
}

func buildNav(outputs []*firehose.Output) []navLink {
	var nav []navLink
	for _, o := range outputs {
		if !o.InNav {
			continue
		}
		title := o.Title
		if title == "" {
			title = o.Name
		}
		nav = append(nav, navLink{Title: title, File: o.File})
	}
	return nav
}

// feedMeta maps stored feed IDs to their title and config block.
func (r *Renderer) feedMeta(ctx context.Context) (map[int64]feedMeta, error) {
	stored, _, err := r.feeds.FindFeeds(ctx, firehose.FeedFilter{})
	if err != nil {
		return nil, err
	}
	byURL := r.cfg.FeedConfByURL()
	meta := map[int64]feedMeta{}
	for _, f := range stored {
		meta[f.ID] = feedMeta{title: f.Title, conf: byURL[f.URL]}
	}
	return meta, nil
}

// themeAttr is the baked data-theme value: empty for auto (media query
// decides), the theme name when config forces one.
func (r *Renderer) themeAttr() string {
	if t := r.cfg.Settings.Theme; t == "light" || t == "dark" {
		return t
	}
	return ""
}

// siteTitle is the fixed site name shown in every page header.
const siteTitle = "firehose"
