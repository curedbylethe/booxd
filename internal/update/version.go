package update

import (
	"strconv"
	"strings"
)

// IsRelease reports whether v names a real release version: a leading "v" and
// three numeric components. A local build reports "dev" and is not a release.
func IsRelease(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return false
	}
	_, ok := parseVersion(v)
	return ok
}

// Newer reports whether latest is strictly newer than current. It is false when
// either side does not parse.
//
// shortcut: prerelease and build suffixes are ignored, so v1.2.3-rc1 is not
// newer than v1.2.3; upgrade to a full semver comparison when a prerelease
// channel ships.
func Newer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	for i := range c {
		if c[i] != l[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseVersion reads a leading "v" and three dot-separated integers, ignoring
// any "-" or "+" suffix.
func parseVersion(v string) ([3]int, bool) {
	var parts [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	if len(fields) != len(parts) {
		return parts, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
