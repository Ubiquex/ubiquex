package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/tfconvert"
)

// newBlueprintConvertCmd is `ubx blueprint convert --from-terraform`
// (UBI-125, docs/blueprint.md's own "Terraform module conversion"
// section): translates a real Terraform module directly into a built
// blueprint, deterministically -- unlike `ubx blueprint build`, this
// command never calls the intent-provider pipeline at all (tfconvert's
// own package doc comment has the full "why HCL doesn't need AI here"
// account) -- it writes the SAME go/ts/py package layout `build` writes,
// directly, from a deterministically-constructed draft.
func newBlueprintConvertCmd() *cobra.Command {
	var fromTerraform, out, lang string

	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert a real Terraform module into a built blueprint, deterministically (no AI)",
		Long: `Parses every top-level *.tf file in --from-terraform (a real Terraform module directory, non-recursive) directly
via HCL, and translates variable/resource/output blocks into a blueprint mechanically -- no intent-provider/AI call
at all, matching this project's own established discipline for schema-to-code translation (CLAUDE.md): HCL is
already precise, so there's no ambiguity for a model to resolve.

Writes an Ubxfile plus the built go/ts/py package(s) directly into --out, exactly like "ubx blueprint build" would
have produced from an equivalent AI-drafted resources: prose -- except the Ubxfile's own resources: field is
documentation only for a converted blueprint (a short, deterministic summary of what was converted): running
"ubx blueprint build" again on a converted directory re-drafts resources: through the AI pipeline like any other
Ubxfile, and there's no guarantee that re-draft reproduces this command's own deterministic translation -- this
command's OWN output (the go/ts/py packages it writes) is the source of truth for a converted blueprint.

What converts mechanically: variable blocks (params:), resource blocks (resource calls, including a real
count = length(var.list)/for_each = var.list|toset(var.list) -> blueprint's own for_each translation), output
blocks naming a plain resource attribute (outputs:), and the cidrsubnet() built-in (ported as a real generated Go
helper -- Go only so far, a real, named follow-up for TS/Python). Anything the converter can't handle mechanically
(a conditional, an unported function, a data source, a complex locals chain, ...) is never guessed: it's recorded
as a real, non-blocking Question (the SAME mechanism the intent provider already uses for genuine ambiguity) and
the one affected attribute/resource/output is dropped, printed clearly, rather than failing the whole conversion
or silently producing something wrong.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromTerraform == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: --from-terraform is required")}
			}
			if out == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: --out is required")}
			}
			absOut, err := filepath.Abs(out)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}
			langs, err := parseLangFlag(lang)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}
			blueprintName := filepath.Base(absOut)

			res, err := tfconvert.Convert(fromTerraform, blueprintName)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}

			ubxLang := "all"
			if len(langs) == 1 {
				ubxLang = langs[0]
			}
			ubxfile := &blueprint.Ubxfile{
				Dir:             absOut,
				Lang:            ubxLang,
				Params:          res.Params,
				Resources:       res.Summary,
				ResourcesSource: "inline",
				Outputs:         res.Outputs,
			}

			allFiles := map[string]string{}
			for _, l := range langs {
				files, err := blueprintGenerators[l](blueprintName, ubxfile, res.Intent)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert (%s): %w", l, err)}
				}
				for name, content := range files {
					allFiles[name] = content
				}
			}

			if err := os.MkdirAll(absOut, 0o755); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}
			names := make([]string, 0, len(allFiles))
			for name := range allFiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				full := filepath.Join(absOut, name)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
				}
				if err := os.WriteFile(full, []byte(allFiles[name]), 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
				}
			}
			ubxfilePath := filepath.Join(absOut, blueprint.UbxfileName)
			if err := os.WriteFile(ubxfilePath, []byte(renderConvertedUbxfile(ubxfile)), 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}

			outWriter, errWriter := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if len(res.Questions) > 0 {
				fmt.Fprintf(errWriter, "%d question(s) -- not fatal, review before trusting the converted blueprint:\n", len(res.Questions))
				for _, q := range res.Questions {
					fmt.Fprintf(errWriter, "  - %s\n", q.Text)
				}
			}
			if len(res.RequiredProviders) > 0 {
				fmt.Fprintln(outWriter, "declared provider(s) (confirm the converted blueprint resolves against these before trusting it):")
				for _, p := range res.RequiredProviders {
					fmt.Fprintf(outWriter, "  - %s: %s %s\n", p.LocalName, p.Source, p.Version)
				}
			}
			fmt.Fprintf(outWriter, "converted %d resource(s) (%d skipped) -> %s (%s: %s)\n",
				len(res.Intent.Resources), len(res.SkippedResources), absOut, strings.Join(langs, ", "), strings.Join(names, ", "))
			return nil
		},
	}

	cmd.Flags().StringVar(&fromTerraform, "from-terraform", "", "path to a real Terraform module directory to convert (required)")
	cmd.Flags().StringVar(&out, "out", "", "output directory for the converted blueprint (required)")
	cmd.Flags().StringVar(&lang, "lang", "", "target language(s) to build: go, ts, py, or all (default: all)")

	return cmd
}

// renderConvertedUbxfile hand-renders ubxfile's own YAML text -- a
// deterministic, bespoke serializer (matching writeback.go's own "no
// library magic where a direct, exact-format implementation suffices"
// precedent) rather than a generic YAML marshal, since ParseUbxfile's
// own params:/outputs: grammar ("<type>, required"/"<type>, default
// <value>", "<name>: <resource-slug>.<attr>") is simple and fully under
// this function's own control. resources: is always written as a block
// scalar (`resources: |`) -- Result.Summary is short, single-line
// documentation text today, but a block scalar needs no escaping even
// if a future summary ever grows multi-line.
func renderConvertedUbxfile(u *blueprint.Ubxfile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lang: %s\n", u.Lang)

	if len(u.Params) > 0 {
		b.WriteString("\nparams:\n")
		for _, p := range u.Params {
			fmt.Fprintf(&b, "  %s: %s\n", p.Name, renderParamSpec(p))
		}
	}

	b.WriteString("\nresources: |\n")
	for _, line := range strings.Split(u.Resources, "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}

	if len(u.Outputs) > 0 {
		b.WriteString("\noutputs:\n")
		for _, o := range u.Outputs {
			fmt.Fprintf(&b, "  %s: %s\n", o.Name, o.Target)
		}
	}
	return b.String()
}

// renderParamSpec is parseParamSpec's own inverse (blueprint/ubxfile.go)
// -- "<type>, required" or "<type>, default <value>". tfconvert never
// sets Default on a list-typed param (ubxfile.go's own parseDefaultValue
// permanently refuses one), so the list-type branches below are only
// ever reached with Required true, matching every real Result
// tfconvert.Convert can produce.
func renderParamSpec(p blueprint.Param) string {
	if p.Required {
		return fmt.Sprintf("%s, required", p.Type)
	}
	switch v := p.Default.(type) {
	case string:
		return fmt.Sprintf("%s, default %q", p.Type, v)
	case int:
		return fmt.Sprintf("%s, default %s", p.Type, strconv.Itoa(v))
	case bool:
		return fmt.Sprintf("%s, default %s", p.Type, strconv.FormatBool(v))
	default:
		return fmt.Sprintf("%s, required", p.Type)
	}
}
