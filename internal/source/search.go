package source

import (
	"sort"
	"strconv"
	"strings"
)

// SearchOptions carries the result-shaping inputs of a search, independent of
// provider protocol and cache policy.
type SearchOptions struct {
	Page      int
	Languages []string
	Formats   []string
}

// Key renders a stable identity: page, then languages, then formats. The two
// slices are sets, so their order and duplicates do not change the key. Values
// are not normalised. Sets are joined with \x01, sections with \x00, so no
// value can slide across a section boundary.
func (o SearchOptions) Key() string {
	return strconv.Itoa(o.Page) + "\x00" + setKey(o.Languages) + "\x00" + setKey(o.Formats)
}

func setKey(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	unique := sorted[:0]
	for _, value := range sorted {
		if len(unique) == 0 || value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	return strings.Join(unique, "\x01")
}
