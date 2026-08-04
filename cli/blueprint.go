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

// newBlueprintCmd is UBI-74 Slice 1's own CLI entry point -- a parent
// command, matching newSDKCmd's own shape, leaving room for Slice 3's
// `ubx blueprint package`/`pull`/`verify` siblings without renaming
// anything this slice ships.
func newBlueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blueprint",
		Short: "Blueprint commands: build a signed, reusable, parameterized proposal template",
	}
	cmd.AddCommand(newBlueprintBuildCmd())
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
	b.WriteString("do NOT invent or guess a concrete example value for it. Every other attribute not tied to a ")
	b.WriteString("declared parameter should be resolved normally, as usual.\n\n")
	b.WriteString(ubxfile.Resources)
	return b.String()
}
