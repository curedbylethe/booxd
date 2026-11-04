package mirror

import (
	_ "embed"
	"strings"
)

//go:embed seeds.txt
var seedsText string

// Seeds returns the embedded mirror base URLs in probe order.
func Seeds() []string {
	var seeds []string
	for line := range strings.SplitSeq(seedsText, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			seeds = append(seeds, line)
		}
	}
	return seeds
}
