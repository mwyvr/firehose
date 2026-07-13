package render

import (
	"bytes"
	"context"
	"html/template"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"

	"github.com/mwyvr/firehose"
)

// navLink is one entry in the cross-page nav strip.
type navLink struct {
	Title   string
	File    string
	Current bool
}

// itemView is the template-facing projection of an item
type itemView struct {
	ID       string
	Title    string
	URL      string
	HasLink  bool
	Section  string
	Datetime string
	Display  string
	Source   string
	AlsoVia  string // dedupe: other sources carrying this exact story
	Author   string
	ReadMins int
	NoteURL  string
	BodyHTML template.HTML
	Expand   bool
	FullHTML template.HTML
}

// pageView is the river template's data.
type pageView struct {
	SiteTitle string
	Title     string
	ThemeAttr string // baked config default ("" = auto); toggle overrides
	Version   string
	Nav       []navLink
	Items     []itemView
}

func (r *Renderer) renderRiver(ctx context.Context, out *firehose.Output, nav []navLink, meta map[int64]feedMeta) error {
	now := r.Now()
	items, _, err := r.items.FindItems(ctx, out.Filter(now.Add(-r.cfg.MaxDisplayWindow())))
	if err != nil {
		return err
	}
	items = r.inWindow(now, items, meta)

	var alsoVia map[*firehose.Item][]string
	if r.cfg.Settings.Dedupe {
		items, alsoVia = dedupeItems(items, meta, r.feedOrder(meta))
	}

	pv := pageView{
		ThemeAttr: r.themeAttr(),
		Version:   firehose.Version,
		SiteTitle: siteTitle,
		Title:     out.Title,
		Items:     make([]itemView, 0, len(items)),
	}
	// Mark the current page in a copy of the nav.
	pv.Nav = make([]navLink, len(nav))
	copy(pv.Nav, nav)
	for i := range pv.Nav {
		pv.Nav[i].Current = pv.Nav[i].File == out.File
	}

	isAll := out.IsAll()
	for _, it := range items {
		v := r.itemView(it, out, meta[it.FeedID], isAll)
		if via := alsoVia[it]; len(via) > 0 {
			v.AlsoVia = strings.Join(via, ", ")
		}
		pv.Items = append(pv.Items, v)
	}

	var buf bytes.Buffer
	if err := r.river.Execute(&buf, pv); err != nil {
		return firehose.Errorf(firehose.EINTERNAL, "render: %s: %v", out.File, err)
	}
	return atomicWrite(filepath.Join(r.cfg.Settings.OutputDir, out.File), buf.Bytes())
}

// itemView resolves everything the template needs for one item.
func (r *Renderer) itemView(it *firehose.Item, out *firehose.Output, fm feedMeta, isAll bool) itemView {
	s := r.cfg.Settings
	scope := firehose.ResolveBody(s.Body, out.Body, fm.conf.Body)
	img := firehose.ResolveExcerptImage(s.ExcerptImage, out.ExcerptImage, fm.conf.ExcerptImage)

	v := itemView{
		ID:       anchorID(fm.conf.URL, it),
		Title:    it.Title,
		URL:      it.URL,
		HasLink:  it.HasLink(),
		Datetime: it.Published.In(r.cfg.Location).Format(time.RFC3339),
		Display:  it.Published.In(r.cfg.Location).Format(timeDisplay),
		Source:   fm.title,
		Author:   it.Author,
	}
	if v.Title == "" {
		v.Title = "(untitled)"
	}
	if isAll && len(it.Categories) > 0 {
		v.Section = it.Categories[0]
	}
	if readingTimeOn(s.ReadingTime, out.ReadingTime) && it.WordCount > 0 {
		v.ReadMins = int(it.ReadingTime().Minutes())
	}
	if s.NoteTemplate != "" && it.URL != "" {
		v.NoteURL = noteURL(s.NoteTemplate, it.Title, it.URL)
	}

	switch scope {
	case firehose.BodyTitle:
		// headline only
	case firehose.BodyFull:
		v.BodyHTML = template.HTML(it.BodyHTML)
	case firehose.BodyExcerptExpand:
		v.BodyHTML = template.HTML(excerpt(it, s.ExcerptWords, img))
		if it.FullContent {
			v.Expand = true
			v.FullHTML = template.HTML(it.BodyHTML)
		}
		// teaser-only: silent fallback to plain excerpt; the title-link is
		// the escape hatch.
	default: // BodyExcerpt
		v.BodyHTML = template.HTML(excerpt(it, s.ExcerptWords, img))
	}
	return v
}

func readingTimeOn(global bool, override *bool) bool {
	if override != nil {
		return *override
	}
	return global
}

// noteURL substitutes {title} and {url} (query-escaped) into the configured
// note template; other placeholders are removed.
func noteURL(tmpl, title, itemURL string) string {
	repl := strings.NewReplacer(
		"{title}", url.QueryEscape(title),
		"{url}", url.QueryEscape(itemURL),
		"{quote}", "",
	)
	return repl.Replace(tmpl)
}

// anchorID derives the item's stable DOM id. The basis is content identity
// (GUID, else URL) prefixed with the FEED's URL: the same story arriving via
// two feeds must still get distinct ids — HTML requires unique ids, and the
// read marker addresses elements by them. Content-derived, so anchors
// survive cache rebuilds; feed-scoped, so they survive duplicates.
func anchorID(feedURL string, it *firehose.Item) string {
	basis := it.GUID
	if basis == "" {
		basis = it.URL
	}
	if basis == "" {
		return it.ID.String() // no stable identity; kid.ID is the best left
	}
	sum := sha256.Sum256([]byte(feedURL + "|" + basis))
	return hex.EncodeToString(sum[:6])
}

// inWindow narrows the widest-window query result to each feed's own
// display window (per-feed override, else the global setting).
func (r *Renderer) inWindow(now time.Time, items []*firehose.Item, meta map[int64]feedMeta) []*firehose.Item {
	kept := items[:0]
	for _, it := range items {
		if !it.Published.Before(now.Add(-r.cfg.WindowFor(meta[it.FeedID].conf))) {
			kept = append(kept, it)
		}
	}
	return kept
}

// feedOrder maps stored feed IDs to config order — the dedupe tiebreaker
// ("earlier in the config wins" is the operator's preference ranking).
func (r *Renderer) feedOrder(meta map[int64]feedMeta) map[int64]int {
	byURL := map[string]int{}
	for i, fc := range r.cfg.Feeds {
		byURL[fc.URL] = i
	}
	order := map[int64]int{}
	for id, fm := range meta {
		order[id] = byURL[fm.conf.URL]
	}
	return order
}
