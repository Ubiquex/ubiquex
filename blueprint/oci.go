// oci.go is UBI-74 Slice 7's own OCI/GHCR push+pull -- the founder's own
// design (Linear comment 2026-08-04): reuse the ORAS pattern already
// proven in production for Helm charts/WASM modules/SBOMs/Cosign
// signatures, one OCI manifest wrapping Slice 3's own tarball as its one
// content-addressed blob layer, so ANY existing OCI registry (GHCR first)
// becomes a working blueprint-distribution backend with zero new server
// code. Real library, confirmed against its own current API (not
// memory) before writing anything here: oras.land/oras-go/v2 v2.6.2, the
// actively maintained ORAS Go SDK -- oras-go v1 exists too but is legacy;
// v2 is what every real ORAS integration (including the `oras` CLI
// itself) uses today.
package blueprint

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

const (
	// ociBlobMediaType is the ORAS artifact's own one blob layer's media
	// type -- Slice 3's own tarball, pushed AS-IS, never re-encoded or
	// re-wrapped, matching the founder's own literal example verbatim.
	ociBlobMediaType = "application/vnd.ubx.blueprint.v1.tar+gzip"

	// ociArtifactType names the MANIFEST's own OCI 1.1 artifactType --
	// there's no separate config blob (PackManifestOptions.
	// ConfigDescriptor left nil), a deliberately minimal single-blob
	// artifact, the same shape Helm/SBOM/Cosign OCI artifacts already
	// use.
	ociArtifactType = "application/vnd.ubx.blueprint.v1"

	// annoContentHash/annoBlueprintName record blueprint.lock.json's own
	// content_hash/name (already carried INSIDE the pushed tarball) as
	// OCI manifest annotations too -- pure visibility, not a second
	// verification mechanism: a `docker manifest inspect`/`oras manifest
	// fetch` can cross-check the content hash without pulling+extracting
	// first. This is deliberately NOT a second, competing hash scheme --
	// OCI's own blob digest (computed natively by oras-go from the
	// tarball's real bytes, verified by the registry on every push/pull)
	// is a completely separate, orthogonal TRANSPORT-integrity check;
	// content_hash stays exactly the same core.CanonicalJSON-based
	// APPLICATION-level hash Verify already checks, after extraction,
	// unchanged by this slice.
	annoContentHash   = "dev.ubiquex.blueprint.content_hash"
	annoBlueprintName = "dev.ubiquex.blueprint.name"

	// ociAnnotationEpoch is the fixed org.opencontainers.image.created
	// value every push uses -- CLAUDE.md's "determinism is a feature"
	// rule, extended to the OCI layer the same way writeTarGz (package.go)
	// already zeroes every tar header's own ModTime: pushing the
	// identical blueprint content twice produces a byte-identical OCI
	// manifest, not just an identical content_hash.
	ociAnnotationEpoch = "1970-01-01T00:00:00Z"
)

// stripOCIScheme strips ociRef's required "oci://" prefix, returning the
// bare "registry/repo:tag" form oras-go's own registry.Reference parsing
// expects (it has no notion of an "oci://" scheme itself -- that's this
// project's own CLI-facing convention, matching the founder's own literal
// examples in the Linear design comment and this slice's own success
// bar).
func stripOCIScheme(ociRef string) (string, error) {
	trimmed := strings.TrimPrefix(ociRef, "oci://")
	if trimmed == ociRef {
		return "", fmt.Errorf("not a valid oci:// reference (must start with \"oci://\"): %q", ociRef)
	}
	if trimmed == "" {
		return "", fmt.Errorf("oci:// reference has nothing after the scheme: %q", ociRef)
	}
	return trimmed, nil
}

// newAuthenticatedRepository builds a real remote.Repository for
// trimmedRef (already scheme-stripped), authenticated via the SAME
// credentials a real `docker login`/`oras login` already established --
// oras-go's own credentials.NewStoreFromDocker reads the real
// $DOCKER_CONFIG (or ~/.docker/config.json) and, via its own credsStore/
// credHelpers handling, talks to the real OS credential helper (e.g.
// Docker Desktop's own keychain-backed helper) exactly like the `docker`
// CLI itself does. Deliberately NOT a separate `ubx`-specific login
// mechanism -- reusing the founder's own already-verified real login is
// simpler and avoids a second credential store to keep in sync.
func newAuthenticatedRepository(trimmedRef string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(trimmedRef)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", trimmedRef, err)
	}
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("read docker credential store: %w", err)
	}
	repo.Client = &auth.Client{
		Cache:      auth.NewCache(),
		Credential: credentials.Credential(store),
	}
	return repo, nil
}

