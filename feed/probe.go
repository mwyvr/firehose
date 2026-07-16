package feed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mwyvr/firehose"
)

// Probe is the result of a diagnostic single-feed fetch (`firehose test`).
// It ignores the cache entirely: no conditional headers, nothing written.
type Probe struct {
	RequestURL     string
	FinalURL       string
	Hops           []ProbeHop // redirect chain, in order
	ChainPermanent bool       // every hop 301/308: a real fetch would persist FinalURL
	Timezone       string     // zone assumed for zoneless rescued dates

	Status       int
	Proto        string // negotiated protocol (expect HTTP/1.1 by design)
	ContentType  string
	ETag         string
	LastModified string
	Server       string
	BodyBytes    int
	BodySnippet  string // first bytes of an unparseable body (block pages!)

	FeedType    string
	FeedVersion string
	FeedTitle   string
	ItemCount   int
	// Date visibility: how every item's published time resolved. Unparsed
	// items fall to the fetch-time fallback and may never age out of the
	// display window — the probe's job is to say so at onboarding.
	DatesFeed     int
	DatesRescued  int
	DatesUnparsed int
	First         *ProbeItem
}

// ProbeHop is one redirect in the chain.
type ProbeHop struct {
	Status int
	To     string
}

// ProbeItem is the analysis of the first item, run through the same
// sanitize/inference pipeline the fetcher uses.
type ProbeItem struct {
	Title         string
	Link          string
	GUID          string
	Published     time.Time
	PublishedTier string // DateFromFeed | DateRescued | DateNone
	PublishedRaw  string // verbatim raw date string (diagnostics)
	FullContent   bool   // content:encoded present
	Words         int
	LeadImage     string
}

// snippetLen bounds the body preview shown for unparseable responses.
const snippetLen = 300

// ProbeRequest describes the diagnostic request: URL plus the same optional
// overrides a per-feed config block can apply, so `firehose test` can bisect
// exactly what a hostile CDN wants.
type ProbeRequest struct {
	URL       string
	UserAgent string            // overrides fetch.UserAgent when set
	Headers   map[string]string // set verbatim, last
	Timezone  string            // zone for zoneless rescued dates; UTC when unset
}

// RunProbe fetches and analyses a single feed URL with the configured fetch
// politeness (User-Agent, Accept, Accept-Language, timeout) but no cache
// interaction. A non-nil Probe is always returned, carrying whatever was
// learned before the failure; err classifies the failure.
func RunProbe(ctx context.Context, fetch firehose.FetchConfig, preq ProbeRequest) (*Probe, error) {
	feedURL := preq.URL
	p := &Probe{RequestURL: feedURL, FinalURL: feedURL}
	if preq.Timezone != "" {
		if _, err := time.LoadLocation(preq.Timezone); err != nil {
			return p, firehose.Errorf(firehose.EINVALID, "bad timezone %q: %v", preq.Timezone, err)
		}
	}
	p.Timezone = feedLocation(preq.Timezone).String()

	if firehose.IsLocalFeed(feedURL) {
		return probeLocal(p, feedURL, preq.Timezone)
	}

	fd := &firehose.Feed{URL: feedURL, UserAgent: preq.UserAgent, Headers: preq.Headers}
	req, err := buildFeedRequest(ctx, fetch, fd)
	if err != nil {
		return p, firehose.Errorf(firehose.EINVALID, "bad url: %v", err)
	}

	resp, trace, err := doFollow(newHTTPClient(fetch), req)
	if err != nil {
		return p, firehose.Errorf(classifyNetErr(err), "fetch: %v", err)
	}
	for _, h := range trace.hops {
		p.Hops = append(p.Hops, ProbeHop(h))
	}
	if trace.finalURL != "" {
		p.FinalURL = trace.finalURL
	}
	p.ChainPermanent = trace.allPermanent && len(trace.hops) > 0
	defer func() { _ = resp.Body.Close() }()

	p.Status = resp.StatusCode
	p.Proto = resp.Proto
	p.ContentType = resp.Header.Get("Content-Type")
	p.ETag = resp.Header.Get("ETag")
	p.LastModified = resp.Header.Get("Last-Modified")
	p.Server = resp.Header.Get("Server")

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return p, firehose.Errorf(firehose.EINTERNAL, "reading body: %v", err)
	}
	p.BodyBytes = len(body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		p.BodySnippet = snippet(body)
		code := firehose.EINTERNAL
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			code = firehose.ENOTFOUND
		}
		return p, firehose.Errorf(code, "HTTP %d", resp.StatusCode)
	}

	return analyzeProbeBody(p, body, feedURL, preq.Timezone)
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > snippetLen {
		s = s[:snippetLen] + "\u2026"
	}
	return s
}

// analyzeProbeBody is the transport-independent half of a probe: parse the
// document and report type, item count, and the first-item analysis. Shared
// by the network and file:// paths.
func analyzeProbeBody(p *Probe, body []byte, feedURL, tz string) (*Probe, error) {
	loc := feedLocation(tz)
	parsed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		p.BodySnippet = snippet(body)
		return p, firehose.Errorf(firehose.EPARSE, "parse: %v", err)
	}

	p.FeedType = parsed.FeedType
	p.FeedVersion = parsed.FeedVersion
	p.FeedTitle = parsed.Title
	p.ItemCount = len(parsed.Items)

	for _, it := range parsed.Items {
		if it == nil {
			continue
		}
		switch _, tier := resolvePublished(it, loc); tier {
		case DateFromFeed:
			p.DatesFeed++
		case DateRescued:
			p.DatesRescued++
		default:
			p.DatesUnparsed++
		}
	}

	if len(parsed.Items) > 0 && parsed.Items[0] != nil {
		it := parsed.Items[0]
		raw := it.Content
		full := raw != ""
		if raw == "" {
			raw = it.Description
		}
		base := it.Link
		if base == "" {
			base = feedURL
		}
		clean, words := sanitize(raw, base, nil)
		lead := firstImgSrc(clean)
		published, tier := resolvePublished(it, loc)
		p.First = &ProbeItem{
			Title:         it.Title,
			Link:          it.Link,
			GUID:          it.GUID,
			Published:     published,
			PublishedTier: tier,
			PublishedRaw:  rawDate(it),
			FullContent:   full,
			Words:         words,
			LeadImage:     lead,
		}
	}
	return p, nil
}

// probeLocal is `firehose test` for a file:// feed
func probeLocal(p *Probe, feedURL, tz string) (*Probe, error) {
	path := firehose.LocalFeedPath(feedURL)
	fi, err := os.Stat(path)
	if err != nil {
		code := firehose.EINTERNAL
		if errors.Is(err, os.ErrNotExist) {
			code = firehose.ENOTFOUND
		}
		return p, firehose.Errorf(code, "stat: %v", err)
	}
	p.LastModified = fi.ModTime().UTC().Format(http.TimeFormat)

	fh, err := os.Open(path)
	if err != nil {
		return p, firehose.Errorf(firehose.EINTERNAL, "open: %v", err)
	}
	defer func() { _ = fh.Close() }()
	body, err := io.ReadAll(io.LimitReader(fh, maxBodyBytes))
	if err != nil {
		return p, firehose.Errorf(firehose.EINTERNAL, "read: %v", err)
	}
	p.BodyBytes = len(body)
	return analyzeProbeBody(p, body, feedURL, tz)
}
