package firehose

import (
	"strings"
	"testing"
)

func TestValidateRejects(t *testing.T) {
	allOutput := `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`
	cases := []struct {
		name    string
		toml    string
		wantMsg string
	}{
		{"retention shorter than window", `
display_window = "720h"
cache_retention = "336h"
` + allOutput, "cache_retention"},

		{"unreachable feed category (no ALL river)", `
[[output]]
name = "gov"
file = "gov.html"
categories = ["gov"]

[[feed]]
url = "https://a.example/feed"
categories = ["photography"]
`, "photography"},

		{"duplicate output file", `
[[output]]
name = "a"
file = "same.html"
categories = ["*"]

[[output]]
name = "b"
file = "same.html"
categories = ["tech"]
`, "same.html"},

		{"duplicate output name", `
[[output]]
name = "twin"
file = "one.html"
categories = ["*"]

[[output]]
name = "twin"
file = "two.html"
categories = ["tech"]
`, `"twin"`},

		{"bad timezone", `
timezone = "Not/AZone"
` + allOutput, "timezone"},

		{"duplicate feed url", allOutput + `
[[feed]]
url = "https://a.example/feed"
categories = ["x"]

[[feed]]
url = "https://a.example/feed"
categories = ["y"]
`, "duplicate feed url"},

		{"feed url required", allOutput + `
[[feed]]
categories = ["x"]
`, "url is required"},

		{"rewrite_host with scheme", allOutput + `
[[feed]]
url = "https://a.example/feed"
categories = ["x"]
rewrite_host = { "https://wrong.example" = "right.example" }
`, "rewrite_host"},

		{"unknown theme", `
theme = "sepia"
` + allOutput, "sepia"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(writeConfig(t, tc.toml))
			if ErrorCode(err) != EINVALID {
				t.Fatalf("want EINVALID, got %v", err)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error does not identify the rule: want %q in %q", tc.wantMsg, err)
			}
		})
	}
}

func TestFeedTimezoneValidated(t *testing.T) {
	bad := `
[[feed]]
url = "https://x.example/feed"
timezone = "Mars/Olympus"

[[output]]
name = "all"
file = "index.html"
categories = ["*"]
`
	_, err := LoadConfig(writeConfig(t, bad))
	if ErrorCode(err) != EINVALID || !strings.Contains(err.Error(), "timezone") {
		t.Fatalf("want EINVALID naming timezone, got %v", err)
	}
	good := strings.Replace(bad, "Mars/Olympus", "America/Edmonton", 1)
	if _, err := LoadConfig(writeConfig(t, good)); err != nil {
		t.Fatalf("valid feed timezone rejected: %v", err)
	}
}
