package feed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mwyvr/firehose"
)

// fetchOne fetches a single feed and classifies the outcome. It reads in the
// order the design describes the fetch: isolate (panic -> EPANIC), serialize
// per host, build a polite request, follow redirects while tracking
// permanence, dispatch on status, parse, convert, persist validators.
// Every early return carries a classified FeedUpdate; per-feed failure never
// escapes to the run.
func (f *Fetcher) fetchOne(ctx context.Context, fd *firehose.Feed) (res result) {
	res.feed = fd
	defer func() {
		if p := recover(); p != nil {
			res.items = nil
			res.upd = f.failure(fd, firehose.EPANIC)
			_ = p // code-only status; message intentionally not persisted
		}
	}()

	if firehose.IsLocalFeed(fd.URL) {
		return f.fetchLocal(fd)
	}

	defer f.lockHost(fd.URL)()

	req, err := f.buildRequest(ctx, fd)
	if err != nil {
		res.upd = f.failure(fd, firehose.EINVALID)
		return res
	}

	resp, redirect, err := f.doFollow(req)
	if err != nil {
		res.upd = f.failure(fd, classifyNetErr(err))
		return res
	}
	defer drainClose(resp)

	now := f.Now()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		// Unchanged: a successful contact. Reset failure state, stamp the
		// fetch; LastSuccess (last fetch that produced items) is untouched.
		res.upd = success(now, false)
		persistRedirect(&res.upd, fd, redirect.allPermanent, redirect.finalURL)
		return res

	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		res.upd = f.failure(fd, firehose.ENOTFOUND)
		return res

	case resp.StatusCode < 200 || resp.StatusCode > 299:
		res.upd = f.failure(fd, firehose.EINTERNAL)
		return res
	}

	parsed, err := gofeed.NewParser().Parse(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		res.upd = f.failure(fd, firehose.EPARSE)
		return res
	}

	strip, err := CompileStrip(fd.StripSelectors)
	if err != nil {
		res.upd = f.failure(fd, firehose.EINVALID)
		return res
	}

	res.items = f.convert(fd, parsed, now, strip)
	res.upd = success(now, len(res.items) > 0)
	persistRedirect(&res.upd, fd, redirect.allPermanent, redirect.finalURL)
	persistValidators(&res.upd, resp)
	persistSelfTitle(&res.upd, fd, parsed)
	return res
}

// lockHost serializes fetches per host when configured (politeness: never
// two concurrent requests to one origin). It returns the unlock func; a
// no-op when serialization is off or the URL is unparseable.
func (f *Fetcher) lockHost(rawURL string) func() {
	if !f.cfg.Fetch.PerHostSerial {
		return func() {}
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return func() {}
	}
	mu, _ := f.hostLocks.LoadOrStore(u.Host, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}

// buildRequest assembles the polite conditional GET: global or per-feed
// User-Agent, Accept (Go sends none by default — a CDN bot tell),
// Accept-Language, per-feed header overrides, then the cache validators.
func (f *Fetcher) buildRequest(ctx context.Context, fd *firehose.Feed) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fd.URL, nil)
	if err != nil {
		return nil, err
	}
	ua := f.cfg.Fetch.UserAgent
	if fd.UserAgent != "" {
		ua = fd.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", acceptHeader)
	if al := f.cfg.Fetch.AcceptLanguage; al != "" {
		req.Header.Set("Accept-Language", al)
	}
	for k, v := range fd.Headers {
		req.Header.Set(k, v)
	}
	if fd.ETag != "" {
		req.Header.Set("If-None-Match", fd.ETag)
	}
	if fd.LastModified != "" {
		req.Header.Set("If-Modified-Since", fd.LastModified)
	}
	return req, nil
}

// redirectTrace records what following redirects learned: only a chain that
// was permanent END TO END (301/308 every hop) justifies persisting the new
// URL; any temporary hop breaks it.
type redirectTrace struct {
	allPermanent bool
	finalURL     string
}

