package feed

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/mwyvr/firehose/htmlx"
	"golang.org/x/net/html/atom"
)

// normalizeTypography applies firehose's "one voice" intent to text: straight
// quotes become curly, "--"/"---" become em dashes, "..." becomes an
// ellipsis. It operates on text nodes only and never descends into <pre> or
// <code> — the cross-cutting rule; curling a quote inside a program corrupts
// it. Toggleable via settings.typography.
func normalizeTypography(fragment string) string {
	if fragment == "" {
		return fragment
	}
	nodes, err := parseFragment(fragment)
	if err != nil {
		return fragment
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		normalizeNode(n)
		if err := html.Render(&buf, n); err != nil {
			return fragment
		}
	}
	return htmlx.Rewrite(buf.String())
}

func normalizeNode(n *html.Node) {
	if n.Type == html.ElementNode && (n.DataAtom == atom.Pre || n.DataAtom == atom.Code) {
		return // never transform inside code
	}
	if n.Type == html.TextNode {
		n.Data = smarten(n.Data)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		normalizeNode(c)
	}
}

// smarten performs the character-level substitutions on a single text run.
// Quote direction: opening after start-of-run, whitespace, or an opening
// bracket/dash; closing otherwise. An apostrophe between letters is always
// a right single quote.
func smarten(s string) string {
	s = strings.ReplaceAll(s, "...", "\u2026")
	s = strings.ReplaceAll(s, "---", "\u2014")
	s = strings.ReplaceAll(s, "--", "\u2014")

	if !strings.ContainsAny(s, `"'`) {
		return s
	}

	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		switch r {
		case '"':
			if opensQuote(runes, i) {
				b.WriteRune('\u201c') // “
			} else {
				b.WriteRune('\u201d') // ”
			}
		case '\'':
			switch {
			case i > 0 && isWordRune(runes[i-1]):
				b.WriteRune('\u2019') // apostrophe / closing ’
			case opensQuote(runes, i):
				b.WriteRune('\u2018') // ‘
			default:
				b.WriteRune('\u2019') // ’
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// opensQuote reports whether a quote at index i should be an opening quote:
// at the start of the run, or after whitespace or an opening context.
func opensQuote(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	if unicode.IsSpace(prev) {
		return true
	}
	switch prev {
	case '(', '[', '{', '\u2014', '\u2013', '\u2018', '\u201c':
		return true
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
