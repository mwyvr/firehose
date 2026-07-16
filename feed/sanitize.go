package feed

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"

	"github.com/mwyvr/firehose/htmlx"
	"golang.org/x/net/html/atom"
)

// policy is the structural-only sanitization policy: the tool that strips
// hostile markup is the same tool that enforces firehose's one visual voice.
// Publisher styling (style attrs, classes, fonts, tracking pixels) does not
// survive; structure does.
var policy = buildPolicy()

// languageClass preserves declared-language classes on code elements so the
// highlight pass can act on them (declared-language-only; never guess).
var languageClass = regexp.MustCompile(`^(language|lang)-[a-zA-Z0-9#+.-]+$`)

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Structure only: paragraphs, quotes, code, lists, emphasis, figures,
	// minor headings (item titles are h2; content headings nest below).
	p.AllowElements(
		"p", "br", "hr", "blockquote",
		"pre", "code",
		"em", "strong", "b", "i", "sub", "sup", "del", "mark",
		"ul", "ol", "li", "dl", "dt", "dd",
		"figure", "figcaption",
		"h3", "h4", "h5", "h6",
		"table", "thead", "tbody", "tr", "th", "td",
	)

	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("src", "alt", "loading", "width", "height").OnElements("img")
	p.AllowAttrs("class").Matching(languageClass).OnElements("code")

	p.AllowURLSchemes("http", "https")
	p.RequireNoFollowOnLinks(true)

	return p
}

// compileStrip compiles per-feed strip_selectors. An invalid selector is a
// config error (EINVALID at the feed level; also caught by `firehose check`
// via CheckConfig).
func compileStrip(selectors []string) ([]cascadia.SelectorGroup, error) {
	if len(selectors) == 0 {
		return nil, nil
	}
	groups := make([]cascadia.SelectorGroup, 0, len(selectors))
	for _, s := range selectors {
		g, err := cascadia.ParseGroup(s)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// sanitize converts raw feed HTML into firehose's one clean voice and reports
// the word count of the result. One parse, then in order:
//
//  1. strip — remove per-feed cruft nodes (strip_selectors), pre-sanitize.
//  2. absolutize — resolve relative href/src against base (the item's link);
//     relative URLs would otherwise resolve against the firehose host and 404.
//     Images are marked loading="lazy" here.
//  3. sanitize — bluemonday structural policy on the rendered result.
//
// Code blocks pass through untouched apart from the allowed language-* class
// (the never-transform-inside-pre/code rule; nothing here rewrites text
// nodes).
func sanitize(raw, baseURL string, strip []cascadia.SelectorGroup) (clean string, words int) {
	if strings.TrimSpace(raw) == "" {
		return "", 0
	}

	nodes, err := parseFragment(raw)
	if err != nil {
		// Parse trouble: sanitize the input as-is — safety is never reduced,
		// only link fixing and stripping are skipped.
		clean = policy.Sanitize(raw)
		return clean, countWords(clean)
	}

	base, berr := url.Parse(baseURL)
	resolve := berr == nil && base.IsAbs()

	var buf bytes.Buffer
	for _, n := range nodes {
		if matchesAny(n, strip) {
			continue
		}
		stripNodes(n, strip)
		if resolve {
			absolutizeTree(n, base)
		}
		if err := html.Render(&buf, n); err != nil {
			clean = policy.Sanitize(raw)
			return clean, countWords(clean)
		}
	}

	clean = htmlx.Rewrite(policy.Sanitize(buf.String()))
	return clean, countWords(clean)
}

// Clean re-sanitizes an HTML fragment with the structural policy. The render
// layer uses it after excerpt truncation (the truncate-then-sanitize rule).
func Clean(fragment string) string { return policy.Sanitize(fragment) }

func parseFragment(fragment string) ([]*html.Node, error) {
	ctxNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	return html.ParseFragment(strings.NewReader(fragment), ctxNode)
}

func matchesAny(n *html.Node, strip []cascadia.SelectorGroup) bool {
	if n.Type != html.ElementNode {
		return false
	}
	for _, g := range strip {
		if g.Match(n) {
			return true
		}
	}
	return false
}

// stripNodes removes descendants matching any strip selector.
func stripNodes(n *html.Node, strip []cascadia.SelectorGroup) {
	if len(strip) == 0 {
		return
	}
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if matchesAny(c, strip) {
			n.RemoveChild(c)
			continue
		}
		stripNodes(c, strip)
	}
}

// absolutizeTree rewrites a[href] and img[src] to absolute URLs against base
// and sets loading="lazy" on images.
func absolutizeTree(n *html.Node, base *url.URL) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.A:
			resolveAttr(n, "href", base)
		case atom.Img:
			resolveAttr(n, "src", base)
			setAttr(n, "loading", "lazy")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		absolutizeTree(c, base)
	}
}

func resolveAttr(n *html.Node, name string, base *url.URL) {
	for i := range n.Attr {
		if n.Attr[i].Key != name {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(n.Attr[i].Val))
		if err != nil || ref.IsAbs() {
			return
		}
		n.Attr[i].Val = base.ResolveReference(ref).String()
		return
	}
}

func setAttr(n *html.Node, name, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == name {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: name, Val: val})
}

// textContent extracts the text of an HTML fragment.
func textContent(fragment string) string {
	if fragment == "" {
		return ""
	}
	tok := html.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	for {
		switch tok.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			b.Write(tok.Text())
			b.WriteByte(' ')
		}
	}
}

// countWords counts words in the text content of sanitized HTML.
func countWords(clean string) int {
	return len(strings.Fields(textContent(clean)))
}

// firstImgSrc returns the src of the first <img> in an HTML fragment, or "".
func firstImgSrc(fragment string) string {
	tok := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch tok.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tok.TagName()
			if string(name) != "img" || !hasAttr {
				continue
			}
			for {
				k, v, more := tok.TagAttr()
				if string(k) == "src" {
					return string(v)
				}
				if !more {
					break
				}
			}
		}
	}
}