// doFollow performs the request with a per-fetch client copy whose redirect
// hook fills the trace.
func (f *Fetcher) doFollow(req *http.Request) (*http.Response, redirectTrace, error) {
	trace := redirectTrace{allPermanent: true}
	client := *f.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		switch req.Response.StatusCode {
		case http.StatusMovedPermanently, http.StatusPermanentRedirect:
			// still permanent
		default:
			trace.allPermanent = false
		}
		trace.finalURL = req.URL.String()
		return nil
	}
	resp, err := client.Do(req)
	return resp, trace, err
}

// drainClose drains a little of the body before closing so the connection
// can be reused, then closes it.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

// persistValidators stores the response's conditional-GET validators
// (including empty values: a feed that stops sending an ETag clears it).
func persistValidators(upd *firehose.FeedUpdate, resp *http.Response) {
	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")
	upd.ETag = &etag
	upd.LastModified = &lastMod
}

// persistSelfTitle stores the feed's self-reported title only when nothing
// is stored yet; a stored title (config override, or an earlier self-title)
// stands.
func persistSelfTitle(upd *firehose.FeedUpdate, fd *firehose.Feed, parsed *gofeed.Feed) {
	if fd.Title == "" && parsed.Title != "" {
		title := parsed.Title
		upd.Title = &title
	}
}

// success builds the feed update for a successful contact: failure state
// reset, fetch stamped, backoff gate cleared. producedItems means the fetch
// yielded STORABLE items — a parse whose entries all fell to the retention
// gate or filters does not stamp LastSuccess, so the health page's
// quiet-feed detection sees through "parses fine, yields nothing". A 304
// never stamps it.
func success(now time.Time, producedItems bool) firehose.FeedUpdate {
	zero := 0
	empty := ""
	var noBackoff time.Time // zero => stored NULL => always due
	upd := firehose.FeedUpdate{
		FailCount:    &zero,
		LastStatus:   &empty,
		LastFetched:  &now,
		NextEarliest: &noBackoff,
	}
	if producedItems {
		upd.LastSuccess = &now
	}
	return upd
}

// failure builds the feed update for a failed fetch/parse: increment the
// consecutive-failure count and gate the next attempt with exponential
// backoff (1h, 2h, 4h, ... capped at maxBackoff).
func (f *Fetcher) failure(fd *firehose.Feed, code string) firehose.FeedUpdate {
	now := f.Now()
	fails := fd.FailCount + 1
	next := now.Add(backoff(fails))
	return firehose.FeedUpdate{
		FailCount:    &fails,
		LastStatus:   &code,
		LastFetched:  &now,
		NextEarliest: &next,
	}
}

// persistRedirect records the final URL when the whole redirect chain was
// permanent — a 301 means "stop asking here", so we stop asking there.
func persistRedirect(upd *firehose.FeedUpdate, fd *firehose.Feed, allPermanent bool, finalURL string) {
	if allPermanent && finalURL != "" && finalURL != fd.URL {
		upd.URL = &finalURL
	}
}

// backoff computes the exponential delay for the nth consecutive failure.
func backoff(fails int) time.Duration {
	if fails < 1 {
		fails = 1
	}
	shift := min(fails-1, 5)
	d := min(time.Hour<<uint(shift), maxBackoff)
	return d
}

// classifyNetErr maps transport errors to status codes: timeouts are
// ETIMEOUT, everything else EINTERNAL.
func classifyNetErr(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return firehose.ETIMEOUT
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return firehose.ETIMEOUT
	}
	return firehose.EINTERNAL
}

// skipByFilters applies the per-feed keyword filters: exclude drops an item
// whose title or text matches any keyword; a non-empty include keeps only
// matching items. Matching is case-insensitive substring — the config answer
// to rawdog kill-file plugins; anything fancier is exec-hook territory.
