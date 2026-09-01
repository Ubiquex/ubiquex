package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// newBlueprintCmd is UBI-74's own CLI entry point -- a parent command,
// matching newSDKCmd's own shape.
func newBlueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blueprint",
		Short: "Blueprint commands: build a signed, reusable, parameterized proposal template",
	}
	cmd.AddCommand(newBlueprintBuildCmd())
	cmd.AddCommand(newBlueprintConvertCmd())
	cmd.AddCommand(newBlueprintPackageCmd())
	cmd.AddCommand(newBlueprintPushCmd())
	cmd.AddCommand(newBlueprintPullCmd())
	cmd.AddCommand(newBlueprintVerifyCmd())
	return cmd
}

// blueprintGenerators maps a --lang value to its own codegen entry
// point -- Slice 4's own multi-language build model: resources: is
// parsed EXACTLY ONCE regardless of how many languages are requested,
// and each requested language's own generator compiles that SAME
// already-parsed intent independently. "all" isn't a key here --
// parseLangFlag expands it into every key below.
var blueprintGenerators = map[string]func(string, *blueprint.Ubxfile, *resolver.IntentFile) (map[string]string, error){
	"go": blueprint.GenerateGo,
	"ts": blueprint.GenerateTS,
	"py": blueprint.GeneratePython,
}

// parseLangFlag resolves --lang's own raw value into the ordered list of
// languages to build -- "" or "all" means every language (Slice 4's own
// resolved "no --lang default" design, UBI-74's own "--lang default"
// Linear comment: build ALL THREE from one AI draft when no flag is
// given, since the draft's own cost is paid once either way), one of
// "go"/"ts"/"py" narrows to exactly that language. Always returns
// languages in the same fixed order (go, ts, py) regardless of input
// order, so build output/log lines stay deterministic.
func parseLangFlag(lang string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "all":
		return []string{"go", "ts", "py"}, nil
	case "go", "ts", "py":
		return []string{lang}, nil
	default:
		return nil, fmt.Errorf("--lang %q not recognized -- want one of go, ts, py, all", lang)
	}
}

// newBlueprintBuildCmd is `ubx blueprint build` (docs/blueprint.md):
// finds an Ubxfile in the given directory (default ".", the same
// `docker build .` convention the Ubxfile format itself borrows),
// parses its resources: -- a pre-resolved intent/v1 JSON document, the
// SAME wire shape "ubx resolve --from-code --out <file>" already
// produces -- exactly once, and compiles it into real, compilable SDK
// packages -- one sibling directory per requested language ("go/",
// "ts/", "py/") written into that same directory.
//
// UBI-224 removed this command's own intent-provider draft step: a
// pre-validated Ubxfile has nothing left to interpret, only to parse,
// which makes build fully deterministic. A blueprint author now
// produces resources:'s own JSON themselves -- via the SDK ("ubx
// resolve --from-code --out resources.json"), or via "ubx blueprint
// convert" -- before it's ever checked in.
func newBlueprintBuildCmd() *cobra.Command {
	var lang string

	cmd := &cobra.Command{
		Use:   "build [dir]",
		Short: "Build the Ubxfile in dir (default \".\") into real, compilable SDK package(s)",
		Long: `Reads the Ubxfile in dir (default the current directory, matching "docker build ."'s own convention
of finding a Dockerfile), parses its resources: (inline JSON, or an included .json file) -- a pre-resolved
intent/v1 document, the SAME wire shape "ubx resolve --from-code --out <file>" already produces -- EXACTLY ONCE,
regardless of how many languages --lang requests, and compiles it into real SDK package(s): one typed function
per blueprint per language, parameters matching the Ubxfile's own params: block, real resource() calls with real
Computed refs between them, written into sibling "go/"/"ts/"/"py/" subdirectories of dir.

--lang selects which language(s): "go", "ts", "py", or "all" (every language -- the default when --lang is
omitted entirely, since resources: is only ever parsed once regardless of how many languages compile from it).`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			langs, err := parseLangFlag(lang)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			ubxfile, err := blueprint.ParseUbxfile(absDir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			blueprintName := filepath.Base(absDir)

			var draft resolver.IntentFile
			if err := json.Unmarshal([]byte(ubxfile.Resources), &draft); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: resources: is not a valid pre-resolved intent/v1 document (%s): %w", ubxfile.ResourcesSource, err)}
			}

			outWriter := cmd.OutOrStdout()

			allFiles := map[string]string{}
			for _, l := range langs {
				files, err := blueprintGenerators[l](blueprintName, ubxfile, &draft)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build (%s): %w", l, err)}
				}
				for name, content := range files {
					allFiles[name] = content
				}
			}

			names := make([]string, 0, len(allFiles))
			for name := range allFiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				full := filepath.Join(absDir, name)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
				}
				if err := os.WriteFile(full, []byte(allFiles[name]), 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
				}
			}

			fmt.Fprintf(outWriter, "built %d resource(s) -> %s (%s: %s)\n", len(draft.Resources), absDir, strings.Join(langs, ", "), strings.Join(names, ", "))
			return nil
		},
	}

	cmd.Flags().StringVar(&lang, "lang", "", "target language(s): go, ts, py, or all (default: all)")

	return cmd
}

