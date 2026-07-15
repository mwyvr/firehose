package firehose

import "testing"

func TestRewriteHost(t *testing.T) {
	rules := map[string]string{"wrong.example": "right.example"}
	cases := []struct{ name, in, want string }{
		{"exact host", "https://wrong.example/news/a/", "https://right.example/news/a/"},
		{"subdomain via apex key", "https://www.wrong.example/news/a/", "https://right.example/news/a/"},
		{"case-insensitive", "https://WWW.Wrong.Example/a", "https://right.example/a"},
		{"path and query preserved", "https://wrong.example/a?p=1&q=2", "https://right.example/a?p=1&q=2"},
		{"scheme preserved", "http://wrong.example/a", "http://right.example/a"},
		{"unrelated host untouched", "https://other.example/a", "https://other.example/a"},
		{"suffix-similar host untouched", "https://notwrong.example/a", "https://notwrong.example/a"},
		{"opaque guid untouched", "urn:uuid:1234-abcd", "urn:uuid:1234-abcd"},
		{"empty untouched", "", ""},
	}
	for _, tc := range cases {
		if got := RewriteHost(tc.in, rules); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
	if got := RewriteHost("https://wrong.example/a", nil); got != "https://wrong.example/a" {
		t.Errorf("nil rules must be identity: %q", got)
	}
}
