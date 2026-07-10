package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// registryAPIBase is where ubx fetches provider release metadata and
// binaries from. Deliberately NOT registry.terraform.io — see
// docs/architecture.md's provider acquisition section for the rationale
// (ToS, and OpenTofu's registry mirrors the same provider set via the same
// protocol). Provider source *identity* (Source, above) stays the
// Terraform-canonical hostname regardless; this is purely about which
// server ubx's own HTTP requests go to.
const registryAPIBase = "https://registry.opentofu.org/v1/providers"

// registryPackageMetadata is the subset of the provider registry protocol's
// "find a package" response ubx uses. Verified live against
// registry.opentofu.org (GET
// /v1/providers/hashicorp/aws/6.54.0/download/darwin/arm64) rather than
// assumed from memory.
type registryPackageMetadata struct {
	Filename            string              `json:"filename"`
	DownloadURL         string              `json:"download_url"`
	ShasumsURL          string              `json:"shasums_url"`
	ShasumsSignatureURL string              `json:"shasums_signature_url"`
	Shasum              string              `json:"shasum"`
	SigningKeys         registrySigningKeys `json:"signing_keys"`
}

type registrySigningKeys struct {
	GPGPublicKeys []registryGPGPublicKey `json:"gpg_public_keys"`
}

type registryGPGPublicKey struct {
	KeyID      string `json:"key_id"`
	ASCIIArmor string `json:"ascii_armor"`
}

// fetchPackageMetadata queries the registry for one provider release's
// download info for a specific platform.
func fetchPackageMetadata(ctx context.Context, httpClient *http.Client, apiBase string, src Source, version, goos, goarch string) (*registryPackageMetadata, error) {
	url := fmt.Sprintf("%s/%s/%s/%s/download/%s/%s", apiBase, src.Namespace, src.Type, version, goos, goarch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("registry: build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry: request %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s %s for %s/%s", ErrPlatformNotFound, src, version, goos, goarch)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("registry: %s: unexpected status %d: %s", url, resp.StatusCode, body)
	}

	var meta registryPackageMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("registry: %s: decode response: %w", url, err)
	}
	return &meta, nil
}

// httpGetBytes downloads a URL's full body. Shared by the zip/SHA256SUMS/
// signature fetches, which are all "just download this whole file" with no
// further parsing at this layer.
func httpGetBytes(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