// newBlueprintPackageCmd is `ubx blueprint package` (docs/blueprint.md,
// Slice 3): computes a content hash over a built blueprint directory's
// own files (the same canonical-hashing approach core.Hash already uses
// for a Proposal, core/canonical.go), writes it into
// dir/blueprint.lock.json, and archives the directory into a
// content-addressed gzipped tar at -o.
func newBlueprintPackageCmd() *cobra.Command {
	var out string

	cmd := &cobra.Command{
		Use:   "package <dir>",
		Short: "Package a built blueprint directory into a content-addressed tarball",
		Long: `Computes a content hash over every file in dir (the same canonical-hashing approach "ubx accept" already
uses for a Proposal's own hash -- core/canonical.go), writes it into dir/blueprint.lock.json, and archives dir
(including that manifest) into a gzipped tar at -o.

dir must already be a built blueprint (an Ubxfile, plus whatever "ubx blueprint build" produced) -- package
doesn't build anything itself.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint package: -o is required")}
			}
			manifest, err := blueprint.Package(args[0], out)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "packaged %q -> %s (%d file(s), content hash %s)\n", manifest.Name, out, len(manifest.Files), manifest.ContentHash)
			return nil
		},
	}

	cmd.Flags().StringVarP(&out, "output", "o", "", "output tarball path (e.g. ci-platform-v1.tar.gz)")

	return cmd
}

// newBlueprintPushCmd is `ubx blueprint push` (docs/blueprint.md, Slice
// 7): uploads a tarball `ubx blueprint package` already produced to a
// real OCI registry as a real OCI artifact -- the founder's own ORAS
// design (UBI-74 Linear comment 2026-08-04), one manifest wrapping the
// tarball as its one content-addressed blob layer, authenticated using
// the SAME credentials a real "docker login"/"oras login" already
// established.
func newBlueprintPushCmd() *cobra.Command {
	var to string

	cmd := &cobra.Command{
		Use:   "push <tarball>",
		Short: "Push a packaged blueprint tarball to a real OCI registry",
		Long: `Uploads tarball (ubx blueprint package's own output, unmodified) to --to (an "oci://registry/repo:tag"
reference, e.g. "oci://ghcr.io/ubiquex/ci-platform:v1") as a real OCI artifact via ORAS -- one manifest, the
tarball as its one blob layer. Authenticates using the SAME credentials a real "docker login"/"oras login" against
that registry already established (read from the real Docker credential store) -- this project never asks for a
second, ubx-specific login.

tarball must be a real "ubx blueprint package" output (it must contain blueprint.lock.json) -- pushing an
unpackaged directory isn't supported; package it first.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint push: --to is required")}
			}
			manifest, err := blueprint.Push(cmd.Context(), args[0], to)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pushed %q -> %s (%d file(s), content hash %s)\n", manifest.Name, to, len(manifest.Files), manifest.ContentHash)
			return nil
		},
	}

	cmd.Flags().StringVar(&to, "to", "", "OCI destination, e.g. oci://ghcr.io/ubiquex/ci-platform:v1 (required)")

	return cmd
}

