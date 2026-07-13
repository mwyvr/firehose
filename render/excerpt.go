package render

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/mwyvr/firehose"
	"github.com/mwyvr/firehose/feed"
	"github.com/mwyvr/firehose/htmlx"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// excerpt produces the excerpt-scope rendering of an item body.
//
// The two axes from the design:
//
//  1. Text: walk the parsed tree accumulating words until the budget is
//     reached, truncating at element granularity on a word boundary. Code
//     blocks are DROPPED from excerpts entirely — the never-cut-inside-pre/code
//     rule, resolved at whole-block granularity: a teaser is no place for a
//     half-shown program, and including whole blocks would blow the budget.
//  2. Image: "none" strips all images; "lead" strips all images then restores
//     the item's single LeadImage (lazy, if one exists) ahead of the text.
//
// Order: truncate the PARSED TREE, then re-sanitize the serialized fragment
// (feed.Clean). Never truncate a serialized HTML string.
func excerpt(it *firehose.Item, words int, image firehose.ExcerptImage) string {
	src := it.SummaryHTML // excerpt source of truth: feed summary first
	if src == "" {
		src = it.BodyHTML // else truncate the (sanitized) body
	}
	if strings.TrimSpace(src) == "" {
		return leadImageHTML(it, image) // nothing textual; maybe just the image
	}

	truncated := truncateWords(src, words)
	out := leadImageHTML(it, image) + feed.Clean(truncated)
	return out
}

// leadImageHTML renders the restored lead image for image == "lead".
func leadImageHTML(it *firehose.Item, image firehose.ExcerptImage) string {
	if image != firehose.ExcerptImageLead || it.LeadImage == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<figure class="feeditem__leadimage"><img src="`)
	b.WriteString(html.EscapeString(it.LeadImage))
	b.WriteString(`" alt="" loading="lazy"></figure>`)
	return b.String()
}

// truncateWords walks the fragment tree, keeping nodes until the word budget
// is exhausted. Images and pre blocks are dropped (images are handled by the
// lead-image axis; code blocks never appear truncated). Truncation lands on a
// word boundary with an ellipsis.
func truncateWords(fragment string, budget int) string {
	ctxNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctxNode)
	if err != nil {
		// Fall back to a plain-text truncation of the stripped content; the
		// caller re-sanitizes either way.
		fields := strings.Fields(fragment)
		if len(fields) > budget {
			fields = fields[:budget]
			return strings.Join(fields, " ") + "\u2026"
		}
		return strings.Join(fields, " ")
	}

	remaining := budget
	var keep []*html.Node
	for _, n := range nodes {
		if remaining <= 0 {
			break
		}
		if kept := prune(n, &remaining); kept != nil {
			keep = append(keep, kept)
		}
	}

	var buf bytes.Buffer
	for _, n := range keep {
		if err := html.Render(&buf, n); err != nil {
			return fragment
		}
	}
	return htmlx.Rewrite(buf.String())
}

// truncateText cuts s after budget words, preserving the ORIGINAL bytes of
// the kept prefix — leading and internal whitespace included. Rebuilding via
// strings.Fields+Join here once fused a link with the following word ("Route
// 25takes"): the space separating inline elements lives at the START of the
// adjacent text node, and Fields eats it.
func truncateText(s string, budget int) (out string, used int, cut bool) {
	count := 0
	inWord := false
	for i, r := range s {
		if unicode.IsSpace(r) {
			if inWord {
				inWord = false
				if count == budget {
					return s[:i] + "\u2026", budget, true
				}
			}
			continue
		}
		if !inWord {
			inWord = true
			count++
		}
	}
	return s, count, false // fits (a final word ending at EOS is within budget)
}

// prune trims node n to the remaining word budget, returning nil if the node
// should be dropped entirely. It mutates the tree in place (the tree is
// throwaway, parsed for this call).
func prune(n *html.Node, remaining *int) *html.Node {
	if *remaining <= 0 {
		return nil
	}
	switch n.Type {
	case html.TextNode:
		out, used, cut := truncateText(n.Data, *remaining)
		if !cut {
			*remaining -= used
			return n // untouched: inter-node whitespace preserved
		}
		n.Data = out
		*remaining = 0
		return n
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Pre, atom.Img, atom.Figure:
			// Dropped from excerpts: code blocks are never cut, images are
			// the lead-image axis's business (figure typically wraps an img).
			return nil
		}
	case html.CommentNode:
		return nil
	}

	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if *remaining <= 0 {
			n.RemoveChild(c)
			continue
		}
		if prune(c, remaining) == nil {
			n.RemoveChild(c)
		}
	}
	return n
}
