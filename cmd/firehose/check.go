package main

import (
	"fmt"

	"github.com/mwyvr/firehose/feed"
	"github.com/mwyvr/firehose/render"
)

func runCheck(configPath string) error {
	cfg, err := loadAndReport(configPath)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if err := feed.CheckConfig(cfg); err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	r, err := render.New(cfg, nil, nil)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if err := r.Check(); err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	fmt.Printf("config OK: %d feeds, %d outputs (+health), tz=%s; templates OK\n",
		len(cfg.Feeds), len(cfg.Outputs), cfg.Location)
	return nil
}

// runTest fetches and diagnoses a single feed URL, verbosely: redirect chain,
// status, headers, parse result, first-item analysis, and — when parsing
// fails — a body snippet, because a CDN block page is HTML and seeing it ends
// the mystery. No cache interaction. Config is optional: fetch politeness
// settings are used when the config loads, built-in defaults otherwise.
