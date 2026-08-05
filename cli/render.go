package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/diagram"
	"github.com/ubiquex/ubiquex/mdrender"
)

// newRenderCmd is UBI-47 slice 4's own render half of the diagram medium
// (docs/diagram-medium.md's own "render direction"), extended by UBI-86
// with a second output format: walk a stack's own live resources and
// emit either the canonical D2 rendering of their current, resolved
// shape (the default, unchanged since UBI-47), or, with --md, a
// readable markdown "current state" document (docs/render-md.md) --
// both share the identical ledger read (diagram.Walk), so the two
// formats can never disagree about WHICH resources exist or how they're
// grouped, only how that same real data is presented. The literal
// converse of `ubx propose --from-diagram`, and a projection surface
// this project's own "every medium is a projection, never a second
// source of truth" thesis promised (docs/architecture.md). Never writes
// to the ledger, never resolves anything -- a pure read, same posture
// `ubx status`'s own fleet walk already holds.
//
// --check reuses `ubx sdk gen`'s own "regenerate, meant to be committed
// and diffed by CI" discipline, but makes the diff-and-fail step a real,
// first-class command mode rather than leaving it to `git diff` outside
// the tool -- docs/diagram-medium.md's own explicit "render --check"
// contract: re-emit, byte-compare against --out's own current committed
// content, exit non-zero on any difference. Format-agnostic: it operates
// on rendered bytes, whichever format produced them. This is a real
// "finding," not a hard error (UBI-20, docs/exit-codes.mdx): exit 1 (the
// committed rendered/ copy is stale -- re-run without --check and commit
// the result), exit 2 (a real error -- bad ledger, bad stack).
func newRenderCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		out             string
		check           bool
		md              bool
		fromDrift       string
		syncOverrides   bool
		write           bool
		providerPath    string
		source          string
		providerVersion string
		providerConfig  string
		timeout         time.Duration
	)

	cmd := &cobra.Command{
		Use:           "render",
		Short:         "Emit the canonical D2 diagram (default) or, with --md, a readable markdown current-state document",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check && out == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --check requires --out (the committed file to compare against)")}
			}
			if fromDrift != "" && syncOverrides {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --from-drift and --sync-overrides are mutually exclusive")}
			}
			if write && !syncOverrides {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --write requires --sync-overrides")}
			}
			if (fromDrift != "" || syncOverrides) && !md {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --from-drift/--sync-overrides require --md (UBI-86 Part 2)")}
			}
			if (fromDrift != "" || syncOverrides) && check {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --check has no meaning together with --from-drift/--sync-overrides")}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --stack is required -- a render always covers exactly one stack")}
			}
			if fromDrift != "" || syncOverrides {
				// UBI-86: --from-drift/--sync-overrides are the only render
				// mode that ever launches a provider -- apply the SAME
				// config-file default `ubx status --drift`/`ubx resolve`
				// already do (a bare `.ubx/config` [provider] table needs
				// no --provider flag repeated at every call site).
				applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
				if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
				}
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			defer closeLedger()

			outWriter := cmd.OutOrStdout()

			// UBI-86 Part 2: a genuinely different render mode -- not the
			// current-state document at all, but a MECHANICAL (zero AI,
			// item 10's own confirmed requirement) override-statement
			// generator, templated from real status --drift comparison
			// data (scanDrift, drift.go -- the SAME core.RunScan/
			// DiffAttributes/FilterNormalizationNoise mechanism `ubx
			// status --drift` itself uses, never a second drift
			// algorithm) into whichever medium the calling stack is
			// authored in (autodetectMedium, plan.go's own established
			// auto-detection, reused unchanged).
			if fromDrift != "" || syncOverrides {
				return runRenderOverrideGeneration(cmd, outWriter, ledger, cfg, ledgerDir, stack, fromDrift, write,
					providerPath, source, providerVersion, providerConfig, timeout)
			}

			var rendered []byte
			if md {
				rendered, err = mdrender.Emit(ledger, stack)
			} else {
				rendered, err = diagram.Emit(ledger, stack)
			}
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}

			if check {
				committed, err := os.ReadFile(out)
				if err != nil {
					if os.IsNotExist(err) {
						return &ExitCodeError{Code: 1, Err: fmt.Errorf("render --check: %s does not exist yet -- run \"ubx render --stack %s --out %s\" and commit the result", out, stack, out)}
					}
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --check: %w", err)}
				}
				if bytes.Equal(committed, rendered) {
					fmt.Fprintf(outWriter, "render --check: %s matches the current resolved state\n", out)
					return nil
				}
				diff, err := unifiedDiff(out, rendered)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --check: %w", err)}
				}
				fmt.Fprintln(outWriter, diff)
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("render --check: %s is stale -- re-run \"ubx render --stack %s --out %s\" and commit the result", out, stack, out)}
			}

			if out == "" {
				outWriter.Write(rendered)
				return nil
			}
			if err := os.WriteFile(out, rendered, 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			fmt.Fprintf(outWriter, "wrote %s\n", out)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to render -- required (a render always covers exactly one stack)")
	cmd.Flags().StringVar(&out, "out", "", "write the rendered file here instead of stdout")
	cmd.Flags().BoolVar(&check, "check", false, "don't write -- byte-compare the freshly emitted render against --out's own current content, exiting 1 on any difference (requires --out)")
	cmd.Flags().BoolVar(&md, "md", false, "emit a readable markdown current-state document instead of the default D2 diagram (UBI-86, docs/render-md.md)")
	cmd.Flags().StringVar(&fromDrift, "from-drift", "", "generate a single override statement for one drifted resource's own address, in the calling stack's own authoring medium (requires --md; mutually exclusive with --sync-overrides)")
	cmd.Flags().BoolVar(&syncOverrides, "sync-overrides", false, "walk the whole stack's drift and generate an override statement for every drifted resource, in the calling stack's own authoring medium (requires --md; mechanical, zero AI)")
	cmd.Flags().BoolVar(&write, "write", false, "with --sync-overrides, append the generated override statement(s) directly into the calling stack's own source file instead of printing them (diagram/md authoring only -- refused for a Go/TS/Python-authored stack)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source; only used with --from-drift/--sync-overrides)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version; only used with --from-drift/--sync-overrides)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source; only used with --from-drift/--sync-overrides)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (only used with --from-drift/--sync-overrides)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the drift scan (only used with --from-drift/--sync-overrides)")
	return cmd
}

