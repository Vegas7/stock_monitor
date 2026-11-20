// Package lifecycle updates and uninstalls stockmon.
package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Vegas7/stock_monitor/internal/version"
)

// DefaultAPIURL is the GitHub "latest release" endpoint for this project.
const DefaultAPIURL = "https://api.github.com/repos/Vegas7/stock_monitor/releases/latest"

// Release is a published release: tag and asset name → download URL.
type Release struct {
	Tag    string
	Assets map[string]string
}

// Latest fetches the newest release.
func Latest(ctx context.Context, hc *http.Client, apiURL string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "stockmon")
	resp, err := hc.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("release lookup: HTTP %d", resp.StatusCode)
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("release lookup: %w", err)
	}
	r := Release{Tag: raw.TagName, Assets: map[string]string{}}
	for _, a := range raw.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// Newer reports whether latest is newer than current.
func Newer(current, latest string) bool { return version.Newer(current, latest) }
