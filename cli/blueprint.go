package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
	"github.com/ubiquex/ubiquex/intentprovider/claude"
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
// point -- Slice 4's own multi-language build model: the AI draft
// (draftBlueprint, below) runs EXACTLY ONCE regardless of how many
// languages are requested, and each requested language's own generator
// compiles that SAME already-drafted intent independently. "all" isn't
// a key here -- parseLangFlag expands it into every key below.
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
// resolves its resources: prose through the intent-provider pipeline
// exactly once, and compiles the resulting draft into real, compilable
// SDK packages -- one sibling directory per requested language ("go/",
// "ts/", "py/") written into that same directory.
func newBlueprintBuildCmd() *cobra.Command {
	var timeout time.Duration
	var lang string

	cmd := &cobra.Command{
		Use:   "build [dir]",
		Short: "Build the Ubxfile in dir (default \".\") into real, compilable SDK package(s)",
		Long: `Reads the Ubxfile in dir (default the current directory, matching "docker build ."'s own convention
of finding a Dockerfile), resolves its resources: prose (inline, or an included .md file) through the same
intent-provider pipeline "ubx propose --from-doc"/"ubx plan --from-doc" already use -- EXACTLY ONCE, regardless
of how many languages --lang requests -- and compiles the resulting draft into real SDK package(s): one typed
function per blueprint per language, parameters matching the Ubxfile's own params: block, real resource() calls
with real Computed refs between them, written into sibling "go/"/"ts/"/"py/" subdirectories of dir.

--lang selects which language(s): "go", "ts", "py", or "all" (every language -- the default when --lang is
omitted entirely, since the AI draft's own cost is paid once regardless of how many languages compile from it).`,
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

			cfg, err := loadConfigFromDir(absDir, cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			outWriter := cmd.OutOrStdout()
			adapterName, model := blueprintAdapterLabel(cfg)
			fmt.Fprintf(outWriter, "drafting via %s:%s… ", adapterName, model)
			draft, err := draftBlueprint(cmd, cfg, ubxfile, blueprintName, timeout)
			if err != nil {
				fmt.Fprintln(outWriter)
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}
			fmt.Fprintln(outWriter, "✓")

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

	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for the intent-provider drafting call")
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

// blueprintAdapterLabel mirrors plan.go's own zero-config progress-line
// label (UBI-87's own sonnet-5 default) -- named separately here rather
// than reused directly since plan.go's own version isn't exported and
// this command's own progress line has no other reason to depend on
// plan.go.
func blueprintAdapterLabel(cfg *Config) (adapter, model string) {
	adapter = cfg.Intent.Adapter
	if adapter == "" {
		adapter = "claude"
	}
	model = cfg.Intent.Model
	if model == "" {
		model = claude.DefaultModel
	}
	return adapter, model
}

// draftBlueprint resolves an Ubxfile's own resources: prose through
// UBI-41's intent-provider pipeline exactly once (docs/blueprint.md) --
// the same buildIntentAdapter/Redact/DraftWithRetry/PopulateSources
// sequence draftFromDoc (propose.go) already runs, with two real
// differences: the content comes from the already-parsed Ubxfile (never
// a second file read) and is wrapped with a short preamble instructing
// the model to preserve every {param_name} token literally rather than
// resolving it to a concrete example value -- what makes the built
// function's own parameters genuinely parameterized rather than freezing
// whatever sample value this one build-time draft happened to pick.
func draftBlueprint(cmd *cobra.Command, cfg *Config, ubxfile *blueprint.Ubxfile, blueprintName string, timeout time.Duration) (*resolver.IntentFile, error) {
	adapter, err := buildIntentAdapter(cfg)
	if err != nil {
		return nil, err
	}

	content := []byte(blueprintDraftPrompt(ubxfile))
	redacted, findings := intentprovider.Redact(content)
	errOut := cmd.ErrOrStderr()
	for _, f := range findings {
		fmt.Fprintf(errOut, "warning: redacted possible secret material (%s) before sending the blueprint's own resources: prose to the %s adapter\n", f, adapter.Name())
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	draft, rawOutput, err := intentprovider.DraftWithRetry(ctx, adapter, blueprintName, redacted, nil)
	if err != nil {
		return nil, err
	}

	source := ubxfile.ResourcesSource
	if source == "inline" {
		source = filepath.Join(ubxfile.Dir, blueprint.UbxfileName)
	}
	intentprovider.PopulateSources(draft, intentprovider.SourceKindDocument, source, intentprovider.HashDocument(content), adapter.Name(), adapter.Model(), rawOutput)
	return draft, nil
}

// blueprintDraftPrompt wraps an Ubxfile's own resolved resources: prose
// with the parameter-preservation preamble docs/blueprint.md's "The
// build pipeline" section describes. A blueprint with zero declared
// params needs no preamble at all -- the prose is passed straight
// through unchanged, identical to any other intent-provider document.
func blueprintDraftPrompt(ubxfile *blueprint.Ubxfile) string {
	if len(ubxfile.Params) == 0 {
		return ubxfile.Resources
	}

	var b strings.Builder
	b.WriteString("This document describes a PARAMETERIZED BLUEPRINT TEMPLATE, not a concrete stack. ")
	b.WriteString("The following parameters are declared and will be supplied by the caller later, not now:\n")
	for _, p := range ubxfile.Params {
		fmt.Fprintf(&b, "- %s (%s)\n", p.Name, p.Type)
	}
	b.WriteString("Wherever the prose below writes a token like \"{param_name}\" naming one of these parameters, ")
	b.WriteString("preserve that EXACT token literally, unresolved, in the corresponding resolved config value -- ")
	b.WriteString("do NOT invent or guess a concrete example value for it. If a real attribute's own unit or scale ")
	b.WriteString("differs from a parameter's own natural unit (e.g. a real AWS attribute wants seconds but the ")
	b.WriteString("parameter is naturally expressed in days, or a real attribute is a count doubled/halved from the ")
	b.WriteString("parameter), preserve the SAME token but with the conversion written INSIDE the braces, exactly ")
	b.WriteString("one arithmetic operator and one literal number, e.g. \"{retention_days * 86400}\" -- never resolve ")
	b.WriteString("it to a concrete computed number, never combine two parameters, never write a more complex ")
	b.WriteString("expression than one operator against one literal constant. Every other attribute not tied to a ")
	b.WriteString("declared parameter should be resolved normally, as usual.\n\n")

	if hasListParam(ubxfile.Params) {
		b.WriteString("One or more of the parameters above is list-typed (list(string)/list(number)). A list-typed ")
		b.WriteString("parameter is NEVER referenced directly as an ordinary {param_name} token -- only via a resource ")
		b.WriteString("that iterates over it. If (and only if) the prose below describes a real iteration pattern for ")
		b.WriteString("a resource -- \"For each value in {list_param}, create...\", or an equivalent phrasing -- set that ")
		b.WriteString("resource's own for_each field to the list param's bare name (never wrapped in braces). At most one ")
		b.WriteString("resource in this document may do this. Within THAT SAME resource's own name/config fields (never ")
		b.WriteString("any other resource's), refer to the current loop element's own value with the ordinary bare ")
		b.WriteString("{list_param} token (the SAME name you set for_each to), and to its own zero-based position in the ")
		b.WriteString("list with {list_param_index} (that same name with \"_index\" appended) -- both substituted ")
		b.WriteString("literally, exactly like any other {param_name} token, never resolved to a concrete example value. ")
		b.WriteString("This resource's own name must genuinely vary per iteration using one of these two tokens (e.g. ")
		b.WriteString("\"subnet-{availability_zones}\") -- never a fixed literal, and never a Terraform-style numeric ")
		b.WriteString("index scheme of your own invention. A resource with for_each set cannot be referenced by any ")
		b.WriteString("sibling resource, and this document cannot declare outputs: at all if it declares a for_each ")
		b.WriteString("resource. If the prose doesn't actually describe an iteration pattern, leave every resource's own ")
		b.WriteString("for_each as \"\" as usual, even though a list param is declared.\n\n")
	}

	b.WriteString(ubxfile.Resources)
	return b.String()
}

// hasListParam reports whether any of params is list-typed (UBI-129) --
// blueprintDraftPrompt only pays the extra preamble cost (and the model
// only needs the for_each recognition instructions at all) when a
// blueprint actually declares one; every blueprint before this ticket
// declares none and is completely unaffected.
func hasListParam(params []blueprint.Param) bool {
	for _, p := range params {
		if p.Type.IsList() {
			return true
		}
	}
	return false
}
