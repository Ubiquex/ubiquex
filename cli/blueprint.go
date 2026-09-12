package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	cmd.AddCommand(newBlueprintPackageCmd())
	cmd.AddCommand(newBlueprintPushCmd())
	cmd.AddCommand(newBlueprintPullCmd())
	cmd.AddCommand(newBlueprintVerifyCmd())
	cmd.AddCommand(newBlueprintDescribeCmd())
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

			ubxfile, draft, err := blueprint.Validate(absDir)
			if err != nil {
				// A blueprint that is CODE has no Ubxfile and needs no
				// build: its own source is what runs, and its schema is
				// derived by `package`. Without this an author who has
				// just written one gets a bare "open .../Ubxfile: no such
				// file or directory", which names a file they deliberately
				// do not have, at exactly the moment they are learning
				// that the two models differ.
				if _, statErr := os.Stat(filepath.Join(absDir, blueprint.UbxfileName)); statErr != nil {
					if lang, langErr := blueprint.DetectLanguage(absDir); langErr == nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %s holds %s source and no %s -- a blueprint written as code is not built, it IS the package; run `ubx blueprint package` to derive its schema and archive it", absDir, lang, blueprint.UbxfileName)}
					}
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			blueprintName := filepath.Base(absDir)

			outWriter := cmd.OutOrStdout()

			allFiles := map[string]string{}
			for _, l := range langs {
				files, err := blueprintGenerators[l](blueprintName, ubxfile, draft)
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
		Short: "Package a blueprint directory into a content-addressed tarball",
		Long: `Computes a content hash over every file in dir (the same canonical-hashing approach "ubx accept" already
uses for a Proposal's own hash -- core/canonical.go), writes it into dir/blueprint.lock.json, and archives dir
(including that manifest) into a gzipped tar at -o.

For a blueprint written as CODE, package is also where its schema is derived: the signature of its own
entrypoint function is read (without running it) and written to blueprint.schema.json inside dir, before the
hash is computed, so the hash covers it. The schema is re-derived on every package rather than reused, so it
cannot disagree with the function it describes.

For an Ubxfile blueprint, dir must already be built (an Ubxfile, plus whatever "ubx blueprint build"
produced) -- package builds nothing itself.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint package: -o is required")}
			}
			manifest, err := blueprint.Package(cmd.Context(), args[0], out)
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

// newBlueprintDescribeCmd is `ubx blueprint describe` (UBI-261): read a
// blueprint and report what it takes and returns, without building,
// packaging, or running it.
//
// It exists because an author writing a blueprint as code had no way to
// see the schema their own function produces. The schema is derived at
// package time, so checking it meant packaging the blueprint, or asking
// an assistant to call the describe_blueprint MCP tool. Needing an
// assistant to read your own function's signature is the wrong shape,
// and the MCP tool already proved the payload is worth having.
//
// Deliberately DERIVES rather than reading a written
// blueprint.schema.json, for a directory that holds source: the point
// is answering "what will this become", and reading a file that a
// previous package wrote would answer "what did it become last time",
// which is exactly the staleness the derived schema exists to prevent.
func newBlueprintDescribeCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "describe [dir]",
		Short: "Report what a blueprint takes and returns, without building or running it",
		Long: `Reads the blueprint in dir (default the current directory) and reports its parameters, its outputs,
and, for a blueprint written as code, the entrypoint a caller invokes.

For a blueprint that is code, the schema is DERIVED here from the function's own signature, the same derivation
"ubx blueprint package" performs, rather than read back from a blueprint.schema.json a previous package wrote.
That answers "what will this become", not "what did it become last time".

An optional parameter of a code blueprint reports "default not derivable": its default lives in the function
body, where reading it would mean running the code.`,
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
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint describe: %w", err)}
			}

			desc, err := describeBlueprintDir(cmd.Context(), absDir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint describe: %w", err)}
			}

			out := cmd.OutOrStdout()
			if asJSON {
				if desc.Schema == nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint describe: %s is described by its own %s, which has no JSON form -- --json reports a derived schema", absDir, blueprint.UbxfileName)}
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(desc.Schema); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint describe: %w", err)}
				}
				return nil
			}
			renderBlueprintDescription(out, desc)
			return nil
		},
	}

	// No backticks in this description: cobra reads a backticked span as
	// the flag's own value placeholder, so "`ubx blueprint package`"
	// rendered as "--json ubx blueprint package" in --help. Caught by
	// reading the real --help output while writing the user docs, which
	// is what CLAUDE.md rule 5 asks for.
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the derived schema as JSON, exactly as ubx blueprint package would write it")
	return cmd
}

// describeBlueprintDir derives a code blueprint's schema fresh, and
// falls back to whatever an Ubxfile blueprint already declares.
func describeBlueprintDir(ctx context.Context, absDir string) (*blueprint.Description, error) {
	if _, err := os.Stat(filepath.Join(absDir, blueprint.UbxfileName)); err != nil {
		if _, langErr := blueprint.DetectLanguage(absDir); langErr == nil {
			schema, err := blueprint.Extract(ctx, absDir, filepath.Base(absDir))
			if err != nil {
				return nil, err
			}
			return blueprint.DescriptionFromSchema(absDir, schema), nil
		}
	}
	return blueprint.Describe(absDir)
}

// renderBlueprintDescription prints the human form. Params and outputs
// keep their declaration order, which is the schema's own and the
// source's own.
func renderBlueprintDescription(out io.Writer, d *blueprint.Description) {
	fmt.Fprintf(out, "%s (%s, described by %s)\n", d.Name, d.Lang, d.Source)

	if e := d.Schema; e != nil {
		fmt.Fprintf(out, "\nentrypoint\n")
		switch e.Entrypoint.Language {
		case "go":
			fmt.Fprintf(out, "  import   %s\n", e.Entrypoint.GoModule)
			fmt.Fprintf(out, "  package  %s\n", e.Entrypoint.GoPackage)
		case "ts":
			fmt.Fprintf(out, "  entry    %s\n", e.Entrypoint.TSEntry)
		case "py":
			fmt.Fprintf(out, "  module   %s\n", e.Entrypoint.PyModule)
		}
		fmt.Fprintf(out, "  function %s(%s)", e.Entrypoint.Function, e.Entrypoint.ConfigType)
		if e.Entrypoint.OutputsType != "" {
			fmt.Fprintf(out, " %s", e.Entrypoint.OutputsType)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "\nparams\n")
	if len(d.Params) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, p := range d.Params {
		required := "optional"
		if p.Required {
			required = "required"
		}
		fmt.Fprintf(out, "  %-24s %-14s %s", p.Name, p.Type, required)
		switch {
		case p.Required:
		case d.DefaultsKnown:
			fmt.Fprintf(out, ", default %v", p.Default)
		default:
			fmt.Fprintf(out, ", default not derivable")
		}
		if sn := sourceNameOfParam(d, p.Name); sn != "" && sn != p.Name {
			fmt.Fprintf(out, "  [%s]", sn)
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "\noutputs\n")
	if len(d.Outputs) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, o := range d.Outputs {
		fmt.Fprintf(out, "  %-24s", o.Name)
		if o.Target != "" {
			fmt.Fprintf(out, " %s", o.Target)
		}
		if sn := sourceNameOfOutput(d, o.Name); sn != "" && sn != o.Name {
			fmt.Fprintf(out, "  [%s]", sn)
		}
		fmt.Fprintln(out)
	}

	if d.Schema != nil && len(d.Schema.Derivation.Assumptions) > 0 {
		fmt.Fprintf(out, "\nassumptions\n")
		for _, a := range d.Schema.Derivation.Assumptions {
			fmt.Fprintf(out, "  %s\n", a)
		}
	}
}

// sourceNameOfParam/sourceNameOfOutput surface the identifier as
// written, which is what a caller constructing a config literal needs
// and cannot reconstruct from the wire name.
func sourceNameOfParam(d *blueprint.Description, name string) string {
	if d.Schema == nil {
		return ""
	}
	for _, p := range d.Schema.Params {
		if p.Name == name {
			return p.SourceName
		}
	}
	return ""
}

func sourceNameOfOutput(d *blueprint.Description, name string) string {
	if d.Schema == nil {
		return ""
	}
	for _, o := range d.Schema.Outputs {
		if o.Name == name {
			return o.SourceName
		}
	}
	return ""
}
