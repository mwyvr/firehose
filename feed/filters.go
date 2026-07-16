package feed

import (
	"strings"

	"github.com/mwyvr/firehose"
)

// skipByFilters drops an item per the feed's filters: any exclude match
// (text or URL) drops; if include rules exist, any match (text or URL)
// keeps.
func skipByFilters(fd *firehose.Feed, title, bodyHTML, summaryHTML, link string) bool {
	hay := strings.ToLower(title + " " + textContent(bodyHTML) + " " + textContent(summaryHTML))
	for _, kw := range fd.Exclude {
		if kw != "" && strings.Contains(hay, strings.ToLower(kw)) {
			return true
		}
	}
	l := strings.ToLower(link)
	for _, kw := range fd.ExcludeURL {
		if kw != "" && strings.Contains(l, strings.ToLower(kw)) {
			return true
		}
	}

	if len(fd.Include) == 0 && len(fd.IncludeURL) == 0 {
		return false
	}
	for _, kw := range fd.Include {
		if kw != "" && strings.Contains(hay, strings.ToLower(kw)) {
			return false
		}
	}
	if link != "" {
		for _, kw := range fd.IncludeURL {
			if kw != "" && strings.Contains(l, strings.ToLower(kw)) {
				return false
			}
		}
	}
	return true
}
