package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/diagram"
)

// newRenderCmd is UBI-47 slice 4's own render half of the diagram medium
// (docs/diagram-medium.md's own "render direction"): walk a stack's own
// live resources and emit the canonical D2 rendering of their current,
// resolved shape -- the literal converse of `ubx propose --from-diagram`,
// and the fourth projection surface this project's own "every medium is
// a projection, never a second source of truth" thesis promised
// (docs/architecture.md). Never writes to the ledger, never resolves
// anything -- a pure read, same posture `ubx status`'s own fleet walk
// already holds.
//
// --check reuses `ubx sdk gen`'s own "regenerate, meant to be committed
// and diffed by CI" discipline, but makes the diff-and-fail step a real,
// first-class command mode rather than leaving it to `git diff` outside
// the tool -- docs/diagram-medium.md's own explicit "render --check"
// contract: re-emit, byte-compare against --out's own current committed
// content, exit non-zero on any difference. This is a real "finding," not
// a hard error (UBI-20, docs/exit-codes.mdx): exit 1 (the committed
// rendered/ copy is stale -- re-run without --check and commit the
// result), exit 2 (a real error -- bad ledger, bad stack).
func newRenderCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		out       string
		check     bool
	)

	cmd := &cobra.Command{
		Use:           "render",
		Short:         "Emit the canonical D2 rendering of a stack's own live, resolved resources",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if check && out == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --check requires --out (the committed file to compare against)")}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: errors.New("render: --stack is required -- a diagram is always rendered for exactly one stack")}
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}
			defer closeLedger()

			rendered, err := diagram.Emit(ledger, stack)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("render: %w", err)}
			}

			outWriter := cmd.OutOrStdout()

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
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to render -- required (a diagram is always rendered for exactly one stack)")
	cmd.Flags().StringVar(&out, "out", "", "write the rendered .d2 file here instead of stdout")
	cmd.Flags().BoolVar(&check, "check", false, "don't write -- byte-compare the freshly emitted diagram against --out's own current content, exiting 1 on any difference (requires --out)")
	return cmd
}
