package feed

import (
	"strings"
	"time"
)

// Loose date parsing: a rescue pass for pubDate strings gofeed cannot parse.
// Noted with a civic CMS platforms (Govstack) that emits RFC822-shaped dates
// with a FULL month name and NO timezone — "Tue, 14 July 2026 23:00:00" — which
// no standard layout accepts.
//
// Zoneless timestamps are interpreted in the configured timezone: these formats
// come from local-government sites publishing in local time, and the operator's
// timezone is the best available guess. Hours off beats years off.
var looseDateLayouts = []string{
	"Mon, 2 January 2006 15:04:05", // Govstack: full month, no zone
	"Mon, 2 Jan 2006 15:04:05",     // abbreviated cousin, no zone
	"2 January 2006 15:04:05",      // same, sans weekday
	"2006-01-02 15:04:05",          // bare SQL-style timestamp
}

// parseLooseDate attempts the rescue layouts against a raw date string,
// in the given location. Returns false if nothing matches.
func parseLooseDate(raw string, loc *time.Location) (time.Time, bool) {
	if loc == nil { // resolved configs always carry one; never panic without
		loc = time.Local
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range looseDateLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
