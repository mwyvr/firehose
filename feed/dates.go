package feed

import (
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// Loose date parsing: a rescue pass for pubDate strings gofeed cannot parse.
// Noted with a civic CMS platforms (Govstack) that emits RFC822-shaped dates
// with a FULL month name and NO timezone — "Tue, 14 July 2026 23:00:00" — which
// no standard layout accepts.
//
// Zoneless timestamps are interpreted as UTC — matching gofeed's own
// convention for zoneless standard layouts, so both tiers agree. Live
// evidence: Govstack emits zoneless UTC (an item published 16:00 MST
// carries pubDate 23:00:00). If a CMS that publishes zoneless LOCAL time
// ever appears, a per-feed override is the escape hatch, not a different
// default.
var looseDateLayouts = []string{
	"Mon, 2 January 2006 15:04:05", // Govstack: full month, no zone
	"Mon, 2 Jan 2006 15:04:05",     // abbreviated cousin, no zone
	"2 January 2006 15:04:05",      // same, sans weekday
	"2006-01-02 15:04:05",          // bare SQL-style timestamp
}

// parseLooseDate attempts the rescue layouts against a raw date string.
// Zoneless input is UTC. Returns false if nothing matches.
func parseLooseDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range looseDateLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Date-resolution tiers, reported by resolvePublished and shown by
// `firehose test` so a date-blind feed announces itself at onboarding
// instead of days later as a "just now" ghost in the river.
const (
	DateFromFeed = "feed"    // gofeed parsed it (standard layouts)
	DateRescued  = "rescued" // a loose layout matched, config timezone
	DateNone     = ""        // no usable date: fetch-time fallback applies
)

// resolvePublished is the ONE definition of how an entry's published time
// is determined: gofeed's parse, then the loose-layout rescue over the raw
// strings. Returns the zero time with DateNone when nothing usable exists;
// the pipeline substitutes fetch time, and the probe reports it loudly.
func resolvePublished(entry *gofeed.Item) (time.Time, string) {
	switch {
	case entry.PublishedParsed != nil:
		return *entry.PublishedParsed, DateFromFeed
	case entry.UpdatedParsed != nil:
		return *entry.UpdatedParsed, DateFromFeed
	}
	if t, ok := parseLooseDate(entry.Published); ok {
		return t, DateRescued
	}
	if t, ok := parseLooseDate(entry.Updated); ok {
		return t, DateRescued
	}
	return time.Time{}, DateNone
}

// rawDate returns the entry's raw date string for diagnostics.
func rawDate(entry *gofeed.Item) string {
	if entry.Published != "" {
		return entry.Published
	}
	return entry.Updated
}
