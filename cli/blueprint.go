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
	cmd.AddCommand(newBlueprintPackageCmd())
	cmd.AddCommand(newBlueprintPullCmd())
	cmd.AddCommand(newBlueprintVerifyCmd())
	return cmd
}

// newBlueprintBuildCmd is `ubx blueprint build` (docs/blueprint.md):
// finds an Ubxfile in the given directory (default ".", the same
// `docker build .` convention the Ubxfile format itself borrows),
// resolves its resources: prose through the intent-provider pipeline
// exactly once, and compiles the resulting draft into a real, compilable
// Go package written into that same directory.
func newBlueprintBuildCmd() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "build [dir]",
		Short: "Build the Ubxfile in dir (default \".\") into a real, compilable SDK package",
		Long: `Reads the Ubxfile in dir (default the current directory, matching "docker build ."'s own convention
of finding a Dockerfile), resolves its resources: prose (inline, or an included .md file) through the same
intent-provider pipeline "ubx propose --from-doc"/"ubx plan --from-doc" already use -- exactly once -- and
compiles the resulting draft into a real Go package: one typed function per blueprint, parameters matching
the Ubxfile's own params: block, real sdk.Resource() calls with real Computed refs between them.

Slice 1 only: --lang is not yet a flag (the Ubxfile's own lang: key must be "go"); the built package is not
yet callable from a real stack (Slice 2), packaged, or published (Slice 3+).`,
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

			files, err := blueprint.GenerateGo(blueprintName, ubxfile, draft)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
			}

			names := make([]string, 0, len(files))
			for name := range files {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(absDir, name), []byte(files[name]), 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint build: %w", err)}
				}
			}

			fmt.Fprintf(outWriter, "built %d resource(s) -> %s (%s)\n", len(draft.Resources), absDir, strings.Join(names, ", "))
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for the intent-provider drafting call")

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

// newBlueprintPullCmd is `ubx blueprint pull` (docs/blueprint.md, Slice
// 3): resolves a blueprint reference (a local path, or a git repo+ref)
// into a real local directory. OCI/Strata sources aren't supported yet
// (Slice 7).
func newBlueprintPullCmd() *cobra.Command {
	var ref, path string

	cmd := &cobra.Command{
		Use:   "pull <source> <dest>",
		Short: "Pull a blueprint from a local path or a git repo into dest",
		Long: `source is either an existing local directory (copied into dest as-is, --ref/--path unused) or a git
repository URL (cloned, checked out at --ref -- branch/tag/commit, default the repo's own default branch -- then
the directory at --path within it, default ".", copied into dest). OCI/Strata sources aren't supported yet
(Slice 7).

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
	b.WriteString(ubxfile.Resources)
	return b.String()
}