// Push uploads tarballPath -- Slice 3's own `ubx blueprint package`
// output, unmodified -- to ociRef ("oci://registry/repo:tag") as a real
// OCI artifact: one manifest, the tarball as its one blob layer. Returns
// the blueprint's own Manifest (read back out of the tarball itself, via
// manifestFromTarball below) so a caller can report its content hash --
// pushing a directory that was never `package`d first (no
// blueprint.lock.json inside the tarball) is a clear, named error, never
// a silent push of un-verifiable content.
func Push(ctx context.Context, tarballPath, ociRef string) (*Manifest, error) {
	m, err := manifestFromTarball(tarballPath)
	if err != nil {
		return nil, fmt.Errorf("blueprint push: %w", err)
	}

	trimmed, err := stripOCIScheme(ociRef)
	if err != nil {
		return nil, fmt.Errorf("blueprint push: %w", err)
	}
	repo, err := newAuthenticatedRepository(trimmed)
	if err != nil {
		return nil, fmt.Errorf("blueprint push: %w", err)
	}
	if repo.Reference.Reference == "" {
		return nil, fmt.Errorf("blueprint push: %s must include a tag (e.g. oci://ghcr.io/org/name:v1)", ociRef)
	}

	if err := pushToTarget(ctx, tarballPath, m, repo, repo.Reference.Reference); err != nil {
		return nil, fmt.Errorf("blueprint push: %w", err)
	}
	return m, nil
}

// pushToTarget is Push's own target-agnostic half: builds the local
// file.Store + OCI manifest and copies both to target -- a real
// remote.Repository in production, any other real oras.Target (a local
// on-disk oci.Store, say) in a hermetic test. Splitting this out means
// the actual ORAS manifest-construction mechanics (fs.Add/
// oras.PackManifest/fs.Tag/oras.Copy) get tested against oras-go's own
// real local target implementations, never a hand-rolled fake standing
// in for them.
func pushToTarget(ctx context.Context, tarballPath string, m *Manifest, target oras.Target, tag string) error {
	fs, err := file.New(filepath.Dir(tarballPath))
	if err != nil {
		return fmt.Errorf("open %s as a file store: %w", filepath.Dir(tarballPath), err)
	}
	defer fs.Close()

	fileDesc, err := fs.Add(ctx, filepath.Base(tarballPath), ociBlobMediaType, tarballPath)
	if err != nil {
		return fmt.Errorf("add %s as a blob: %w", tarballPath, err)
	}

	manifestDesc, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, ociArtifactType, oras.PackManifestOptions{
		Layers: []ocispec.Descriptor{fileDesc},
		ManifestAnnotations: map[string]string{
			ocispec.AnnotationCreated: ociAnnotationEpoch,
			annoContentHash:           m.ContentHash,
			annoBlueprintName:         m.Name,
		},
	})
	if err != nil {
		return fmt.Errorf("pack OCI manifest: %w", err)
	}

	if err := fs.Tag(ctx, manifestDesc, tag); err != nil {
		return fmt.Errorf("tag manifest %s: %w", tag, err)
	}

	if _, err := oras.Copy(ctx, fs, tag, target, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy %s to target: %w", tag, err)
	}
	return nil
}

// pullOCI is Pull's own oci:// branch (pull.go) -- Push's converse.
// Downloads ociRef's real OCI artifact and extracts its one tarball blob
// into dest, leaving dest holding an ordinary local blueprint directory
// (blueprint.lock.json included) indistinguishable afterward from Pull's
// own local/git branches' own output -- `ubx blueprint verify` needs zero
// awareness of which of the three source types actually produced it.
func pullOCI(ctx context.Context, ociRef, dest string) error {
	trimmed, err := stripOCIScheme(ociRef)
	if err != nil {
		return err
	}
	repo, err := newAuthenticatedRepository(trimmed)
	if err != nil {
		return err
	}
	if repo.Reference.Reference == "" {
		return fmt.Errorf("%s must include a tag (e.g. oci://ghcr.io/org/name:v1)", ociRef)
	}
	if err := pullFromTarget(ctx, repo, repo.Reference.Reference, dest); err != nil {
		return fmt.Errorf("%s: %w", ociRef, err)
	}
	return nil
}

