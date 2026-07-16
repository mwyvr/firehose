package feed

import (
	"strings"
	"testing"
)

func TestSanitizeCanonicalVoids(t *testing.T) {
	clean, _ := sanitize(`<p>a<br/>b<br>c</p><hr/>`, "https://x.example/", nil)
	if strings.Contains(clean, "/>") {
		t.Fatalf("self-closing void survived: %s", clean)
	}
	if !strings.Contains(clean, "<br>") {
		t.Fatalf("br lost: %s", clean)
	}
}
