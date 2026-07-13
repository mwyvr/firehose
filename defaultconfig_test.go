package firehose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigTOMLRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(DefaultConfigTOML()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	def := DefaultConfig().Settings
	if err != nil {
		t.Fatalf("emitted default config failed to load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("emitted default config produced warnings: %v", cfg.Warnings)
	}
	if cfg.Settings.Body != def.Body {
		t.Errorf("body drifted: emitted %q, default %q", cfg.Settings.Body, def.Body)
	}
	if cfg.Settings.DisplayWindow != def.DisplayWindow {
		t.Errorf("display_window drifted: %v vs %v", cfg.Settings.DisplayWindow.D(), def.DisplayWindow.D())
	}
	if cfg.Settings.ExcerptWords != def.ExcerptWords {
		t.Errorf("excerpt_words drifted: %d vs %d", cfg.Settings.ExcerptWords, def.ExcerptWords)
	}
	if cfg.Fetch.Concurrency != defaultConcurrency {
		t.Errorf("concurrency drifted: %d vs %d", cfg.Fetch.Concurrency, defaultConcurrency)
	}
	if !cfg.Fetch.PerHostSerial {
		t.Error("per_host_serial should default true (polite by default)")
	}
	if cfg.Fonts.ContentFamily != defaultContentFamily || cfg.Fonts.ChromeFamily != defaultChromeFamily {
		t.Errorf("font families drifted: %q / %q", cfg.Fonts.ContentFamily, cfg.Fonts.ChromeFamily)
	}
	if cfg.Fonts.CSSURL != defaultFontsCSSURL {
		t.Errorf("css_url drifted: %q", cfg.Fonts.CSSURL)
	}
	if len(cfg.Outputs) != 2 || len(cfg.Feeds) != 1 {
		t.Errorf("example structure: %d outputs, %d feeds", len(cfg.Outputs), len(cfg.Feeds))
	}
}
