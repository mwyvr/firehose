package firehose

import (
	"net/url"
	"strings"
)

// Per-feed host rewriting for feeds that emit another site's hostname in
// item links and GUIDs. A key matches itself and its subdomains; matching
// is case-insensitive; only the host component is replaced. Strings that
// don't parse as URLs with a host are returned unchanged.

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
