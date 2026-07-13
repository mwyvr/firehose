package firehose

import (
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the decoded TOML configuration.
type Config struct {
	Settings Settings     `toml:"settings"`
	Fetch    FetchConfig  `toml:"fetch"`
	Fonts    FontConfig   `toml:"fonts"`
	Outputs  []OutputConf `toml:"output"`
	Feeds    []FeedConf   `toml:"feed"`

	// Location is the resolved display *time.Location, threaded through parsing
	// (ParseInLocation) and rendering. A TZ slip reorders the river, not just
	// misdisplays it
	Location *time.Location `toml:"-"`

	// Warnings collects non-fatal problems found while loading (e.g. unknown
	// keys, likely typos).
	Warnings []string `toml:"-"`
}

// Settings holds global defaults and paths.
type Settings struct {
	OutputDir          string       `toml:"output_dir"`
	CacheDB            string       `toml:"cache_db"`
	DisplayWindow      Duration     `toml:"display_window"`  // what renders (e.g. 14d)
	CacheRetention     Duration     `toml:"cache_retention"` // GUID history (longer)
	Timezone           string       `toml:"timezone"`        // IANA name; resolved into Location
	Body               BodyScope    `toml:"body"`
	ExcerptWords       int          `toml:"excerpt_words"`
	ExcerptImage       ExcerptImage `toml:"excerpt_image"`
	Typography         bool         `toml:"typography"`
	ReadingTime        bool         `toml:"reading_time"`
	Highlight          bool         `toml:"highlight"`       // declared-language-only; never guess
	Dedupe             bool         `toml:"dedupe"`          // collapse the same story arriving via multiple feeds
	Theme              string       `toml:"theme"`           // auto | light | dark (page default; toggle overrides)
	HighlightTheme     string       `toml:"highlight_theme"` // chroma style, light mode
	HighlightThemeDark string       `toml:"highlight_theme_dark"`

	// Integration with the Author's CMS; NoteTemplate, when set, renders a per-item "note" link with {title} and
	// {url} substituted (query-escaped) No backend: it is just a URL and could be used to support other CMS.
	NoteTemplate string `toml:"note_template"`
}

// FetchConfig holds politeness and concurrency controls.
type FetchConfig struct {
	Concurrency   int      `toml:"concurrency"`
	PerHostSerial bool     `toml:"per_host_serial"`
	Timeout       Duration `toml:"timeout"`
	UserAgent     string   `toml:"user_agent"`

	// AcceptLanguage is sent on every request when non-empty. Browsers always
	// send it; its absence is a bot tell for CDN filtering. We are honest
	// except when we cannot be, but this is a personal use tool after all.
	AcceptLanguage string `toml:"accept_language"`
}

// FontConfig holds the content/chrome family split and where the font files
// come from: a remote stylesheet (default: Google Fonts for the default
// families) or self-hosted woff2 sources, which take precedence when set.
type FontConfig struct {
	ContentFamily string `toml:"content_family"`
	ChromeFamily  string `toml:"chrome_family"`

	// CSSURL is a remote font stylesheet imported by style.css. Defaulted to
	// Google Fonts for the default families when no self-hosted sources are
	// configured. If you change the families, change this too (or self-host).
	CSSURL string `toml:"css_url"`

	// Self-hosted overrides: when set, @font-face rules are emitted and no
	// remote stylesheet is defaulted.
	ContentSrc string `toml:"content_src"`
	ChromeSrc  string `toml:"chrome_src"`
}

// OutputConf is a configured section/river.
type OutputConf struct {
	Name         string       `toml:"name"`
	File         string       `toml:"file"`
	Title        string       `toml:"title"`
	Categories   []string     `toml:"categories"`
	Body         BodyScope    `toml:"body"`
	ExcerptImage ExcerptImage `toml:"excerpt_image"`
	ReadingTime  *bool        `toml:"reading_time"`
}

// FeedConf is a configured feed and its per-feed overrides.
type FeedConf struct {
	URL            string       `toml:"url"`
	Title          string       `toml:"title"` // override garbage self-reported titles
	Categories     []string     `toml:"categories"`
	Body           BodyScope    `toml:"body"`
	ExcerptImage   ExcerptImage `toml:"excerpt_image"`
	Exclude        []string     `toml:"exclude"`
	Include        []string     `toml:"include"`
	StripSelectors []string     `toml:"strip_selectors"`
	DisplayWindow  Duration     `toml:"display_window"` // per-feed override; zero inherits settings

	// Per-feed fetch overrides (CDN-hostile endpoints).
	UserAgent string            `toml:"user_agent"`
	Headers   map[string]string `toml:"headers"`
}

// Duration is a TOML-decodable time.Duration accepting Go duration strings
// ("336h", "20s"). Keeps the config surface human-friendly.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler for TOML decoding.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return Errorf(EINVALID, "invalid duration %q: %v", string(text), err)
	}
	*d = Duration(v)
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Default values applied to any field left unset.

