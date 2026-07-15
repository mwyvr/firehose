// Package feed fetches and converts syndication feeds.
//
// The fetch side is a bounded worker pool (Run) over per-feed fetches
// (fetchOne); results flow to a single collector goroutine that performs ALL
// database writes — the channel is the queue, and services need no mutexes.
// Failure is per-feed and never escapes: panics become EPANIC, errors become
// classified FeedUpdates feeding exponential backoff (1h doubling to a 24h
// cap, reset by any successful contact including a 304).
//
// Politeness is deliberate and layered: conditional GET, per-host
// serialization, honest User-Agent (per-feed overrides are the operator's
// explicit call), Accept and Accept-Language headers, a 10 MiB body cap,
// and HTTP/1.1 only — CDN bot-mitigation fingerprints the h2 connection
// itself, and a batch fetcher gains nothing from h2 (see docs/design.md).
//
// The content pipeline order is LOAD-BEARING:
//
//	select body -> Sanitize (strip, absolutize, structural policy)
//	            -> NormalizeTypography -> Highlight -> keyword filters
//
// Sanitize establishes the invariants everything downstream assumes;
// typography and highlighting never touch code; filters match what a reader
// would see. Filters, strip selectors, and fetch overrides live only in
// config and are overlaid onto DB-sourced feeds at the start of every Run.
package feed

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/mwyvr/firehose"
)

// maxBodyBytes caps a feed response body. Politeness and self-protection: a
// hostile or broken endpoint must not balloon memory.
const maxBodyBytes = 10 << 20 // 10 MiB

// maxBackoff caps the exponential per-feed backoff.
const maxBackoff = 24 * time.Hour

// acceptHeader is sent on every fetch. Go's http client sends no Accept by
// default, and CDN bot-filtering (Akamai and friends) commonly rejects
// header-anomalous requests that browsers would never produce. Declaring the
// feed types we actually want is correct behavior regardless.
const acceptHeader = "application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5"

// Fetcher runs one fetch pass over all due feeds.
type Fetcher struct {
	cfg   *firehose.Config
	feeds firehose.FeedService
	items firehose.ItemService

	client *http.Client

	// Now is injectable for tests (WTF-style testable time).
	Now func() time.Time

	// Force ignores per-feed backoff gates: every feed is attempted now.
	Force bool

	hostLocks sync.Map // host -> *sync.Mutex, when PerHostSerial
}

// NewFetcher constructs a Fetcher over the given services.
func NewFetcher(cfg *firehose.Config, feeds firehose.FeedService, items firehose.ItemService) *Fetcher {
	return &Fetcher{
		cfg:    cfg,
		feeds:  feeds,
		items:  items,
		client: newHTTPClient(cfg.Fetch),
		Now:    time.Now,
	}
}

// newHTTPClient builds the fetch client. HTTP/2 is deliberately disabled:
// CDN bot-mitigation (Akamai notably) fingerprints the h2 connection itself
// — SETTINGS frames, frame ordering — and resets streams from non-browser
// clients (observed as "stream error ... INTERNAL_ERROR; received from
// peer", e.g. cbc.ca). Headers cannot fix a protocol-layer fingerprint. A
// batch fetcher making one serialized request per host gains nothing from
// h2 multiplexing, so HTTP/1.1 removes the whole surface at zero cost.
func newHTTPClient(fetch firehose.FetchConfig) *http.Client {
	tr := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
		// A non-nil empty TLSNextProto map disables HTTP/2 entirely.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	return &http.Client{
		Timeout:   fetch.Timeout.D(),
		Transport: tr,
	}
}

// result carries one feed's outcome from a worker to the collector. Failures
// travel the same path as successes: a panicked feed is just a result with an
// EPANIC status update and no items.
type result struct {
	feed  *firehose.Feed
	items []*firehose.Item
	upd   firehose.FeedUpdate
}

// Run fetches all due feeds (those not gated by backoff), fanning out to
// cfg.Fetch.Concurrency workers and collecting all writes on this goroutine —
// the single-writer invariant. It returns the first cache-write error
// encountered; per-feed fetch/parse failures are not errors here, they are
// recorded as feed health status.
func (f *Fetcher) Run(ctx context.Context) error {
	// Force fetches every feed now, ignoring backoff gates — the debugging
	// escape hatch. Conditional requests still apply: unchanged feeds 304
	// cheaply, so force is polite too.
	due, _, err := f.feeds.FindFeeds(ctx, firehose.FeedFilter{DueOnly: !f.Force})
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}

	// Overlay config-only fields onto the stored rows. The cache persists
	// url/title/categories and fetch state; filters, strip selectors, and
	// fetch overrides live only in config and MUST be merged here — the
	// renderer does the same join (feedMeta). Without this overlay those
	// features are silently inert against real (DB-sourced) feeds.
	confByURL := f.cfg.FeedConfByURL()
	for _, fd := range due {
		if fc, ok := confByURL[fd.URL]; ok {
			fd.Exclude = fc.Exclude
			fd.Include = fc.Include
			fd.StripSelectors = fc.StripSelectors
			fd.UserAgent = fc.UserAgent
			fd.Headers = fc.Headers
			fd.RewriteHost = fc.RewriteHost
		}
	}

	workers := max(f.cfg.Fetch.Concurrency, 1)

	jobs := make(chan *firehose.Feed)
	results := make(chan result)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for fd := range jobs {
				results <- f.fetchOne(ctx, fd)
			}
		})
	}
	go func() {
		defer close(jobs)
		for _, fd := range due {
			select {
			case jobs <- fd:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	// Single-writer collector: ALL cache writes happen here.
	var firstErr error
	for res := range results {
		if len(res.items) > 0 {
			if err := f.items.UpsertItems(ctx, res.items); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if _, err := f.feeds.UpdateFeed(ctx, res.feed.ID, res.upd); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
