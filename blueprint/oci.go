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
func Push(ctx context.Context, tarballPath, ociRef string, opts ...TransferOption) (*Manifest, error) {
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

	if err := pushToTarget(ctx, tarballPath, m, repo, repo.Reference.Reference, opts...); err != nil {
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
func pushToTarget(ctx context.Context, tarballPath string, m *Manifest, target oras.Target, tag string, opts ...TransferOption) error {
	// Absolute before anything else, because a relative path would
	// otherwise have its directory component applied TWICE.
	//
	// file.New roots the store at a directory, and file.Store.Add
	// resolves a relative path argument against that same root
	// (its own absPath). Passing Dir(p) to one and p to the other means
	// the directory is consumed once by each: from a/b, pushing
	// "../x.tar.gz" built a store at "a" and then looked for
	// "a/../x.tar.gz", one level above the file.
	//
	// It only ever appeared to work by coincidence, and the coincidence
	// is narrow. A bare filename has no directory component to double.
	// "../sib/x" doubles to "../sib/../sib/x", which path cleaning
	// collapses back to the right answer. Nothing else survives:
	// "sub/x" doubles to "sub/sub/x", "../x" to "../../x", and
	// "../../sub/x" cleans to "../../../sub/x", each landing somewhere
	// the file is not.
	//
	// An absolute path is returned by absPath unchanged, so making it
	// absolute here removes the ambiguity rather than compensating for
	// it.
	absTarball, err := filepath.Abs(tarballPath)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", tarballPath, err)
	}

	fs, err := file.New(filepath.Dir(absTarball))
	if err != nil {
		return fmt.Errorf("open %s as a file store: %w", filepath.Dir(absTarball), err)
	}
	defer fs.Close()

	// The bytes leave through the SOURCE store's Fetch, so that is what
	// gets counted (transfer.go). The remote repository is never
	// wrapped, deliberately.
	//
	// The expected total is declared by the same wrapper that counts the
	// bytes (countingStore.expect), so the two cannot disagree about
	// what is being measured.
	o := applyTransferOptions(opts)
	var src oras.Target = fs
	if o.onProgress != nil {
		src = &countingStore{inner: fs, counter: newByteCounter(0, o.onProgress), countFetch: true}
	}

	fileDesc, err := fs.Add(ctx, filepath.Base(absTarball), ociBlobMediaType, absTarball)
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

	if _, err := oras.Copy(ctx, src, tag, target, tag, oras.DefaultCopyOptions); err != nil {
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
func pullOCI(ctx context.Context, ociRef, dest string, opts ...TransferOption) error {
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
	if err := pullFromTarget(ctx, repo, repo.Reference.Reference, dest, opts...); err != nil {
		return fmt.Errorf("%s: %w", ociRef, err)
	}
	return nil
}

// pullFromTarget is pullOCI's own target-agnostic half, mirroring
// pushToTarget's own split for the identical hermetic-testing reason:
// copies tag's own manifest+blob from target into a throwaway local
// file.Store, then extracts the one blob it finds there (the tarball) into
// dest via extractTarGz.
func pullFromTarget(ctx context.Context, target oras.ReadOnlyTarget, tag, dest string, opts ...TransferOption) error {
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

	// The bytes arrive through the DESTINATION store's Push, so that is
	// what gets counted (transfer.go). The remote repository, which is
	// the source here, is never wrapped.
	//
	// The total is not known when the copy starts: a pull learns the
	// layer's size from the manifest, which is itself the first thing
	// fetched. It is declared by the same wrapper that counts the bytes
	// (countingStore.expect), so the two cannot disagree about what is
	// being measured.
	o := applyTransferOptions(opts)
	var dst oras.Target = fs
	if o.onProgress != nil {
		dst = &countingStore{inner: fs, counter: newByteCounter(0, o.onProgress), countPush: true}
	}

	if _, err := oras.Copy(ctx, target, tag, dst, tag, oras.DefaultCopyOptions); err != nil {
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

	// ABSOLUTE, not filepath.Clean. The containment test below compares
	// the destination against each entry's resolved path, and a
	// comparison between two paths only means anything if neither can be
	// rewritten into a different spelling of itself.
	//
	// Clean(".") is ".", and filepath.Join(".", "x") is "x": Join cleans
	// its result, which drops the "." entirely. So for a destination of
	// "." the destination stopped being a textual prefix of its own
	// entries, and every legitimate file was reported as escaping. Same
	// for "" and "./", which also clean to ".".
	//
	// Made absolute rather than special-cased. A special case for "."
	// would be the loosening instinct this kind of bug invites, and it
	// would leave the comparison resting on a representation that can
	// still be rewritten. An absolute path has one spelling, so there is
	// no case to make an exception for, and the check below gets
	// strictly stronger rather than more permissive.
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve destination %s: %w", destDir, err)
	}
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
		// Two checks, and the first one is the idiom the other two tar
		// extractors in this codebase already use (provider/
		// acquireschema.go, provider/acquiredynamicprovider.go): judge
		// the ENTRY NAME on its own, with no reference to the
		// destination at all. A check that never looks at the
		// destination cannot be broken by how the destination is
		// spelled, which is exactly what went wrong here.
		//
		// It also rejects an absolute entry name rather than relocating
		// it. filepath.Join would neutralise the leading separator and
		// quietly place "/etc/passwd" at <dest>/etc/passwd; writeTarGz
		// never emits such a name, so a tarball carrying one is foreign
		// or tampered and saying so is better than silently accepting a
		// rewritten version of it.
		cleaned := filepath.Clean(hdr.Name)
		if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("tar entry %q escapes the destination directory", hdr.Name)
		}

		// The containment check stays as well. It is redundant against
		// the check above for every case either can reach today, and
		// redundancy is the right posture for the one guard standing
		// between a downloaded archive and the filesystem.
		target := filepath.Join(absDest, cleaned)
		if target != absDest && !strings.HasPrefix(target, absDest+string(os.PathSeparator)) {
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
