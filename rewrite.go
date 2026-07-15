package firehose

import (
	"net/url"
	"strings"
)

// Per-feed host rewriting: some syndicated CMSes emit another site's
// hostname in item links and GUIDs (a local outlet's feed carrying the
// parent network's domain), leaving every link a 404. The fix is
// declarative, per feed:
//
//	rewrite_host = { "wrong.example" = "right.example" }
//
// A key matches itself and its subdomains (the apex form covers www);
// matching is case-insensitive; only the host component is replaced —
// scheme, path, and query pass through untouched. Values are bare
// hostnames (validated). Strings that don't parse as URLs with a host
// (opaque GUIDs) are returned unchanged.

// RewriteHost applies host-rewrite rules to a URL string. Unmatched or
// unparseable input is returned as-is.
func RewriteHost(raw string, rules map[string]string) string {
	if len(rules) == 0 || raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	for from, to := range rules {
		from = strings.ToLower(from)
		if host == from || strings.HasSuffix(host, "."+from) {
			if p := u.Port(); p != "" {
				u.Host = to + ":" + p
			} else {
				u.Host = to
			}
			return u.String()
		}
	}
	return raw
}
