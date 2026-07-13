// Package htmlx ensures HTML5-correct self-closing elements as
// x/net/html.Render emits XML-style self-closing elements ("<br/>"),
package htmlx

import "regexp"

var voidSelfClose = regexp.MustCompile(`<(area|base|br|col|embed|hr|img|input|link|meta|source|track|wbr)([^>]*?)/>`)

// Rewrite rewrites self-closing void elements in x/net/html.Render output
// to HTML5 canonical form.
func Rewrite(serialized string) string {
	return voidSelfClose.ReplaceAllString(serialized, "<$1$2>")
}
