package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	repotemplate "github.com/ubiquex/ubiquex/sdk/codegen/templates/repo"
)

// newSDKInitRepoCmd is UBI-222's own real fix for a class of bug, not
// just one instance of it: `ubx sdk gen` produces a provider's real
// generated bindings, but never the one-time scaffold a brand new
// ubx-sdk-<name> repo also needs (LICENSE, publish.yml, build-npm.mjs,
// CLAUDE.md, README.md, STATE.md, HISTORY.md, go.sum) -- every
// existing provider repo had these hand-copied in from a sibling repo
// at onboarding time, out of band, with nothing generating them.
// DigitalOcean's own onboarding forgot two of them in a row
// (deno.json, then build-npm.mjs), each a real, live CI/local failure,
// not a hypothetical -- see sdk/codegen/templates/repo's own doc
// comment for the full account.
//
// Never overwrites a file that already exists -- a repo re-running
// this command after a founder has since hand-edited CLAUDE.md (a
// real, expected thing to happen) must not silently clobber that
// edit. go.sum is handled separately from the rest: it needs a real
// `go mod tidy` against the actual Go module proxy, not static
// template content, and only runs at all if sdk/go/go.mod already
// exists (a real `ubx sdk gen --lang go` output) and go.sum does not.
func newSDKInitRepoCmd() *cobra.Command {
	var (
		out             string
		shortName       string
		providerDisplay string
		sourceNote      string
	)

	cmd := &cobra.Command{
		Use:   "init-repo",
		Short: "Write the one-time SDK repo scaffold a genuinely new provider needs, never overwriting an already-existing file",
		Long: `Writes LICENSE, .github/scripts/build-npm.mjs, .github/workflows/publish.yml,
CLAUDE.md, README.md, STATE.md, and HISTORY.md into an already-generated
provider repo directory (ubx sdk gen's own --out/<short-name>), plus a real
sdk/go/go.sum via "go mod tidy" if sdk/go/go.mod already exists and go.sum
does not. Every file that already exists is left untouched, reported as
skipped rather than silently overwritten.

deno.json is NOT written here -- ubx sdk gen --lang ts already writes it
directly as real, per-provider generated content (its own "exports" map),
not scaffold.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSDKInitRepo(cmd.Context(), cmd, out, shortName, providerDisplay, sourceNote)
		},
	}

	cmd.Flags().StringVar(&out, "out", "", `the already-generated repo directory (ubx sdk gen's own --out/<short-name>) to write scaffold files into`)
	cmd.Flags().StringVar(&shortName, "short-name", "", `the real, published SDK repo's own short name (e.g. "digitalocean") -- matches [dynamic_providers.<name>] in ubiquex's own sdk/providers/.ubx/config`)
	cmd.Flags().StringVar(&providerDisplay, "provider-display", "", `the real, human display name (e.g. "DigitalOcean")`)
	cmd.Flags().StringVar(&sourceNote, "source-note", "", "one real, honest sentence describing this provider's own schema source and format (e.g. \"OpenAPI-sourced via `ubx-provider-dynamic`\") -- a deliberate, per-provider judgment call, not inferred from the name")
	for _, name := range []string{"out", "short-name", "provider-display", "source-note"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

func runSDKInitRepo(ctx context.Context, cmd *cobra.Command, out, shortName, providerDisplay, sourceNote string) error {
	files, err := repotemplate.Scaffold(shortName, providerDisplay, sourceNote)
	if err != nil {
		return err
	}

	var written, skipped []string
	for path := range files {
		full := filepath.Join(out, path)
		if _, err := os.Stat(full); err == nil {
			skipped = append(skipped, path)
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("sdk init-repo: %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("sdk init-repo: %s: %w", path, err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o644); err != nil {
			return fmt.Errorf("sdk init-repo: %s: %w", path, err)
		}
		written = append(written, path)
	}
	sort.Strings(written)
	sort.Strings(skipped)
	for _, p := range written {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", p)
	}
	for _, p := range skipped {
		fmt.Fprintf(cmd.OutOrStdout(), "skipped %s (already exists)\n", p)
	}

	goDir := filepath.Join(out, "sdk", "go")
	if _, err := os.Stat(filepath.Join(goDir, "go.mod")); os.IsNotExist(err) {
		fmt.Fprintf(cmd.OutOrStdout(), "no sdk/go/go.mod under %s -- skipping go mod tidy (run `ubx sdk gen --lang go` first)\n", out)
		return nil
	}
	if _, err := os.Stat(filepath.Join(goDir, "go.sum")); err == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "skipped sdk/go/go.sum (already exists)")
		return nil
	}
	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidy.Dir = goDir
	tidy.Stdout = cmd.OutOrStdout()
	tidy.Stderr = cmd.ErrOrStderr()
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("sdk init-repo: go mod tidy in %s: %w", goDir, err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "wrote sdk/go/go.sum")
	return nil
}
