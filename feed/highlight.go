package feed

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"golang.org/x/net/html"

	"github.com/mwyvr/firehose/htmlx"
	"golang.org/x/net/html/atom"
)

// highlightFormatter emits class-based markup (no inline colors), so the
// stylesheet — which knows about light and dark mode — owns the palette.
var highlightFormatter = chromahtml.New(chromahtml.WithClasses(true))

// Highlight applies generate-time syntax highlighting to code blocks that
// DECLARE a language (class="language-x" / "lang-x"). It never guesses:
// chroma's analyser is unreliable enough to be worse than nothing, and plain
// monospace is firehose's honest voice for undeclared code — click through
// for the publisher's presentation. Toggleable via settings.highlight.
func Highlight(fragment string) string {
	if !strings.Contains(fragment, "language-") && !strings.Contains(fragment, "lang-") {
		return fragment
	}
	nodes, err := parseFragment(fragment)
	if err != nil {
		return fragment
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		// A code block is commonly a fragment ROOT, not a descendant; handle
		// the root case explicitly (parents handle the descendant case).
		if n.Type == html.ElementNode && n.DataAtom == atom.Pre {
			if repl := highlightPre(n); repl != nil {
				for _, r := range repl {
					if err := html.Render(&buf, r); err != nil {
						return fragment
					}
				}
				continue
			}
			if err := html.Render(&buf, n); err != nil {
				return fragment
			}
			continue
		}
		highlightTree(n)
		if err := html.Render(&buf, n); err != nil {
			return fragment
		}
	}
	return htmlx.Rewrite(buf.String())
}

func highlightTree(n *html.Node) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.ElementNode && c.DataAtom == atom.Pre {
			if repl := highlightPre(c); repl != nil {
				for _, r := range repl {
					n.InsertBefore(r, c)
				}
				n.RemoveChild(c)
			}
			continue // never descend into pre
		}
		highlightTree(c)
	}
}

// highlightPre returns replacement nodes for a <pre> whose code child
// declares a known language, or nil to leave the block untouched (plain).
func highlightPre(pre *html.Node) []*html.Node {
	code := firstElementChild(pre)
	if code == nil || code.DataAtom != atom.Code {
		return nil
	}
	lang := declaredLanguage(code)
	if lang == "" {
		return nil
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return nil // declared but unknown: stay plain, never guess
	}

	src := strings.TrimRight(textOf(code), "\n")
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return nil
	}
	var out bytes.Buffer
	// Style argument is required by the API but unused with class output.
	if err := highlightFormatter.Format(&out, styles.Fallback, iterator); err != nil {
		return nil
	}
	repl, err := parseFragment(out.String())
	if err != nil {
		return nil
	}
	return repl
}

func declaredLanguage(code *html.Node) string {
	for _, a := range code.Attr {
		if a.Key != "class" {
			continue
		}
		for cls := range strings.FieldsSeq(a.Val) {
			if languageClass.MatchString(cls) {
				cls = strings.TrimPrefix(cls, "language-")
				cls = strings.TrimPrefix(cls, "lang-")
				return cls
			}
		}
	}
	return ""
}

func firstElementChild(n *html.Node) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