// runRenderOverrideGeneration is `ubx render --from-drift`/`--sync-
// overrides`'s own RunE body -- split out of newRenderCmd's own closure
// purely for readability (this is a genuinely different render mode, not
// a variation on the current-state document one above it).
func runRenderOverrideGeneration(
	cmd *cobra.Command,
	outWriter io.Writer,
	ledger *core.Ledger,
	cfg *Config,
	ledgerDir, stack, fromDrift string,
	write bool,
	providerPath, source, providerVersion, providerConfig string,
	timeout time.Duration,
) error {
	fleet, err := ledger.Fleet(stack)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
	}

	if fromDrift != "" {
		addr, ok := core.ParseAddress(fromDrift)
		if !ok {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --from-drift: %q is not a valid <stack>.<type>.<name> address", fromDrift)}
		}
		filtered := fleet[:0]
		for _, e := range fleet {
			if e.Address == addr {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --from-drift: %s not found in stack %q's own fleet", fromDrift, stack)}
		}
		fleet = filtered
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	drifted, err := scanDrift(ctx, ledger, cfg, providerPath, source, providerVersion, providerConfig, fleet)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
	}
	if fromDrift != "" && len(drifted) == 0 {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("render --from-drift: %s is not currently drifted", fromDrift)}
	}
	if len(drifted) == 0 {
		fmt.Fprintln(outWriter, "render --sync-overrides: no drift found -- nothing to generate")
		return nil
	}
	sort.Slice(drifted, func(i, j int) bool { return drifted[i].Address.String() < drifted[j].Address.String() })

	candidates, derr := autodetectMedium(ledgerDir)
	if derr != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: auto-detecting the calling stack's own authoring medium: %w", derr)}
	}
	if len(candidates) != 1 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: requires exactly one authoring medium file in %s to auto-detect against, found %d -- this command generates syntax for the stack's OWN source, which must be unambiguous", ledgerDir, len(candidates))}
	}
	medium, merr := overrideMediumFromDetected(candidates[0])
	if merr != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", merr)}
	}

	var buf bytes.Buffer
	for _, d := range drifted {
		topLevel, err := topLevelOverrideFields(d)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides: %w", err)}
		}
		stmt, err := renderOverrideStatement(medium, d.Address, topLevel)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides: %w", err)}
		}
		buf.WriteString(stmt)
		buf.WriteString("\n")
	}

	if write {
		if medium == "go" || medium == "ts" || medium == "py" {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides --write: not supported for an SDK-authored stack (%s) -- the override statement must be placed inside the stack's own describe function body, which this command can't safely splice into; copy the printed statement in by hand instead (run without --write)", candidates[0].path)}
		}
		f, err := os.OpenFile(candidates[0].path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides --write: %w", err)}
		}
		defer f.Close()
		if _, err := f.WriteString("\n" + buf.String()); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("render --sync-overrides --write: %w", err)}
		}
		fmt.Fprintf(outWriter, "wrote %d override statement(s) into %s\n", len(drifted), candidates[0].path)
		return nil
	}

	outWriter.Write(buf.Bytes())
	return nil
}
