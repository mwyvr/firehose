package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/firehose/feed"
)

func runTest(ctx context.Context, configPath string, args []string) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	ua := fs.String("ua", "", "override User-Agent for this probe")
	var hdrs headerFlags
	fs.Var(&hdrs, "H", `extra header, "Key: Value" (repeatable)`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: firehose test [-ua STRING] [-H \"Key: Value\"] URL")
	}
	feedURL := fs.Arg(0)

	fetchCfg := firehose.DefaultFetchConfig()
	if cfg, err := firehose.LoadConfig(configPath); err == nil {
		fetchCfg = cfg.Fetch
	} else {
		fmt.Fprintf(os.Stderr, "note: config not loaded (%v); using built-in fetch defaults\n", err)
	}

	preq := feed.ProbeRequest{URL: feedURL, UserAgent: *ua, Headers: hdrs.m}
	effectiveUA := fetchCfg.UserAgent
	if *ua != "" {
		effectiveUA = *ua
	}
	fmt.Printf("GET %s\n", feedURL)
	fmt.Printf("    user-agent: %s\n", effectiveUA)
	for k, v := range hdrs.m {
		fmt.Printf("    %s: %s\n", k, v)
	}

	p, perr := feed.RunProbe(ctx, fetchCfg, preq)

	for _, hop := range p.Hops {
		fmt.Printf("    -> %d %s\n", hop.Status, hop.To)
	}
	if p.Status != 0 {
		fmt.Printf("status: %d\n", p.Status)
		printIf("proto", p.Proto)
		printIf("content-type", p.ContentType)
		printIf("server", p.Server)
		printIf("etag", p.ETag)
		printIf("last-modified", p.LastModified)
		fmt.Printf("body: %d bytes\n", p.BodyBytes)
	}

	if perr != nil {
		if p.BodySnippet != "" {
			fmt.Printf("\nbody snippet:\n%s\n", p.BodySnippet)
		}
		fmt.Printf("\nFAIL (%s)\n", firehose.ErrorCode(perr))
		if p.Status == 403 {
			fmt.Println("hint: 403 with a browser working often means CDN bot-filtering;")
			fmt.Println("      the snippet above usually names it. UA and Accept headers matter.")
		}
		return perr
	}

	fmt.Printf("\nparsed: %s %s — %q — %d items\n", p.FeedType, p.FeedVersion, p.FeedTitle, p.ItemCount)
	if p.First != nil {
		it := p.First
		fmt.Println("first item:")
		fmt.Printf("    title:     %s\n", it.Title)
		fmt.Printf("    link:      %s\n", it.Link)
		fmt.Printf("    guid:      %s\n", it.GUID)
		fmt.Printf("    published: %s\n", it.Published)
		if it.FullContent {
			fmt.Println("    content:   full (content:encoded present) — excerpt+expand eligible")
		} else {
			fmt.Println("    content:   teaser (description only)")
		}
		fmt.Printf("    sanitized: %d words", it.Words)
		if it.LeadImage != "" {
			fmt.Printf("; lead image: %s", it.LeadImage)
		}
		fmt.Println()
	}
	fmt.Println("\nok")
	return nil
}

// headerFlags collects repeatable -H "Key: Value" flags.
type headerFlags struct{ m map[string]string }

func (h *headerFlags) String() string { return fmt.Sprint(h.m) }

func (h *headerFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, ":")
	if !ok {
		return fmt.Errorf("header %q: want \"Key: Value\"", v)
	}
	if h.m == nil {
		h.m = map[string]string{}
	}
	h.m[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}

func printIf(k, v string) {
	if v != "" {
		fmt.Printf("%s: %s\n", k, v)
	}
}

// OPML structures (stdlib encoding/xml; export only — import is later).
