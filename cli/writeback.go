package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/tfwrite"
)

// newWritebackCmd is UBI-11 stage 2: ".tf write-back" (docs/architecture.md
// — Decision loop). Triggered only by an already-accepted drift_adopt
// proposal — never a change or anything still in draft, since write-back
// records reality into existing source, it doesn't propose new
// infrastructure. Every attribute tfwrite declines is reported, not
// silently dropped, and by default nothing is written to disk at all — a
// diff is printed for review, matching every other "human in the loop"
// surface in this trust chain (see cmd's own --write flag for opting into
// an actual file write, which still never commits or pushes).
func newWritebackCmd() *cobra.Command {
	var (
		ledgerDir string
		tfDir     string
		write     bool
	)

	cmd := &cobra.Command{
		Use:   "writeback <proposal-id>",
		Short: "Write an accepted drift_adopt proposal's recorded reality back into existing .tf source",
		Args:  cobra.ExactArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0
		// (everything applied cleanly, or nothing to write back), 1 (an
		// attribute was declined, or a resource block couldn't be located --
		// the write-back is incomplete and needs a human, an actionable
		// finding), 2 (error). SilenceUsage/Errors: same reasoning as
		// status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("writeback: %w", err)}
			}
			applyTFDirDefault(cmd, &tfDir, cfg)

			ledger := core.Open(ledgerDir)
			p, err := ledger.Read(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			if p.Kind != core.KindDriftAdopt {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("writeback: proposal %s is kind %q, not drift_adopt -- write-back only applies to recorded drift", p.ID, p.Kind)}
			}
			if p.Status != core.StatusAccepted {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("writeback: proposal %s is not accepted (status %q)", p.ID, p.Status)}
			}
			if len(p.Delta.Modifies) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "writeback: proposal has no delta.modifies entries -- nothing to write back")
				return nil
			}

			out := cmd.OutOrStdout()
			var unresolved []string
			var declinedCount int
			for _, m := range p.Delta.Modifies {
				path, newContent, report, err := tfwrite.FindAndApply(tfDir, m.Target, m)
				if err != nil {
					fmt.Fprintf(out, "%s: %v\n", m.Target, err)
					unresolved = append(unresolved, m.Target.String())
					continue
				}

				fmt.Fprintf(out, "%s (%s):\n", m.Target, path)
				for _, a := range report.Applied {
					fmt.Fprintf(out, "  applied: %s\n", a)
				}
				declinedCount += len(report.Declined)
				for _, d := range report.Declined {
					fmt.Fprintf(out, "  declined: %s -- %s", d.Path, d.Reason)
					if d.Expression != "" {
						fmt.Fprintf(out, " (current value: %s)", d.Expression)
					}
					fmt.Fprintln(out)
				}
				if len(report.Applied) == 0 {
					continue
				}

				if write {
					if err := os.WriteFile(path, newContent, 0o644); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("writeback: write %s: %w", path, err)}
					}
					fmt.Fprintf(out, "  wrote %s\n", path)
				} else {
					diff, err := unifiedDiff(path, newContent)
					if err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("writeback: diff %s: %w", path, err)}
					}
					fmt.Fprintln(out, diff)
				}
			}

			if len(unresolved) > 0 {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("writeback: could not locate a resource block to write back into for: %s", strings.Join(unresolved, ", "))}
			}
			if declinedCount > 0 {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("writeback: %d attribute(s) declined, needs manual attention (see above)", declinedCount)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&tfDir, "tf-dir", ".", "directory containing the .tf files to write back into")
	cmd.Flags().BoolVar(&write, "write", false, "actually write the modified file to disk instead of printing a diff (never commits or pushes)")
	return cmd
}

// unifiedDiff shells out to the system `diff` (present on every platform
// this project targets, same "use the real tool" comfort already
// established for `git`/`aws` elsewhere in this codebase) rather than
// vendoring a diff algorithm just for this one CLI report.
func unifiedDiff(path string, newContent []byte) (string, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	oldFile, err := os.CreateTemp("", "ubx-writeback-before-*.tf")
	if err != nil {
		return "", err
	}
	defer os.Remove(oldFile.Name())
	if _, err := oldFile.Write(before); err != nil {
		return "", err
	}
	oldFile.Close()

	newFile, err := os.CreateTemp("", "ubx-writeback-after-*.tf")
	if err != nil {
		return "", err
	}
	defer os.Remove(newFile.Name())
	if _, err := newFile.Write(newContent); err != nil {
		return "", err
	}
	newFile.Close()

	out, err := exec.Command("diff", "-u",
		"--label", path+" (before)", "--label", path+" (after)",
		oldFile.Name(), newFile.Name(),
	).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		// diff exits 1 to report "files differ" -- that's the expected,
		// successful outcome here, not a failure.
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return string(out), nil
		}
		return "", fmt.Errorf("%w: %s", err, out)
	}
	return string(out), nil
}
