package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// githubAPIBase is GitHub's own real REST API root. Unauthenticated
// (public-repo releases only, matching registry.opentofu.org's own
// unauthenticated pattern already used in registry.go) -- no new
// infrastructure, no token to provision or rotate.
const githubAPIBase = "https://api.github.com"

// githubRelease is the subset of GitHub's own real "get a release by tag"
// response AcquireSchema uses.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// asset returns the release asset named name, or (nil, false).
func (r *githubRelease) asset(name string) (githubAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return githubAsset{}, false
}

// fetchRelease queries apiBase for owner/repo's real release tagged tag
// (GitHub's own real convention: a release's tag is what's requested, not
// a release ID -- "v1.2.0" for schema snapshot version "1.2.0", the same
// "v"-prefixed tag convention the SDK bindings repos' own
// version-watch.yml already uses).
func fetchRelease(ctx context.Context, httpClient *http.Client, apiBase, owner, repo, tag string) (*githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBase, owner, repo, tag)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s/%s tag %s", ErrSchemaNotFound, owner, repo, tag)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: %s: unexpected status %d: %s", url, resp.StatusCode, body)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github: %s: decode response: %w", url, err)
	}
	return &rel, nil
}

// listReleases returns every published release for owner/repo, newest
// first (GitHub's own ordering), paging once at the API's maximum page
// size. One page of 100 is deliberate rather than a full pager: this is
// used to find the newest binary compatible with a snapshot's own stamped
// floor, and a floor more than 100 releases behind the head is a far
// bigger problem than picking the wrong one of them.
func listReleases(ctx context.Context, httpClient *http.Client, apiBase, owner, repo string) ([]githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", apiBase, owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: %s: unexpected status %d: %s", url, resp.StatusCode, body)
	}

	var out []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("github: decode %s: %w", url, err)
	}
	return out, nil
}
