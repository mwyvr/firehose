package firehose

import "testing"

func TestLocalFeedURLForms(t *testing.T) {
	if !IsLocalFeed("file:///var/lib/hydrant/site.xml") || IsLocalFeed("https://x.example/feed") {
		t.Fatal("scheme detection wrong")
	}
	if got := LocalFeedPath("file:///var/lib/hydrant/site.xml"); got != "/var/lib/hydrant/site.xml" {
		t.Fatalf("path extraction: %q", got)
	}
}

func TestLocalFeedConfigCanonicalizedAndValidated(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]

[[feed]]
url = "/var/lib/hydrant/site.xml"
categories = ["scraped"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Feeds[0].URL != "file:///var/lib/hydrant/site.xml" {
		t.Fatalf("bare path not canonicalized: %q", cfg.Feeds[0].URL)
	}

	_, err = LoadConfig(writeConfig(t, `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]

[[feed]]
url = "file://feeds/site.xml"
categories = ["scraped"]
`))
	if ErrorCode(err) != EINVALID {
		t.Fatalf("relative file:// path must be rejected, got %v", err)
	}
}
