// Package instanceid resolves and validates the per-daemon instance identifier
// used to namespace global resources (git branches, worktree paths, Discord
// titles) so multiple dctl daemons can share one Discord home.
package instanceid

import "regexp"

// idRe is the strict slug accepted as an instanceID: lowercase alnum start,
// then lowercase alnum or '-', total length 1..16. No '_' (so the '__'
// title separator can never appear inside an id), no '/' and no '.'.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

// Validate reports whether id is a well-formed instanceID slug.
func Validate(id string) bool {
	return idRe.MatchString(id)
}

// Slugify derives a short instanceID from a Discord owner snowflake. It returns
// "u" + the last up-to-8 characters of owner, which keeps the result <=9 chars
// and within the validation regex. An empty owner yields an empty string.
func Slugify(owner string) string {
	if owner == "" {
		return ""
	}
	if len(owner) > 8 {
		owner = owner[len(owner)-8:]
	}
	return "u" + owner
}
