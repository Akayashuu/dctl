package dctl

import "net/url"

// seg escapes a caller-supplied value for safe interpolation into a single URL
// path segment, preventing extra path/query smuggling against the API base.
func seg(s string) string { return url.PathEscape(s) }
