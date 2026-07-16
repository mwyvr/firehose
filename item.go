package firehose

import (
	"context"
	"time"

	"github.com/mwyvr/kid"
)

// Item is a single feed entry, already sanitized (and, where the feed declared
// a language, highlighted). BodyHTML is safe to render directly.
type Item struct {
	ID     kid.ID // first-class value type; id.Time() etc.
	FeedID int64
	GUID   string // feed-provided; dedupe key with FeedID

	Title  string
	URL    string // source link; MAY be empty (see linkless-title rule)
	Author string

	Published time.Time // sort key for the river (TZ-correct, ParseInLocation)

	// BodyHTML is the sanitized item body. When FullContent is true this is the
	// whole item (content:encoded); otherwise it is a teaser (description).
	BodyHTML string

	// SummaryHTML is the sanitized feed-provided summary when it is distinct
	// from the body (feed shipped both content:encoded and description).
	// Excerpt source-of-truth prefers this over truncating BodyHTML.
	SummaryHTML string

	// LeadImage is the first image URL for the item — the first inline <img>
	// in the sanitized body, else the first image enclosure/media thumbnail.
	// Used when excerpt_image = "lead" restores an image to a text excerpt.
	LeadImage string

	// FullContent gates excerpt+expand: the expand affordance is only offered
	// when the feed actually shipped a full body. Inferred at fetch time.
	FullContent bool

	// WordCount of the sanitized text, for the reading-time hint.
	WordCount int

	FetchedAt time.Time

	// Categories are inherited from the item's feed at render time (not stored
	// on the row). Populated when rendering the ALL river's section label.
	Categories []string
}

// ReadingTime returns an estimated read duration from WordCount (~220 wpm),
// rounded up to whole minutes with a floor of one.
func (it *Item) ReadingTime() time.Duration {
	const wpm = 220
	mins := max((it.WordCount+wpm-1)/wpm, 1)
	return time.Duration(mins) * time.Minute
}

// HasLink reports whether the item has a usable source URL. When false, the
// template renders the title as plain text rather than a dead anchor.
func (it *Item) HasLink() bool { return it.URL != "" }

// ItemStat is the health page's view of one cached item.
type ItemStat struct {
	FeedID    int64
	Published time.Time
}

// ItemFilter narrows which items render into an output; a section is a
// filter, and the ALL river is a filter with empty Categories.
type ItemFilter struct {
	// Categories selects items in any of these categories. Empty selects all
	// (the ALL river).
	Categories []string

	// Since bounds the display window (e.g. now - 14d). Zero means unbounded.
	Since time.Time

	Limit  int
	Offset int
}

// ItemService is the cache's item store. Implemented by the sqlite package and
// mock for tests.
type ItemService interface {
	// FindItems returns items matching filter, newest-first (Published desc,
	// then ID as a stable tiebreaker for deterministic output), and the total
	// count.
	FindItems(ctx context.Context, filter ItemFilter) ([]*Item, int, error)

	// UpsertItems inserts new items and updates changed ones, keyed by
	// (FeedID, GUID). Existing IDs are preserved so kid.ID stays stable.
	UpsertItems(ctx context.Context, items []*Item) error

	// ItemStats returns (FeedID, Published) for every cached item — enough
	// for the health page to compute per-feed shown/cached counts without
	// loading bodies.
	ItemStats(ctx context.Context) ([]ItemStat, error)

	// PurgeExpired deletes items older than the cache retention cutoff. Returns
	// the number removed. (Distinct from the display window: retention keeps
	// GUID history longer to prevent re-published old items reappearing.)
	PurgeExpired(ctx context.Context, olderThan time.Time) (int, error)
}
