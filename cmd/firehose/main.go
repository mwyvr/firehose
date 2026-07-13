// Command firehose fetches subscribed feeds and writes static river-of-news
// HTML. It runs from a systemd timer (or cron), writes, and exits — no daemon.
//
// main contains no logic: it constructs and connects, then runs the pipeline:
//
//	load config -> open cache -> sync feeds -> fetch (fan-out, single-writer
//	collector) -> render outputs + health (atomic writes) -> purge expired
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mwyvr/firehose"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "firehose: %v\n", err)
		os.Exit(1)
	}
}

// run dispatches subcommands. Wiring only — the work lives in the feed/,
// sqlite/, and render/ packages behind the root interfaces.
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("firehose", flag.ContinueOnError)
	configPath := fs.String("config", "/etc/firehose/config.toml", "path to TOML config")
	force := fs.Bool("force", false, "generate: fetch every feed now, ignoring backoff gates")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "firehose %s\n", firehose.Version)
		_, _ = fmt.Fprint(fs.Output(), `usage: firehose [flags] [command]

Fetches RSS/Atom feeds and writes static river-of-news HTML. With no command,
runs a full generate pass: fetch -> cache -> render (atomic writes). With
-force, every feed is attempted now, ignoring failure-backoff gates.

commands:
  check     validate config and dry-run templates (no network, no cache)
  export    write the feed list as OPML to stdout (-section NAME to filter)
  init      write an annotated default configuration to stdout
  test URL  fetch and diagnose one feed, verbose (no cache; config optional)
  version   print the version
            flags: -ua STRING, -H "Key: Value" (repeatable)

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // usage already printed; -h is not an error
		}
		return err
	}

	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "init":
			fmt.Print(firehose.DefaultConfigTOML())
			return nil
		case "version":
			fmt.Println(firehose.Version)
			return nil
		case "check":
			return runCheck(*configPath)
		case "export":
			return runExport(*configPath, fs.Args()[1:])
		case "test":
			return runTest(ctx, *configPath, fs.Args()[1:])
		default:
			fs.Usage()
			return fmt.Errorf("unknown subcommand %q", fs.Arg(0))
		}
	}
	return runGenerate(ctx, *configPath, *force)
}

// loadAndReport loads config and prints warnings to stderr (the root package
// does no I/O; printing is cmd's job).
func loadAndReport(configPath string) (*firehose.Config, error) {
	cfg, err := firehose.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "firehose: warning: %s\n", w)
	}
	return cfg, nil
}

// runGenerate is the default path: fetch, cache, render, write.
//
// A top-level recover converts a panic in any shared phase into a failure
// banner on firehose.html (via the dependency-free fallback writer) plus a
// nonzero exit for systemd. Per-feed panics never reach here — the fetcher
// isolates those. recover() does not cover OOM kills or SIGKILL; atomic
// writes and systemd's own failure tracking cover the rest.
