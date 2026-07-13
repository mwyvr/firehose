package render

import (
	"bytes"
	"time"

	"github.com/mwyvr/firehose"
)

// Check dry-runs every template against a synthetic fixture so a broken
// template fails `firehose check`, not a live run.
func (r *Renderer) Check() error {
	full := true
	fixtureOut := &firehose.Output{Name: "check", File: "check.html", Title: "Check", InNav: true, ReadingTime: &full}
	nav := []navLink{{Title: "Check", File: "check.html", Current: true}}

	it := &firehose.Item{
		Title: "Fixture item", URL: "https://example.org/x", Author: "A. Writer",
		Published:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		BodyHTML:    "<p>Fixture body with <a href=\"https://example.org\" rel=\"nofollow\">a link</a>.</p>",
		SummaryHTML: "<p>Fixture summary.</p>",
		FullContent: true, WordCount: 240,
		Categories: []string{"check"},
	}
	fm := feedMeta{title: "Fixture Feed"}

	pv := pageView{
		ThemeAttr: r.themeAttr(),
		Version:   firehose.Version,
		SiteTitle: siteTitle, Title: "Check", Nav: nav,
		Items: []itemView{r.itemView(it, fixtureOut, fm, true)},
	}
	if err := r.river.Execute(&bytes.Buffer{}, pv); err != nil {
		return firehose.Errorf(firehose.EINVALID, "river template: %v", err)
	}

	hv := healthView{SiteTitle: siteTitle, ThemeAttr: r.themeAttr(), Version: firehose.Version, Nav: nav, Feeds: []healthRow{{
		Title: "Fixture", URL: "https://example.org/feed", Status: firehose.EPARSE,
		FailCount: 2, LastFetched: timeCell{Display: "—"},
		LastSuccess: timeCell{Display: "—"}, NextEarliest: timeCell{Display: "—"},
		Erroring: true,
	}}}
	if err := r.health.Execute(&bytes.Buffer{}, hv); err != nil {
		return firehose.Errorf(firehose.EINVALID, "health template: %v", err)
	}

	if err := r.css.Execute(&bytes.Buffer{}, fontsView(r.cfg)); err != nil {
		return firehose.Errorf(firehose.EINVALID, "css template: %v", err)
	}
	return nil
}
