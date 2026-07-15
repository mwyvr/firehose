package feed

import (
	"context"
	"errors"
	"fmt"
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
	RequestURL string
	FinalURL   string
	Hops       []ProbeHop // redirect chain, in order

	Status       int
	Proto        string // negotiated protocol (expect HTTP/1.1 by design)
	ContentType  string
	ETag         string
	LastModified string
	Server       string
	BodyBytes    int
	BodySnippet  string // first bytes of an unparseable body (block pages!)
	ErrCode      string // classification when failing ("" on success)

	FeedType    string
	FeedVersion string
	FeedTitle   string
	ItemCount   int
	First       *ProbeItem
}

// ProbeHop is one redirect in the chain.
type ProbeHop struct {
	Status int
	To     string
}

// ProbeItem is the analysis of the first item, run through the same
// sanitize/inference pipeline the fetcher uses.
type ProbeItem struct {
	Title       string
	Link        string
	GUID        string
	Published   time.Time
	FullContent bool // content:encoded present
	HasSummary  bool
	Words       int
	LeadImage   string
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
}

// RunProbe fetches and analyses a single feed URL with the configured fetch
// politeness (User-Agent, Accept, Accept-Language, timeout) but no cache
// interaction. A non-nil Probe is always returned, carrying whatever was
// learned before the failure; err classifies the failure.
func RunProbe(ctx context.Context, fetch firehose.FetchConfig, preq ProbeRequest) (*Probe, error) {
	feedURL := preq.URL
	p := &Probe{RequestURL: feedURL, FinalURL: feedURL}

	if firehose.IsLocalFeed(feedURL) {
		return probeLocal(p, feedURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		p.ErrCode = firehose.EINVALID
		return p, firehose.Errorf(firehose.EINVALID, "bad url: %v", err)
	}
	ua := fetch.UserAgent
	if preq.UserAgent != "" {
		ua = preq.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", acceptHeader)
	if fetch.AcceptLanguage != "" {
		req.Header.Set("Accept-Language", fetch.AcceptLanguage)
	}
	for k, v := range preq.Headers {
		req.Header.Set(k, v)
	}

	client := newHTTPClient(fetch)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		p.Hops = append(p.Hops, ProbeHop{Status: req.Response.StatusCode, To: req.URL.String()})
		p.FinalURL = req.URL.String()
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		p.ErrCode = classifyNetErr(err)
		return p, firehose.Errorf(p.ErrCode, "fetch: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	p.Status = resp.StatusCode
	p.Proto = resp.Proto
	p.ContentType = resp.Header.Get("Content-Type")
	p.ETag = resp.Header.Get("ETag")
	p.LastModified = resp.Header.Get("Last-Modified")
	p.Server = resp.Header.Get("Server")

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		p.ErrCode = classifyNetErr(err)
		return p, firehose.Errorf(p.ErrCode, "reading body: %v", err)
	}
	p.BodyBytes = len(body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		p.BodySnippet = snippet(body)
		switch resp.StatusCode {
		case http.StatusNotFound, http.StatusGone:
			p.ErrCode = firehose.ENOTFOUND
		default:
			p.ErrCode = firehose.EINTERNAL
		}
		return p, firehose.Errorf(p.ErrCode, "HTTP %d", resp.StatusCode)
	}

	return analyzeProbeBody(p, body, feedURL)
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
func analyzeProbeBody(p *Probe, body []byte, feedURL string) (*Probe, error) {
	parsed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		p.ErrCode = firehose.EPARSE
		p.BodySnippet = snippet(body)
		return p, firehose.Errorf(firehose.EPARSE, "parse: %v", err)
	}

	p.FeedType = parsed.FeedType
	p.FeedVersion = parsed.FeedVersion
	p.FeedTitle = parsed.Title
	p.ItemCount = len(parsed.Items)

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
		clean, words := Sanitize(raw, base, nil)
		lead := firstImgSrc(clean)
		published := time.Time{}
		if it.PublishedParsed != nil {
			published = *it.PublishedParsed
		} else if it.UpdatedParsed != nil {
			published = *it.UpdatedParsed
		}
		p.First = &ProbeItem{
			Title:       it.Title,
			Link:        it.Link,
			GUID:        it.GUID,
			Published:   published,
			FullContent: full,
			HasSummary:  full && it.Description != "",
			Words:       words,
			LeadImage:   lead,
		}
	}
	return p, nil
}

// probeLocal is `firehose test` for a file:// feed
func probeLocal(p *Probe, feedURL string) (*Probe, error) {
	path := firehose.LocalFeedPath(feedURL)
	fi, err := os.Stat(path)
	if err != nil {
		p.ErrCode = firehose.EINTERNAL
		if errors.Is(err, os.ErrNotExist) {
			p.ErrCode = firehose.ENOTFOUND
		}
		return p, firehose.Errorf(p.ErrCode, "stat: %v", err)
	}
	p.LastModified = fi.ModTime().UTC().Format(http.TimeFormat)

	fh, err := os.Open(path)
	if err != nil {
		p.ErrCode = firehose.EINTERNAL
		return p, firehose.Errorf(p.ErrCode, "open: %v", err)
	}
	defer func() { _ = fh.Close() }()
	body, err := io.ReadAll(io.LimitReader(fh, maxBodyBytes))
	if err != nil {
		p.ErrCode = firehose.EINTERNAL
		return p, firehose.Errorf(p.ErrCode, "read: %v", err)
	}
	p.BodyBytes = len(body)
	return analyzeProbeBody(p, body, feedURL)
}
