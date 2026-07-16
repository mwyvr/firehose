![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/mwyvr/kid)
[![Test](https://github.com/mwyvr/kid/actions/workflows/test.yaml/badge.svg)](https://github.com/mwyvr/kid/actions/workflows/test.yaml)
[![ci](https://github.com/mwyvr/firehose/actions/workflows/ci.yml/badge.svg)](https://github.com/mwyvr/firehose/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)![Coverage](https://img.shields.io/badge/coverage-92.6%25-brightgreen)

# firehose

<!--toc:start-->
- [firehose](#firehose)
  - [Install](#install)
  - [Examples](#examples)
  - [Quick start](#quick-start)
  - [Deployment](#deployment)
  - [Changelog](#changelog)
  - [Future](#future)
  - [License](#license)
<!--toc:end-->

`firehose` is an RSS/Atom feed aggregator and static HTML generator that renders
your chosen feeds as a single reverse-chronological *river of news*. Inbound
feed content is sanitized for safety, typography is normalized, and the layout
and styling are kept deliberately simple to favor reading on small and larger
devices alike.

`firehose` runs as a batch job — fetch, render, write, exit — typically from
a systemd timer or cron. Nothing stays resident and nothing listens on a port.
Deploy locally or remote using the web infrastructure you already have.

Why the name? Think *drinking from a fire hose*.

## Install

`firehose` is deliberately not dependent on `cgo` and thus is
portable across operating systems and architectures. The [releases
page](https://github.com/mwyvr/firehose/releases) contains pre-built static
binaries for amd64/arm64 architecture macOS and Linux distributions.

Or, build it yourself:

```
go install github.com/mwyvr/firehose/cmd/firehose@latest
```
## Examples

![Example 1](docs/firehose1.png) ![Example 2](docs/firehose2.png)

## Quick start

```
firehose init > config.toml     # every option, annotated, at its default
$EDITOR config.toml             # add feeds and sections; set output_dir, cache_db
firehose -config config.toml check
firehose -config config.toml    # generate
```

**Usage**:

```
firehose               generate: fetch, cache, render, write (default)
firehose -force        generate, ignoring failure-backoff gates
firehose check         validate config + dry-run templates (no network)
firehose test URL      diagnose one feed, verbose (-ua, -H "K: V" to bisect)
firehose export        feed list as OPML to stdout (-section NAME)
firehose init          annotated default config to stdout
firehose version       print the version
```

**Try it locally**:

```
make dev                    # builds, fetches sample feeds into ./build/dev, renders
open build/dev/index.html   # build/dev/firehose.html = health page
```

## Deployment

`firehose` generates static HTML to serve up via the local or remote web
server infrastructure you already have in place. See examples and more in
[docs/deploy.md](docs/deploy.md).

## Changelog

- v0.2.3, 2026-07-15: improve filters and cleanup
- v0.2.2, 2026-07-14: Add per-feed source url hostname rewrite
- v0.2.1, 2026-07-13: Fix a miss: make categories case-insensitive
- v0.2.0, 2026-07-13: Cross-feed dedupe with "also via" attribution; per-feed `display_window`
- v0.1.0, 2026-07-12: Eat my own dog food initial beta release.

## Future

Not a lot planned. Possibly:

- OPML import (`firehose export` already writes OPML)
- *Maybe* user-definable templates and CSS overrides.

## License

MIT — see [LICENSE](LICENSE).
