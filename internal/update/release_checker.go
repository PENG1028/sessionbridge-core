package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// GitHubRelease is a partial GitHub Releases API response.
type GitHubRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// ReleaseChecker checks for updates via GitHub Releases API.
type ReleaseChecker struct {
	Repo       string // "PENG1028/sessionbridge-core"
	CurrentTag string // "v0.8.0"
	HTTPClient *http.Client
	OS         string // runtime.GOOS, overridable for tests
	Arch       string // runtime.GOARCH, overridable for tests
}

// NewReleaseChecker creates a checker with defaults.
func NewReleaseChecker(repo, currentTag string) *ReleaseChecker {
	return &ReleaseChecker{
		Repo:       repo,
		CurrentTag: currentTag,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
	}
}

// CheckLatest queries the GitHub API for the latest release.
// Returns (hasUpdate, latestTag, downloadURL, error).
func (c *ReleaseChecker) CheckLatest() (bool, string, string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.Repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, "", "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sessionbridge-core/1.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, "", "", fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 {
		return false, "", "", fmt.Errorf("github api rate limited (HTTP 403)")
	}
	if resp.StatusCode != 200 {
		return false, "", "", fmt.Errorf("github api: HTTP %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return false, "", "", fmt.Errorf("github api decode: %w", err)
	}

	// Skip drafts
	if release.Draft {
		return false, "", "", nil
	}

	// Compare versions
	if release.TagName == "" || release.TagName == c.CurrentTag {
		return false, "", "", nil
	}

	// Find asset for current platform
	assetName := releaseAssetName(release.TagName, c.OS, c.Arch)
	for _, a := range release.Assets {
		if a.Name == assetName {
			return true, release.TagName, a.BrowserDownloadURL, nil
		}
	}

	return true, release.TagName, "", fmt.Errorf("no asset found for %s/%s (expected: %s)", c.OS, c.Arch, assetName)
}

// releaseAssetName builds the platform-specific asset filename matching goreleaser naming.
func releaseAssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("sessionbridge-core_%s_%s_%s.tar.gz",
		trimV(tag), goos, goarch)
}

func trimV(tag string) string {
	if len(tag) > 0 && tag[0] == 'v' {
		return tag[1:]
	}
	return tag
}
