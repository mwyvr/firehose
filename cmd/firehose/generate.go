package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/mwyvr/firehose/feed"
	"github.com/mwyvr/firehose/render"
	"github.com/mwyvr/firehose/sqlite"
)

func runGenerate(ctx context.Context, configPath string, force bool) (err error) {
	var outputDir string
	defer func() {
		if p := recover(); p != nil {
			msg := fmt.Sprintf("panic: %v", p)
			if outputDir != "" {
				if werr := render.WriteFallbackHealth(outputDir, msg, string(debug.Stack())); werr != nil {
					fmt.Fprintf(os.Stderr, "firehose: fallback health write failed: %v\n", werr)
				}
			}
			err = errors.New(msg)
		}
	}()

	cfg, err := loadAndReport(configPath)
	if err != nil {
		return err
	}
	outputDir = cfg.Settings.OutputDir

	db := sqlite.NewDB(cfg.Settings.CacheDB, cfg.Location)
	if err := db.Open(ctx); err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	feeds := sqlite.NewFeedService(db)
	items := sqlite.NewItemService(db)

	if err := feeds.SyncFeeds(ctx, cfg.ToFeeds()); err != nil {
		return err
	}
	fetcher := feed.NewFetcher(cfg, feeds, items)
	fetcher.Force = force
	if err := fetcher.Run(ctx); err != nil {
		return err
	}

	r, err := render.New(cfg, items, feeds)
	if err != nil {
		return err
	}
	if err := r.RenderAll(ctx); err != nil {
		return err
	}

	cutoff := time.Now().Add(-cfg.Settings.CacheRetention.D())
	if _, err := items.PurgeExpired(ctx, cutoff); err != nil {
		return err
	}
	return nil
}

// runCheck is the preflight validator: config + template dry-run against a
// synthetic fixture. Touches neither network nor cache. ExecStartPre= fodder.
