package firehose

import "testing"

func opmlConfig(t *testing.T) *Config {
	t.Helper()
	path := writeConfig(t, `
[[output]]
name = "all"
file = "index.html"
categories = ["*"]

[[output]]
name = "tech"
file = "tech.html"
title = "Technology"
categories = ["tech"]

[[feed]]
url = "https://a.example/feed"
title = "A"
categories = ["tech"]

[[feed]]
url = "https://b.example/feed"
categories = ["orphaned"]
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestBuildOPML(t *testing.T) {
	doc := BuildOPML(opmlConfig(t), "")
	if len(doc.Body.Outlines) != 2 {
		t.Fatalf("want tech group + 1 ungrouped feed, got %d outlines", len(doc.Body.Outlines))
	}
	tech := doc.Body.Outlines[0]
	if tech.Text != "Technology" || len(tech.Children) != 1 || tech.Children[0].XMLURL != "https://a.example/feed" {
		t.Errorf("tech group wrong: %+v", tech)
	}
	loose := doc.Body.Outlines[1]
	if loose.XMLURL != "https://b.example/feed" || loose.Title != "https://b.example/feed" {
		t.Errorf("ungrouped feed wrong: %+v", loose)
	}

	only := BuildOPML(opmlConfig(t), "tech")
	if len(only.Body.Outlines) != 1 || len(only.Body.Outlines[0].Children) != 1 {
		t.Errorf("-section filter wrong: %+v", only.Body.Outlines)
	}
}
