package feed

import (
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"github.com/mmcdole/gofeed"
	"github.com/mwyvr/firehose"
)

// convert maps parsed entries to storable items. Entries older than the
// retention cutoff, without any dedupe identity, or rejected by the feed's
// keyword filters produce nothing; one bad entry never affects its
// neighbors.
func (f *Fetcher) convert(fd *firehose.Feed, parsed *gofeed.Feed, now time.Time, strip []cascadia.SelectorGroup) []*firehose.Item {
	loc := feedLocation(fd.Timezone)
	cutoff := now.Add(-f.cfg.Settings.CacheRetention.D())
	items := make([]*firehose.Item, 0, len(parsed.Items))
	for _, entry := range parsed.Items {
		if entry == nil {
			continue
		}
		if item := f.itemFromEntry(fd, entry, now, cutoff, strip, loc); item != nil {
			items = append(items, item)
		}
	}
	return items
}

// itemFromEntry converts one entry, or returns nil to skip it. The content
// pipeline order:
//
//	select body -> sanitize -> typography -> highlight -> filters
//
// Sanitize must run first (everything downstream assumes clean, absolutized
// HTML); typography and highlight both refuse to touch code, which only
// works on a parsed-and-normalized tree; filters match against the final
// text a reader would see.
func (f *Fetcher) itemFromEntry(fd *firehose.Feed, entry *gofeed.Item, now, cutoff time.Time, strip []cascadia.SelectorGroup, loc *time.Location) *firehose.Item {
	published := publishedTime(entry, now, loc)
	if published.Before(cutoff) {
		return nil // would be purged immediately; skip the work
	}
	guid := entryGUID(entry)
	if guid == "" {
		return nil // nothing to dedupe on; unusable entry
	}

	raw, summaryRaw, fullContent := selectBody(entry)
	base := entry.Link
	if base == "" {
		base = fd.URL
	}
	clean, summary, words := f.renderVoice(raw, summaryRaw, base, strip)

	link := firehose.RewriteHost(entry.Link, fd.RewriteHost)
	if skipByFilters(fd, entry.Title, clean, summary, link) {
		return nil
	}

	return &firehose.Item{
		FeedID: fd.ID,
		GUID:   firehose.RewriteHost(guid, fd.RewriteHost),
		Title:  entry.Title,
		// may be empty: linkless-title rule at render
		URL:         link,
		Author:      entryAuthor(entry),
		Published:   published.UTC(),
		BodyHTML:    clean,
		SummaryHTML: summary,
		LeadImage:   leadImage(entry, clean),
		FullContent: fullContent,
		WordCount:   words,
		FetchedAt:   now.UTC(),
	}
}

// selectBody applies the body precedence: content:encoded (full) over
// description (teaser). When both exist the description is kept separately
// as the summary — the excerpt engine prefers a feed-provided summary over
// truncating the full body.
func selectBody(entry *gofeed.Item) (raw, summaryRaw string, fullContent bool) {
	raw = entry.Content
	fullContent = raw != ""
	if raw == "" {
		raw = entry.Description
	} else {
		summaryRaw = entry.Description
	}
	return raw, summaryRaw, fullContent
}

// renderVoice runs the one-voice content pipeline over a body and its
// optional summary: sanitize (strip selectors, absolutize, structural
// policy), then typography normalization, then declared-language
// highlighting. Word count is measured on the sanitized body.
func (f *Fetcher) renderVoice(raw, summaryRaw, base string, strip []cascadia.SelectorGroup) (clean, summary string, words int) {
	clean, words = sanitize(raw, base, strip)
	if summaryRaw != "" {
		summary, _ = sanitize(summaryRaw, base, strip)
	}
	if f.cfg.Settings.Typography {
		clean = normalizeTypography(clean)
		if summary != "" {
			summary = normalizeTypography(summary)
		}
	}
	if f.cfg.Settings.Highlight {
		clean = highlight(clean)
	}
	return clean, summary, words
}

// publishedTime prefers the published stamp, falls back to updated, and
// finally to the fetch time (an undated item is treated as new).
func publishedTime(entry *gofeed.Item, now time.Time, loc *time.Location) time.Time {
	if t, tier := resolvePublished(entry, loc); tier != DateNone {
		return t
	}
	return now
}

// entryGUID derives the dedupe identity: guid, then link, then title.
func entryGUID(entry *gofeed.Item) string {
	if entry.GUID != "" {
		return entry.GUID
	}
	if entry.Link != "" {
		return entry.Link
	}
	return entry.Title
}

// leadImage finds the item's representative image: first image in the
// sanitized body, else an image enclosure, else the entry's own image.
func leadImage(entry *gofeed.Item, clean string) string {
	if lead := firstImgSrc(clean); lead != "" {
		return lead
	}
	for _, enc := range entry.Enclosures {
		if enc != nil && strings.HasPrefix(enc.Type, "image/") {
			return enc.URL
		}
	}
	if entry.Image != nil {
		return entry.Image.URL
	}
	return ""
}

func entryAuthor(entry *gofeed.Item) string {
	if len(entry.Authors) > 0 && entry.Authors[0] != nil {
		return entry.Authors[0].Name
	}
	return ""
}
