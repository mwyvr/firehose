package firehose

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	// Provide writable output_dir and cache_db parents by pointing into dir.
	body = "[settings]\noutput_dir = " + q(filepath.Join(dir, "out")) +
		"\ncache_db = " + q(filepath.Join(dir, "cache.db")) + "\n" + body
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func q(s string) string { return `"` + s + `"` }

func TestLoadConfigDefaults(t *testing.T) {
	path := writeConfig(t, `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]

[[feed]]
url = "https://a.example/feed"
categories = ["gov"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Body != BodyFull {
		t.Errorf("default body: got %q", cfg.Settings.Body)
	}
	if cfg.Settings.DisplayWindow.D() != 14*24*time.Hour {
		t.Errorf("default display_window: got %s", cfg.Settings.DisplayWindow.D())
	}
	if cfg.Settings.CacheRetention.D() != 30*24*time.Hour {
		t.Errorf("default cache_retention: got %s", cfg.Settings.CacheRetention.D())
	}
	if cfg.Fetch.Concurrency != defaultConcurrency {
		t.Errorf("default concurrency: got %d", cfg.Fetch.Concurrency)
	}
	if cfg.Location == nil {
		t.Error("location not resolved")
	}
	// The three true-by-default bools, absent from the config, must come back
	// true (via toml.MetaData absence detection, not zero-value).
	if !cfg.Settings.Typography || !cfg.Settings.ReadingTime || !cfg.Settings.Highlight {
		t.Errorf("bool defaults: typography=%v reading_time=%v highlight=%v (want all true)",
			cfg.Settings.Typography, cfg.Settings.ReadingTime, cfg.Settings.Highlight)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
}

func TestLoadConfigExplicitFalseBoolsHonored(t *testing.T) {
	path := writeConfig(t, `
typography = false
reading_time = false
highlight = false

[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Settings.Typography || cfg.Settings.ReadingTime || cfg.Settings.Highlight {
		t.Errorf("explicit false overridden: typography=%v reading_time=%v highlight=%v",
			cfg.Settings.Typography, cfg.Settings.ReadingTime, cfg.Settings.Highlight)
	}
}

func TestLoadConfigUnknownKeyWarns(t *testing.T) {
	path := writeConfig(t, `
excerpt_wordz = 40

[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Warnings) == 0 {
		t.Error("misspelled key produced no warning")
	}
}

func TestResolveBodyThreeTier(t *testing.T) {
	cases := []struct {
		settings, output, feed, want BodyScope
	}{
		{BodyFull, "", "", BodyFull},                                  // settings only
		{BodyFull, BodyExcerpt, "", BodyExcerpt},                      // output wins over settings
		{BodyFull, BodyExcerpt, BodyExcerptExpand, BodyExcerptExpand}, // feed wins over all
		{"", "", "", BodyFull},                                        // absolute default
	}
	for i, c := range cases {
		if got := ResolveBody(c.settings, c.output, c.feed); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestResolveExcerptImageThreeTier(t *testing.T) {
	if got := ResolveExcerptImage(ExcerptImageNone, ExcerptImageLead, ""); got != ExcerptImageLead {
		t.Errorf("output should win: got %q", got)
	}
	if got := ResolveExcerptImage(ExcerptImageLead, ExcerptImageLead, ExcerptImageNone); got != ExcerptImageNone {
		t.Errorf("feed should win: got %q", got)
	}
	if got := ResolveExcerptImage("", "", ""); got != ExcerptImageNone {
		t.Errorf("absolute default should be none: got %q", got)
	}
}

func TestToOutputsAppendsHealth(t *testing.T) {
	cfg := &Config{Outputs: []OutputConf{{Name: "all", File: "index.html", Categories: []string{"*"}}}}
	outs := cfg.ToOutputs()
	if len(outs) != 2 {
		t.Fatalf("want 2 outputs (incl health), got %d", len(outs))
	}
	health := outs[len(outs)-1]
	if !health.Health || health.InNav || health.File != "firehose.html" {
		t.Errorf("health output wrong: %+v", health)
	}
	if !outs[0].InNav {
		t.Error("regular output should be in nav")
	}
}

// TestDefaultConfigTOMLRoundTrips keeps code and test in honest agreement
func TestPerHostSerialExplicitFalseHonored(t *testing.T) {
	path := writeConfig(t, `
[fetch]
per_host_serial = false

[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Fetch.PerHostSerial {
		t.Error("explicit per_host_serial = false overridden")
	}
}

func TestSelfHostedFontsSuppressRemoteDefault(t *testing.T) {
	path := writeConfig(t, `
[fonts]
content_src = "/fonts/CrimsonPro.woff2"

[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.FontsCSSURL(); got != "" {
		t.Errorf("self-hosted src must suppress the remote default: %q", got)
	}
}

func TestFontsCSSURLDefaultsRemote(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.FontsCSSURL(); got != defaultFontsCSSURL {
		t.Errorf("no fonts config: want remote default, got %q", got)
	}
}
