package provider

import (
	"os"
	"path/filepath"
)

// providerMirrorEnv, if set, points at a local directory to check for a
// provider binary before ever touching the network — preserves the manual
// "download it yourself, point --provider at it" workflow used in earlier
// sessions, just organized under one directory instead of ad hoc paths
// scattered across a scratch dir. A miss here is not an error: Acquire
// falls through to the normal cache-then-network path.
const providerMirrorEnv = "UBX_PROVIDER_MIRROR"

// platformDir is the "<namespace>/<type>/<version>/<os_arch>/" suffix
// shared by both the local mirror and ubx's own verified-binary cache: one
// directory expected to hold exactly one file, the ready-to-run provider
// binary (already extracted — not a zip). Neither the mirror nor the
// cache need to agree on Terraform's own upstream filename convention;
// "whatever the one file in this directory is" is simpler for a user to
// hand-populate and for ubx to write to.
func platformDir(root string, src Source, version, goos, goarch string) string {
	return filepath.Join(root, src.Namespace, src.Type, version, goos+"_"+goarch)
}

// findSingleFile returns the path to the one regular file in dir, or "" if
// dir doesn't exist, is empty, or (deliberately conservative) contains
// anything other than exactly one regular file — an ambiguous directory
// is treated as a miss, not guessed at.
func findSingleFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var found string
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		found = filepath.Join(dir, e.Name())
		count++
	}
	if count != 1 {
		return ""
	}
	return found
}

// lookupMirror returns the path to a provider binary already present in
// the local mirror directory named by UBX_PROVIDER_MIRROR, or "" if the
// env var isn't set or nothing usable is there.
func lookupMirror(src Source, version, goos, goarch string) string {
	mirrorDir := os.Getenv(providerMirrorEnv)
	if mirrorDir == "" {
		return ""
	}
	return findSingleFile(platformDir(mirrorDir, src, version, goos, goarch))
}

// defaultCacheRoot is ~/.ubx/providers — the on-disk cache for verified
// provider binaries.
func defaultCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "providers"), nil
}

// cacheDir is <cacheRoot>/<hostname>/<namespace>/<type>/<version>/<os_arch>/
// — mirrors Terraform's own plugin cache layout convention (hostname
// included, unlike the mirror dir, since the cache is keyed by the full
// canonical source identity; a local mirror a user hand-populates has no
// need to disambiguate hostnames).
func cacheDir(cacheRoot string, src Source, version, goos, goarch string) string {
	return filepath.Join(cacheRoot, src.Hostname, platformDirRel(src, version, goos, goarch))
}

func platformDirRel(src Source, version, goos, goarch string) string {
	return filepath.Join(src.Namespace, src.Type, version, goos+"_"+goarch)
}
