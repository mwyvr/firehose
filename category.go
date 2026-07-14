package firehose

import "strings"

// Categories are case-insensitive everywhere they are COMPARED — "Tech",
// "tech", and "TECH" name one category — while display preserves whatever
// the operator wrote. "*" is the ALL wildcard and matches literally. This
// file is the single definition of category matching; sqlite filtering,
// OPML grouping, and validation all delegate here.

// ContainsCategory reports whether cats contains want, case-insensitively.
func ContainsCategory(cats []string, want string) bool {
	for _, c := range cats {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}

// CategoriesIntersect reports whether cats intersects want,
// case-insensitively; "*" in want matches everything.
func CategoriesIntersect(cats, want []string) bool {
	for _, w := range want {
		if w == "*" || ContainsCategory(cats, w) {
			return true
		}
	}
	return false
}
