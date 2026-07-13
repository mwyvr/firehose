package htmlx

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestCanonicalVoids(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(
		`<p>a<br>b<img src="x.png" alt="a>b"><hr></p>`))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		t.Fatal(err)
	}
	out := Rewrite(buf.String())
	for _, bad := range []string{"<br/>", "<hr/>", "/>"} {
		if strings.Contains(out, bad) {
			t.Errorf("self-closing survived: %q in %s", bad, out)
		}
	}
	for _, want := range []string{"<br>", "<hr>", `alt="a&gt;b"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
