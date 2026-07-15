package firehose

import "strings"

// Local feeds: a feed URL of the form file:///abs/path
const localFeedScheme = "file://"

// IsLocalFeed reports whether url names a local feed document.
func IsLocalFeed(url string) bool { return strings.HasPrefix(url, localFeedScheme) }

// LocalFeedPath returns the filesystem path of a local feed URL.
func LocalFeedPath(url string) string { return strings.TrimPrefix(url, localFeedScheme) }
