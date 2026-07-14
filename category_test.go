package firehose

import "testing"

func TestCategoryMatchingIsCaseInsensitive(t *testing.T) {
	cats := []string{"Tech", "BC"}
	for _, want := range []string{"tech", "TECH", "Tech", "bc"} {
		if !ContainsCategory(cats, want) {
			t.Errorf("ContainsCategory(%v, %q) = false", cats, want)
		}
	}
	if ContainsCategory(cats, "technology") {
		t.Error("substring must not match")
	}
	if !CategoriesIntersect([]string{"News"}, []string{"gov", "news"}) {
		t.Error("fold intersect failed")
	}
	if !CategoriesIntersect([]string{"anything"}, []string{"*"}) {
		t.Error("star must match everything")
	}
	if CategoriesIntersect([]string{"tech"}, []string{"gov"}) {
		t.Error("disjoint sets must not intersect")
	}
}
