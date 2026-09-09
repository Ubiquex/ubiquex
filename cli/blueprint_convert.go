package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/provider"
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
	var checkSource, checkVersion, checkProvider string

	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert a real Terraform module into a built blueprint, deterministically (no AI)",
		Long: `Parses every top-level *.tf file in --from-terraform (a real Terraform module directory, non-recursive) directly
via HCL, and translates variable/resource/output blocks into a blueprint mechanically -- no intent-provider/AI call
at all, matching this project's own established discipline for schema-to-code translation (CLAUDE.md): HCL is
already precise, so there's no ambiguity for a model to resolve.

Writes an Ubxfile plus the built go/ts/py package(s) directly into --out, exactly like "ubx blueprint build" would
have produced -- except the Ubxfile's own resources: field is documentation only for a converted blueprint (a
short, deterministic summary of what was converted, not a pre-resolved intent/v1 JSON document): "ubx blueprint
build" no longer has any drafting step at all (UBI-224 removed it -- build only ever parses resources: as JSON
now), so running it again on a converted directory fails outright, a JSON parse error, rather than reproducing
anything -- this command's OWN output (the go/ts/py packages it writes) is the source of truth for a converted
blueprint.

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
			absFrom, err := filepath.Abs(fromTerraform)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: %w", err)}
			}
			ubxfile := &blueprint.Ubxfile{
				Dir:             absOut,
				Lang:            ubxLang,
				ConvertedFrom:   absFrom,
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
			// Required-attribute validation, when a provider to check
			// against was named.
			//
			// A resource can convert while losing the attributes it cannot
			// work without: terraform-aws-sqs produces four queue-policy
			// resources retaining only `region`, having lost queue_url and
			// their entire policy document. That is not a partial
			// conversion, it is a resource that would fail at the provider
			// or create something meaningless, and reporting it as
			// converted was the misleading half of the resource count.
			//
			// Requires a schema, which the converter otherwise never has,
			// and a provider the caller must name: the module declares
			// which provider IT used (hashicorp/aws here), but a converted
			// blueprint resolves against whatever the calling stack
			// configures, which is unknowable at convert time. So this is
			// opt-in by flag rather than inferred, and its absence is
			// reported rather than silent.
			if checkSource != "" || checkProvider != "" {
				dropped, cerr := refuseResourcesMissingRequired(cmd.Context(), res, checkProvider, checkSource, checkVersion)
				if cerr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint convert: --check-against: %w", cerr)}
				}
				for _, d := range dropped {
					fmt.Fprintf(errWriter, "  - %s\n", d)
				}
			}

			if len(res.RequiredProviders) > 0 {
				fmt.Fprintln(outWriter, "declared provider(s) (confirm the converted blueprint resolves against these before trusting it):")
				for _, p := range res.RequiredProviders {
					fmt.Fprintf(outWriter, "  - %s: %s %s\n", p.LocalName, p.Source, p.Version)
				}
			}
			// Converting nothing is a failed conversion, not a quiet one.
			//
			// Measured against terraform-aws-modules/terraform-aws-sqs
			// (UBI-125, 2026-09-08): every one of its eight resources uses
			// `count = var.create ? 1 : 0`, which this converter does not
			// support, so all eight were skipped and all ten outputs
			// dropped. The command printed
			// `converted 0 resource(s) (8 skipped)` and exited 0, leaving
			// an empty blueprint that builds, packages and hashes exactly
			// like a real one. Conditional count is the house style across
			// that entire org, so this is the common case for a real
			// published module, not an edge case.
			//
			// Exit 1, not 2: this is an actionable finding about the input
			// module, the same tier `ubx scan` uses for a real drift
			// finding, not a usage error. The questions above already say
			// which constructs were refused, so this adds the verdict
			// rather than repeating them.
			if len(res.Intent.Resources) == 0 {
				fmt.Fprintf(outWriter, "converted 0 resource(s) (%d skipped) -> %s (%s: %s)\n",
					len(res.SkippedResources), absOut, strings.Join(langs, ", "), strings.Join(names, ", "))
				return &ExitCodeError{Code: 1, Err: fmt.Errorf(
					"blueprint convert: nothing was converted -- every resource in %s was skipped, so the blueprint written to %s is empty and describes none of that module. "+
						"Review the question(s) above for which constructs were refused; an empty blueprint still builds, packages and hashes like a real one, which is why this is an error rather than a warning",
					fromTerraform, absOut)}
			}
			fmt.Fprintf(outWriter, "converted %d resource(s) (%d skipped) -> %s (%s: %s)\n",
				len(res.Intent.Resources), len(res.SkippedResources), absOut, strings.Join(langs, ", "), strings.Join(names, ", "))
			writeRetentionReport(outWriter, res.Retention)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromTerraform, "from-terraform", "", "path to a real Terraform module directory to convert (required)")
	cmd.Flags().StringVar(&out, "out", "", "output directory for the converted blueprint (required)")
	cmd.Flags().StringVar(&lang, "lang", "", "target language(s) to build: go, ts, py, or all (default: all)")
	cmd.Flags().StringVar(&checkSource, "check-against", "", `provider source to validate the converted resources against, e.g. "ubiquex/aws" or "hashicorp/aws" -- requires --check-against-version. Without it, no required-attribute check runs and the conversion is reported as-is`)
	cmd.Flags().StringVar(&checkVersion, "check-against-version", "", "explicit provider version to acquire for --check-against")
	cmd.Flags().StringVar(&checkProvider, "check-against-provider", "", "path to a provider binary to validate against, instead of --check-against (mutually exclusive with it)")

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

	// converted_from marks this blueprint as one `ubx blueprint build`
	// cannot rebuild, so it can say that plainly instead of failing on a
	// JSON parse at the first letter of the prose below.
	if u.ConvertedFrom != "" {
		fmt.Fprintf(&b, "converted_from: %s\n", u.ConvertedFrom)
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

// writeRetentionReport says what a conversion actually preserved, not
// only how many resources it produced.
//
// "converted 6 resource(s)" was a misleading measure. Converting
// terraform-aws-modules/terraform-aws-sqs reports six resources, of which
// four retain exactly one attribute each, all of them region, having lost
// queue_url and their entire policy document. A queue policy with no
// policy and no queue_url is not a partial conversion; it is a resource
// that would fail at the provider or create something meaningless. The
// 71 questions said so individually and nothing summarised it.
//
// Stated as the fact rather than as a percentage. "40% of attributes
// retained" reads like a partial success; "4 of 6 resources retained only
// 1 attribute each (region)" is what actually happened, and names the
// resources so the reader can go and look.
func writeRetentionReport(w io.Writer, retention []tfconvert.Retention) {
	if len(retention) == 0 {
		return
	}
	source, kept := 0, 0
	for _, r := range retention {
		source += r.SourceAttrs
		kept += r.KeptAttrs
	}
	fmt.Fprintf(w, "  %d of %d attribute(s) retained\n", kept, source)

	// A resource that lost most of itself is the thing worth naming. The
	// threshold is deliberately generous rather than tuned: losing more
	// than half of a resource's own attributes is worth a reader's
	// attention whatever the resource is.
	var gutted []tfconvert.Retention
	for _, r := range retention {
		if r.SourceAttrs > 0 && r.KeptAttrs*2 <= r.SourceAttrs {
			gutted = append(gutted, r)
		}
	}
	if len(gutted) == 0 {
		return
	}
	sort.Slice(gutted, func(i, j int) bool { return gutted[i].Address < gutted[j].Address })
	fmt.Fprintf(w, "  %d of %d converted resource(s) lost more than half their attributes:\n", len(gutted), len(retention))
	for _, r := range gutted {
		remaining := "nothing"
		if len(r.Kept) > 0 {
			remaining = strings.Join(r.Kept, ", ")
		}
		fmt.Fprintf(w, "    %s: kept %d of %d (%s)\n", r.Address, r.KeptAttrs, r.SourceAttrs, remaining)
	}
}

// refuseResourcesMissingRequired drops every converted resource that lost
// an attribute the provider's own schema marks Required, and returns one
// human-readable line per drop.
//
// The same rule as refusing an empty conversion, one level down. A
// resource that converts without the attributes it cannot work without is
// not partially converted: aws_sqs_queue_policy with no queue_url and no
// policy would fail at the provider or create something meaningless, and
// counting it as converted is what made "converted 6 resource(s)" a
// misleading measure for terraform-aws-sqs.
//
// Dropped rather than failing the whole conversion, matching how every
// other unconvertible thing here behaves: the resource is removed, the
// reason is stated, and the rest of the module still converts. A
// conversion that produces nothing at all still fails, via the existing
// zero-resource check, which now also catches a module whose every
// resource is refused here.
//
// Attributes the converter itself never sees cannot be judged: this only
// checks top-level Required attributes, since nested block requirements
// live behind a schema shape the converted config does not mirror
// one-to-one.
func refuseResourcesMissingRequired(ctx context.Context, res *tfconvert.Result, providerPath, source, version string) ([]string, error) {
	if providerPath != "" && source != "" {
		return nil, fmt.Errorf("--check-against and --check-against-provider are mutually exclusive")
	}
	path, _, err := resolveProviderBinary(ctx, providerPath, source, version)
	if err != nil {
		return nil, err
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("read provider schema: %w", err)
	}

	var dropped []string
	kept := res.Intent.Resources[:0]
	for _, ri := range res.Intent.Resources {
		rs, ok := schemas.Resources[ri.Type]
		if !ok {
			// A type the checked provider does not have is not evidence
			// the resource is wrong, only that this is the wrong provider
			// to judge it. Said out loud rather than dropped or ignored.
			dropped = append(dropped, fmt.Sprintf("resource %q: type %q is not in the checked provider's schema -- not validated (is --check-against the provider this blueprint will resolve against?)", ri.Type+"."+ri.Name, ri.Type))
			kept = append(kept, ri)
			continue
		}
		var config map[string]any
		if err := json.Unmarshal(ri.Config, &config); err != nil {
			return nil, fmt.Errorf("resource %s.%s: parse converted config: %w", ri.Type, ri.Name, err)
		}
		var missing []string
		for _, a := range rs.Block.Attributes {
			if a.Required {
				if _, present := config[a.Name]; !present {
					missing = append(missing, a.Name)
				}
			}
		}
		if len(missing) == 0 {
			kept = append(kept, ri)
			continue
		}
		sort.Strings(missing)
		dropped = append(dropped, fmt.Sprintf("resource %q lost required attribute(s) %s -- NOT converted; a resource missing what it cannot work without would fail at the provider or create something meaningless",
			ri.Type+"."+ri.Name, strings.Join(missing, ", ")))
	}
	res.Intent.Resources = kept

	// Keep the retention report describing what actually survived. Left
	// unfiltered it would report on resources this function just removed,
	// so "converted 2 resource(s)" and "5 of 6 converted resource(s) lost
	// more than half" would appear together and contradict each other.
	surviving := make(map[string]bool, len(kept))
	for _, ri := range kept {
		surviving[ri.Type+"."+ri.Name] = true
	}
	keptRetention := res.Retention[:0]
	for _, r := range res.Retention {
		if surviving[r.Slug] {
			keptRetention = append(keptRetention, r)
		}
	}
	res.Retention = keptRetention
	return dropped, nil
}