const (
	defaultConcurrency    = 8
	defaultTimeout        = 20 * time.Second
	defaultUserAgent      = "firehose/1.0 (+https://github.com/mwyvr/firehose)"
	defaultAcceptLanguage = "en"
	defaultContentFamily  = "Crimson Pro"
	defaultChromeFamily   = "IBM Plex Sans"
	// defaultFontsCSSURL serves the two default families above. If the
	// families change, this URL no longer matches — set css_url or self-host.
	defaultFontsCSSURL = "https://fonts.googleapis.com/css2?family=Crimson+Pro:ital,wght@0,400;0,600;1,400;1,600&family=IBM+Plex+Sans:wght@400;600&display=swap"
)

// LoadConfig reads and decodes the TOML config at path, applies defaults,
// resolves the display Location, and validates.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, Errorf(EINVALID, "decoding config %s: %v", path, err)
	}
	// misspelled or unknown keys are not fatal
	for _, key := range md.Undecoded() {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("unknown config key %q", key.String()))
	}

	if err := cfg.resolveLocation(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WindowFor resolves a feed's display window: the per-feed override when
// set, else the global setting. Slow civic feeds get long windows without
// bloating every section.
func (c *Config) WindowFor(fc FeedConf) time.Duration {
	if fc.DisplayWindow.D() > 0 {
		return fc.DisplayWindow.D()
	}
	return c.Settings.DisplayWindow.D()
}

// MaxDisplayWindow is the widest window any feed uses — the single Since
// bound for the item query; per-feed narrowing happens at render.
func (c *Config) MaxDisplayWindow() time.Duration {
	w := c.Settings.DisplayWindow.D()
	for _, fc := range c.Feeds {
		if fc.DisplayWindow.D() > w {
			w = fc.DisplayWindow.D()
		}
	}
	return w
}

// FeedConfByURL maps config feed blocks by URL
func (c *Config) FeedConfByURL() map[string]FeedConf {
	byURL := make(map[string]FeedConf, len(c.Feeds))
	for _, fc := range c.Feeds {
		byURL[fc.URL] = fc
	}
	return byURL
}

// FontsCSSURL resolves the remote font stylesheet at runtime
func (c *Config) FontsCSSURL() string {
	switch {
	case c.Fonts.CSSURL != "":
		return c.Fonts.CSSURL
	case c.Fonts.ContentSrc != "" || c.Fonts.ChromeSrc != "":
		return ""
	}
	return defaultFontsCSSURL
}

// resolveLocation resolves Settings.Timezone (IANA name) into Location,
// defaulting to time.Local when unset.
func (c *Config) resolveLocation() error {
	if c.Settings.Timezone == "" {
		c.Location = time.Local
		return nil
	}
	loc, err := time.LoadLocation(c.Settings.Timezone)
	if err != nil {
		return Errorf(EINVALID, "invalid timezone %q: %v", c.Settings.Timezone, err)
	}
	c.Location = loc
	return nil
}

// validBodyScopes and validExcerptImages gate config values.
var validBodyScopes = map[BodyScope]bool{
	BodyTitle: true, BodyExcerpt: true, BodyFull: true, BodyExcerptExpand: true,
}

var validExcerptImages = map[ExcerptImage]bool{
	ExcerptImageLead: true, ExcerptImageNone: true,
}

// Validate checks the fully-defaulted config for internal consistency. Returns
// the first problem as an EINVALID error. This is the core of `firehose check`.
func (c *Config) Validate() error {
	s := c.Settings
	if s.OutputDir == "" {
		return Errorf(EINVALID, "settings.output_dir is required")
	}
	if s.CacheDB == "" {
		return Errorf(EINVALID, "settings.cache_db is required")
	}
	if s.CacheRetention.D() < s.DisplayWindow.D() {
		return Errorf(EINVALID,
			"cache_retention (%s) must be >= display_window (%s): retention shorter than the display window causes re-published old items to reappear",
			s.CacheRetention.D(), s.DisplayWindow.D())
	}
	if !validBodyScopes[s.Body] {
		return Errorf(EINVALID, "settings.body %q invalid (want title|excerpt|full|excerpt+expand)", s.Body)
	}
	if !validExcerptImages[s.ExcerptImage] {
		return Errorf(EINVALID, "settings.excerpt_image %q invalid (want lead|none)", s.ExcerptImage)
	}
	if s.ExcerptWords < 1 {
		return Errorf(EINVALID, "settings.excerpt_words must be >= 1")
	}

	if len(c.Outputs) == 0 {
		return Errorf(EINVALID, "at least one [[output]] is required")
	}

	// Collect the set of categories any output selects, and detect an ALL
	// river. Validate per-output fields and duplicate output files.
	seenFile := map[string]string{} // file -> output name
	seenName := map[string]bool{}
	selected := map[string]bool{} // categories covered by some output
	haveAll := false

	for i := range c.Outputs {
		o := &c.Outputs[i]
		if o.Name == "" {
			return Errorf(EINVALID, "output[%d]: name is required", i)
		}
		if seenName[o.Name] {
			return Errorf(EINVALID, "duplicate output name %q", o.Name)
		}
		seenName[o.Name] = true

		if o.File == "" {
			return Errorf(EINVALID, "output %q: file is required", o.Name)
		}
		if prev, dup := seenFile[o.File]; dup {
			return Errorf(EINVALID, "outputs %q and %q both write file %q", prev, o.Name, o.File)
		}
		seenFile[o.File] = o.Name

		if o.Body != "" && !validBodyScopes[o.Body] {
			return Errorf(EINVALID, "output %q: body %q invalid", o.Name, o.Body)
		}
		if o.ExcerptImage != "" && !validExcerptImages[o.ExcerptImage] {
			return Errorf(EINVALID, "output %q: excerpt_image %q invalid", o.Name, o.ExcerptImage)
		}

		if len(o.Categories) == 0 {
			haveAll = true
		}
		for _, cat := range o.Categories {
			if cat == "*" {
				haveAll = true
			} else {
				selected[cat] = true
			}
		}
	}

	// Every feed category must be reachable: either an ALL river exists, or
	// some output selects that category. Otherwise items would be fetched and
	// never rendered anywhere — a silent black hole.
	switch c.Settings.Theme {
	case "auto", "light", "dark":
	default:
		return Errorf(EINVALID, "settings.theme %q: want auto, light, or dark", c.Settings.Theme)
	}

	for _, fc := range c.Feeds {
		if d := fc.DisplayWindow.D(); d > 0 && d > c.Settings.CacheRetention.D() {
			return Errorf(EINVALID,
				"feed %s: display_window %s exceeds cache_retention %s (items would be purged before they could render)",
				fc.URL, fc.DisplayWindow.D(), c.Settings.CacheRetention.D())
		}
	}

	seenFeed := map[string]bool{}
	for i := range c.Feeds {
		fd := &c.Feeds[i]
		if fd.URL == "" {
			return Errorf(EINVALID, "feed[%d]: url is required", i)
		}
		if seenFeed[fd.URL] {
			return Errorf(EINVALID, "duplicate feed url %s", fd.URL)
		}
		seenFeed[fd.URL] = true
		if fd.Body != "" && !validBodyScopes[fd.Body] {
			return Errorf(EINVALID, "feed %s: body %q invalid", fd.URL, fd.Body)
		}
		if fd.ExcerptImage != "" && !validExcerptImages[fd.ExcerptImage] {
			return Errorf(EINVALID, "feed %s: excerpt_image %q invalid", fd.URL, fd.ExcerptImage)
		}
		if !haveAll {
			for _, cat := range fd.Categories {
				if !selected[cat] {
					return Errorf(EINVALID,
						"feed %s: category %q is not selected by any output and there is no ALL river",
						fd.URL, cat)
				}
			}
		}
	}

	return nil
}

// ToOutputs converts config outputs into domain Output values, applying the
// InNav/Health flags. The synthetic health output (firehose.html) is appended:
// generated every run, excluded from nav.
func (c *Config) ToOutputs() []*Output {
	outs := make([]*Output, 0, len(c.Outputs)+1)
	for i := range c.Outputs {
		oc := &c.Outputs[i]
		outs = append(outs, &Output{
			Name:         oc.Name,
			File:         oc.File,
			Title:        oc.Title,
			Categories:   oc.Categories,
			Body:         oc.Body,
			ExcerptImage: oc.ExcerptImage,
			ReadingTime:  oc.ReadingTime,
			InNav:        true,
		})
	}
	outs = append(outs, &Output{
		Name:   "health",
		File:   "firehose.html",
		Title:  "firehose",
		InNav:  false,
		Health: true,
	})
	return outs
}

// ToFeeds converts config feeds into domain Feed values
func (c *Config) ToFeeds() []*Feed {
	feeds := make([]*Feed, 0, len(c.Feeds))
	for i := range c.Feeds {
		fc := &c.Feeds[i]
		feeds = append(feeds, &Feed{
			URL:            fc.URL,
			Title:          fc.Title,
			Categories:     fc.Categories,
			Body:           string(fc.Body),
			ExcerptImage:   string(fc.ExcerptImage),
			Exclude:        fc.Exclude,
			Include:        fc.Include,
			StripSelectors: fc.StripSelectors,
			UserAgent:      fc.UserAgent,
			Headers:        fc.Headers,
		})
	}
	return feeds
}

// ResolveBody applies three-tier resolution: feed wins over output wins over
// settings. Empty string means "inherit".
func ResolveBody(settings, output, feed BodyScope) BodyScope {
	if feed != "" {
		return feed
	}
	if output != "" {
		return output
	}
	if settings != "" {
		return settings
	}
	return BodyFull
}

// ResolveExcerptImage applies the same three-tier resolution for the lead-image, if any
func ResolveExcerptImage(settings, output, feed ExcerptImage) ExcerptImage {
	if feed != "" {
		return feed
	}
	if output != "" {
		return output
	}
	if settings != "" {
		return settings
	}
	return ExcerptImageNone
}
