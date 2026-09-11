package blueprint

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Package builds dir's own blueprint.lock.json (buildManifest/
// writeManifest, manifest.go) and archives the resulting directory --
// every real file it names, plus the manifest itself -- into a gzipped
// tar written to outPath.
//
// For an Ubxfile blueprint, dir must already be built (an Ubxfile plus
// whatever `ubx blueprint build` produced): Package builds nothing
// itself, and build and package are two separate sequential steps
// (docs/blueprint.md).
//
// For a blueprint that is CODE, package time is when the schema is
// derived (docs/blueprint.md's own "Blueprint schema"). It is written
// before the manifest is built, so the hash covers it like every other
// file, and it is re-derived on every package rather than reused: a
// schema that could be stale would defeat the reason it is derived at
// all.
func Package(ctx context.Context, dir, outPath string) (*Manifest, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("blueprint package: %w", err)
	}
	name := filepath.Base(absDir)

	// An Ubxfile blueprint keeps describing itself the way it always
	// did; only a code blueprint gets a derived schema.
	//
	// The marker check deliberately comes AFTER this rather than before
	// it. A blueprint that is code carries no marker until its schema is
	// derived, and its schema is derived HERE, so checking first refuses
	// every code blueprint that has not already been packaged once,
	// which is all of them the first time. Found by packaging a fresh
	// one (schemamodel_test.go). So the order is: derive if this looks
	// like source, then require that a marker now exists.
	if _, err := os.Stat(filepath.Join(absDir, UbxfileName)); err != nil {
		if _, langErr := DetectLanguage(absDir); langErr == nil {
			schema, err := Extract(ctx, absDir, name)
			if err != nil {
				return nil, fmt.Errorf("blueprint package: %w", err)
			}
			if err := WriteSchema(absDir, schema); err != nil {
				return nil, fmt.Errorf("blueprint package: %w", err)
			}
		}
	}

	if !IsBlueprintDir(absDir) {
		return nil, fmt.Errorf("blueprint package: %s has no %s, and no .go/.ts/.py source to derive one from -- package a blueprint's own source directory, or a directory `ubx blueprint build` already produced", absDir, blueprintMarkers())
	}

	manifest, err := buildManifest(absDir, name)
	if err != nil {
		return nil, fmt.Errorf("blueprint package: %w", err)
	}
	if err := writeManifest(absDir, manifest); err != nil {
		return nil, fmt.Errorf("blueprint package: %w", err)
	}

	rel := make([]string, 0, len(manifest.Files)+1)
	for r := range manifest.Files {
		rel = append(rel, r)
	}
	rel = append(rel, ManifestFileName)
	sort.Strings(rel)

	if err := writeTarGz(absDir, rel, outPath); err != nil {
		return nil, fmt.Errorf("blueprint package: %w", err)
	}
	return manifest, nil
}

// writeTarGz archives relFiles (paths relative to dir, already sorted --
// deterministic entry order) into a gzipped tar at outPath. Every
// header's ModTime/Uid/Gid is zeroed -- the same "an artifact should
// depend on content, not incidental packaging metadata" reasoning
// buildManifest's own doc comment states (manifest.go), applied here to
// the archive's own bytes too: a re-`package` of unchanged content
// reproduces a byte-identical tarball, not just an identical
// content_hash.
func writeTarGz(dir string, relFiles []string, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	epoch := time.Unix(0, 0).UTC()
	for _, rel := range relFiles {
		full := filepath.Join(dir, rel)
		info, err := os.Stat(full)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(rel),
			Size:    int64(len(raw)),
			Mode:    int64(info.Mode().Perm()),
			ModTime: epoch,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(raw); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}