// newBlueprintPullCmd is `ubx blueprint pull` (docs/blueprint.md, Slices
// 3, 7, and 8): resolves a blueprint reference (a local directory, a bare
// tarball file, a git repo+ref, or a real OCI artifact) into a real local
// directory. Strata itself (the eventual registry service) isn't built
// yet.
func newBlueprintPullCmd() *cobra.Command {
	var ref, path string

	cmd := &cobra.Command{
		Use:   "pull <source> <dest>",
		Short: "Pull a blueprint from a local path, a tarball file, a git repo, or an OCI registry into dest",
		Long: `source is one of four real forms:

  - an existing local directory: copied into dest as-is, --ref/--path unused.
  - an existing local FILE (not a directory): treated as a bare "ubx blueprint package" tarball -- extracted directly
    into dest, no network involved at all (Slice 8's own offline/email/support-ticket delivery mode). --ref/--path
    unused; run "ubx blueprint verify" afterward -- this is the one delivery mode with no git history or
    registry-native integrity to lean on, so verification is what actually protects it.
  - a git repository URL: cloned, checked out at --ref (branch/tag/commit, default the repo's own default branch),
    then the directory at --path within it (default ".") copied into dest.
  - an OCI artifact reference, "oci://registry/repo:tag" (e.g. "oci://ghcr.io/ubiquex/ci-platform:v1"): pulled via
    ORAS (oras.land/oras-go/v2), authenticated using the SAME credentials a real "docker login"/"oras login"
    already established (this project never asks for a second, ubx-specific login) -- --ref/--path are git-specific
    and refused if set, since the tag is already embedded in the oci:// reference itself.

dest must not already exist, or must be empty -- pull never overwrites existing content.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dest, err := blueprint.Pull(cmd.Context(), args[0], args[1], ref, path)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pulled %s -> %s\n", args[0], dest)
			return nil
		},
	}

	cmd.Flags().StringVar(&ref, "ref", "", "git ref (branch/tag/commit) -- default the repo's own default branch")
	cmd.Flags().StringVar(&path, "path", "", "path within the git repo to the blueprint package -- default \".\"")

	return cmd
}

// newBlueprintVerifyCmd is `ubx blueprint verify` (docs/blueprint.md,
// Slice 3): recomputes a blueprint directory's own content hash and
// confirms it matches blueprint.lock.json's own declared hash -- the
// same tamper-evidence principle proposal verification already gives a
// ledger entry, applied here to a pulled blueprint's own files.
func newBlueprintVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "verify <dir>",
		Short:         "Recompute a blueprint's own content hash and confirm it matches blueprint.lock.json",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := blueprint.Verify(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "verified %q: content hash %s matches (%d file(s))\n", manifest.Name, manifest.ContentHash, len(manifest.Files))
			return nil
		},
	}

	return cmd
}

// UBI-224: a blueprint author now writes resources:'s own {param_name}
// tokens (and the {param * N} arithmetic form, and the for_each list-
// param {list_param}/{list_param_index} pair) directly into the
// intent/v1 JSON they produce -- the same wire convention tfconvert
// (blueprint/decode.go, blueprint/cidrsubnet.go) already follows
// deterministically, with no AI drafting step involved. See
// docs/blueprint.md's own "The build pipeline" section for the full
// token grammar.
