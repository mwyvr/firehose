package render

import (
	"net/url"
	"sort"
	"strings"

	"github.com/mwyvr/firehose"
)

// Cross-feed dedupe: the same story arriving via multiple feeds (BC Gov
// repeats releases across every ministry feed; CBC's sections overlap)
// renders once per page. Identity is the CANONICAL item URL — exact match
// after tracker-stripping, never fuzzy similarity: "same coverage by two
// outlets" is clustering, permanently out of scope. Items without a URL are
// never deduped (no identity).

// trackerParams are query parameters that identify the click, not the
// content. Matched case-insensitively; utm_* matches by prefix.
var trackerParams = map[string]bool{
	"cmp": true, "cmpid": true, "fbclid": true, "gclid": true,
	"igshid": true, "mc_cid": true, "mc_eid": true,
}

// canonicalItemURL normalizes an item URL to its content identity:
// lowercased scheme and host, fragment dropped, tracker params removed with
// the remaining query preserved in original order. Unparseable input is
// returned as-is (identity = the raw string). Deliberately conservative:
// trailing slashes and http/https are preserved — a false merge is worse
// than a missed one.
func canonicalItemURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""

	if u.RawQuery != "" {
		var kept []string
		for _, pair := range strings.Split(u.RawQuery, "&") {
			name := pair
			if i := strings.Index(pair, "="); i >= 0 {
				name = pair[:i]
			}
			name = strings.ToLower(name)
			if strings.HasPrefix(name, "utm_") || trackerParams[name] {
				continue
			}
			kept = append(kept, pair)
		}
		u.RawQuery = strings.Join(kept, "&")
	}
	return u.String()
}

// dedupeItems collapses duplicates in a newest-first item slice. The winner
// per canonical URL: full content beats a teaser, then earliest published
// (the origin over the echo), then config feed order. Returns the kept
// items in their original river order and, per kept item, the OTHER source
// titles ("also via"), in config feed order.
func dedupeItems(items []*firehose.Item, meta map[int64]feedMeta, feedOrder map[int64]int) ([]*firehose.Item, map[*firehose.Item][]string) {
	groups := map[string][]*firehose.Item{}
	for _, it := range items {
		if it.URL == "" {
			continue
		}
		key := canonicalItemURL(it.URL)
		groups[key] = append(groups[key], it)
	}

	winner := map[*firehose.Item]bool{}
	alsoVia := map[*firehose.Item][]string{}
	for _, group := range groups {
		if len(group) == 1 {
			winner[group[0]] = true
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			a, b := group[i], group[j]
			if a.FullContent != b.FullContent {
				return a.FullContent
			}
			if !a.Published.Equal(b.Published) {
				return a.Published.Before(b.Published)
			}
			return feedOrder[a.FeedID] < feedOrder[b.FeedID]
		})
		w := group[0]
		winner[w] = true

		seen := map[string]bool{meta[w.FeedID].title: true}
		var others []*firehose.Item
		others = append(others, group[1:]...)
		sort.SliceStable(others, func(i, j int) bool {
			return feedOrder[others[i].FeedID] < feedOrder[others[j].FeedID]
		})
		for _, o := range others {
			t := meta[o.FeedID].title
			if t != "" && !seen[t] {
				seen[t] = true
				alsoVia[w] = append(alsoVia[w], t)
			}
		}
	}

	kept := items[:0]
	for _, it := range items {
		if it.URL == "" || winner[it] {
			kept = append(kept, it)
		}
	}
	return kept, alsoVia
}
