package firehose

import (
	"context"
	"time"
)

// Feed is a subscribed source. Fetch-state fields (ETag, LastModified,
// FailCount, backoff) live here because the cache tracks per-feed health and
// conditional-GET validators.
//
// ID is a private cache-internal join key and must never travel outside the
// database or a single run: identity that travels is the URL. (This
// invariant is what makes rowid reuse safe; see the schema.)
type Feed struct {
	ID  int64  // cache primary key
	URL string // canonical URL; updated in place when a 301 is followed

	// Title is the feed's self-reported title. A config Title override wins
	// when the feed's own title is garbage ("Untitled Feed", a bare domain).
	Title string

	// Categories tag the feed into sections. A feed may belong to several; an
	// item inherits its feed's categories. Empty means it appears only in the
	// ALL river.
	Categories []string

	// Config-derived per-feed overrides. Nil/zero means "inherit from output
	// then settings" per the three-tier resolution.
	Body           string            // "", title, excerpt, full, excerpt+expand
	ExcerptImage   string            // "", lead, none
	Exclude        []string          // simple keyword filters (config, not hook)
	RewriteHost    map[string]string // wrong host -> right host (config-only, overlaid per run)
	IncludeURL     []string          // URL-scoped keep filter (config-only, overlaid per run)
	ExcludeURL     []string          // URL-scoped drop filter (config-only, overlaid per run)
	Include        []string          // simple keyword filters (config, not hook)
	StripSelectors []string          // per-feed cruft removal, applied post-parse

	// Per-feed fetch overrides for CDN-hostile endpoints. UserAgent replaces
	// the global one; Headers are set verbatim on the request. Identifying
	// honestly is the default; impersonation is the operator's per-feed call.
	UserAgent string
	Headers   map[string]string

	// Conditional-GET validators, persisted between runs.
	ETag         string
	LastModified string

	// Health / backoff state. LastStatus is an application error code
	// (ENOTFOUND, EPARSE, ETIMEOUT, ...) reported in firehose.html.
	FailCount    int
	LastStatus   string
	LastSuccess  time.Time // last fetch that produced items
	LastFetched  time.Time
	NextEarliest time.Time // backoff; do not fetch before this
}

// FeedFilter narrows FindFeeds. Zero-value fields are ignored.
type FeedFilter struct {
	ID       *int64
	URL      *string
	Category *string

	// DueOnly, when true, returns only feeds whose NextEarliest has passed
	// (i.e. not currently in backoff).
	DueOnly bool

	Offset int
	Limit  int
}

// FeedUpdate carries mutable fetch/health state written back after a fetch.
// Pointer fields left nil are unchanged.
type FeedUpdate struct {
	URL          *string // set when a 301 redirect is persisted
	Title        *string
	ETag         *string
	LastModified *string
	FailCount    *int
	LastStatus   *string
	LastSuccess  *time.Time
	LastFetched  *time.Time
	NextEarliest *time.Time
}

// FeedService is the cache's feed store. Implemented by the sqlite and mock packages
type FeedService interface {
	// FindFeeds returns feeds matching filter, and the total count.
	FindFeeds(ctx context.Context, filter FeedFilter) ([]*Feed, int, error)

	// FindFeedByURL returns the feed with the given URL, or an ENOTFOUND error.
	FindFeedByURL(ctx context.Context, url string) (*Feed, error)

	// SyncFeeds reconciles the configured feed set into the cache: inserts new
	// feeds, updates categories/config on existing ones, and removes feeds no
	// longer in config (cascading their items).
	SyncFeeds(ctx context.Context, feeds []*Feed) error

	// UpdateFeed applies fetch/health state to a feed.
	UpdateFeed(ctx context.Context, id int64, upd FeedUpdate) (*Feed, error)
}
