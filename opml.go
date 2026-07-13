package firehose

import (
	"encoding/xml"
	"slices"
)

// OPML is the domain representation of the feed list for interchange
// (export today; import is planned). Sections map to nested outline groups;
// a feed in multiple sections is duplicated under each — lossless, and
// truest to how firehose treats categories.
type OPML struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    OPMLHead `xml:"head"`
	Body    OPMLBody `xml:"body"`
}

// OPMLHead carries document metadata.
type OPMLHead struct {
	Title string `xml:"title"`
}

// OPMLBody holds the top-level outlines.
type OPMLBody struct {
	Outlines []Outline `xml:"outline"`
}

// Outline is one OPML node: a section group (Children set) or a feed
// (Type "rss", XMLURL set).
type Outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	Children []Outline `xml:"outline,omitempty"`
}

// BuildOPML assembles the OPML document for a config. Section groups are
// emitted in output order (ALL and health outputs are not groups); feeds
// matching no section land at the top level. A non-empty section name limits
// the document to that one group.
func BuildOPML(cfg *Config, section string) *OPML {
	doc := &OPML{Version: "2.0", Head: OPMLHead{Title: "firehose feeds"}}
	grouped := map[string]bool{} // feed URLs placed in at least one group

	for _, out := range cfg.ToOutputs() {
		if out.Health || out.IsAll() {
			continue
		}
		if section != "" && out.Name != section {
			continue
		}
		group := Outline{Text: out.Title}
		if group.Text == "" {
			group.Text = out.Name
		}
		for _, fc := range cfg.Feeds {
			if !feedInOutput(fc, out) {
				continue
			}
			group.Children = append(group.Children, feedOutline(fc))
			grouped[fc.URL] = true
		}
		if len(group.Children) > 0 {
			doc.Body.Outlines = append(doc.Body.Outlines, group)
		}
	}

	// Feeds matching no section land at the top level (full export only).
	if section == "" {
		for _, fc := range cfg.Feeds {
			if !grouped[fc.URL] {
				doc.Body.Outlines = append(doc.Body.Outlines, feedOutline(fc))
			}
		}
	}
	return doc
}

func feedInOutput(fc FeedConf, out *Output) bool {
	for _, c := range fc.Categories {
		if slices.Contains(out.Categories, c) {
			return true
		}
	}
	return false
}

func feedOutline(fc FeedConf) Outline {
	title := fc.Title
	if title == "" {
		title = fc.URL
	}
	return Outline{Text: title, Title: title, Type: "rss", XMLURL: fc.URL}
}