// pullFromTarget is pullOCI's own target-agnostic half, mirroring
// pushToTarget's own split for the identical hermetic-testing reason:
// copies tag's own manifest+blob from target into a throwaway local
// file.Store, then extracts the one blob it finds there (the tarball) into
// dest via extractTarGz.
func pullFromTarget(ctx context.Context, target oras.ReadOnlyTarget, tag, dest string) error {
	fetchDir, err := os.MkdirTemp("", "ubx-blueprint-oci-pull-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(fetchDir)

	fs, err := file.New(fetchDir)
	if err != nil {
		return err
	}
	defer fs.Close()

	if _, err := oras.Copy(ctx, target, tag, fs, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy from target: %w", err)
	}

	tarPath, err := soleFile(fetchDir)
	if err != nil {
		return err
	}
	if err := extractTarGz(tarPath, dest); err != nil {
		return fmt.Errorf("extract %s: %w", filepath.Base(tarPath), err)
	}
	return nil
}

// soleFile returns the one regular file fetchDir contains -- Push always
// adds exactly one blob (the tarball), so a real blueprint artifact's own
// pull always leaves exactly one file behind. More or fewer than one is a
// clear, named error (not a blueprint artifact this build knows how to
// pull) rather than a silent guess at which file to extract.
func soleFile(fetchDir string) (string, error) {
	entries, err := os.ReadDir(fetchDir)
	if err != nil {
		return "", err
	}
	var found string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("expected exactly one blob in the pulled artifact, found more than one (%s, %s) -- not a blueprint artifact this build knows how to pull", filepath.Base(found), e.Name())
		}
		found = filepath.Join(fetchDir, e.Name())
	}
	if found == "" {
		return "", fmt.Errorf("the pulled artifact has no blob layer -- not a blueprint artifact this build knows how to pull")
	}
	return found, nil
}

// manifestFromTarball extracts tarballPath into a throwaway temp
// directory and reads its blueprint.lock.json back out (readManifest,
// manifest.go) -- Push's own way of reporting a real content hash without
// requiring the caller to pass the source directory separately (the CLI
// success bar names pushing a TARBALL, `ubx blueprint push <tarball>
// --to ...`, matching Package's own tarball output directly, never a
// directory).
func manifestFromTarball(tarballPath string) (*Manifest, error) {
	tmp, err := os.MkdirTemp("", "ubx-blueprint-push-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(tarballPath, tmp); err != nil {
		return nil, fmt.Errorf("%s doesn't look like a real gzipped tarball: %w", tarballPath, err)
	}
	m, err := readManifest(tmp)
	if err != nil {
		return nil, fmt.Errorf("%s has no %s -- push a tarball `ubx blueprint package` produced: %w", tarballPath, ManifestFileName, err)
	}
	return m, nil
}

// extractTarGz is writeTarGz's own converse (package.go) -- Slice 7's own
// real new need, since nothing before this slice ever had to read a
// tarball back (Pull's local/git branches never produce one; a bare
// standalone tarball pull is explicitly Slice 8's own scope, untouched
// here). destDir must already exist. Guards against a tar entry naming a
// path that escapes destDir (a malicious or corrupted tarball) -- this
// runs BEFORE Verify's own content-hash check ever gets a chance to
// reject tampered content, so extraction itself can't yet trust the
// tarball's own filenames.
func extractTarGz(tarGzPath, destDir string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a valid gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(destDir)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			// writeTarGz never produces anything but TypeReg entries
			// (subdirectories are implicit in each entry's own slash-
			// separated Name, never a separate TypeDir header) -- a
			// foreign/tampered tarball might, skipped rather than
			// failing the whole extraction over one unexpected entry;
			// Verify's own hash check catches a meaningfully different
			// result afterward regardless.
			continue
		}
		target := filepath.Join(cleanDest, hdr.Name)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes the destination directory", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}
