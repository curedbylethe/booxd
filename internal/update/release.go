package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/curedbylethe/booxd/internal/fetch"
)

// latestReleaseURL is a variable so tests can point it at a local server.
var latestReleaseURL = "https://api.github.com/repos/curedbylethe/booxd/releases/latest"

// latestTag returns the tag_name of the newest GitHub release.
func latestTag(ctx context.Context, client *fetch.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	resp, err := client.Get(ctx, latestReleaseURL)
	if err != nil {
		return "", fmt.Errorf("update: fetch latest release: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("update: latest release returned status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(resp.Body, &release); err != nil {
		return "", fmt.Errorf("update: decode latest release: %w", err)
	}
	if release.TagName == "" {
		return "", errors.New("update: latest release has no tag_name")
	}
	return release.TagName, nil
}
