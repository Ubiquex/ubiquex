package cli

// ExitCodeError lets a command's RunE request a specific process exit code
// -- the CI contract (UBI-20, docs/architecture.md — Hardening pass: exit
// codes, docs/exit-codes.mdx) applies to every verb, not just `ubx status`
// where it started (UBI-17): 0 success/clean (nothing to flag), 1 an
// actionable finding (drift found, a stale/parent-mismatched proposal, a
// rejected pr_merge acceptance claim, declined write-back/revert-plan
// attributes), 2 an error (bad input, I/O, provider failure, ledger
// failure). Any plain (non-ExitCodeError) error returned from a command's
// RunE falls through to exit 2 in cmd/ubx/main.go -- a command that hasn't
// been audited into this contract yet still exits non-zero, just at the
// "something went wrong" code, never the misleading-if-unaudited 1. Err
// may be nil: "drift found" isn't itself a Go error, just a reason to exit
// non-zero after already printing a normal report.
//
// This can't be `os.Exit` called directly inside a command's RunE --
// cli/scan_test.go's runUbx (and every other CLI test in this codebase)
// executes commands in-process via cobra's own Execute(), so an in-process
// os.Exit would kill the test binary itself, not just "the command". A
// value RunE returns, which main.go interprets, keeps the exit-code
// contract testable as an ordinary Go test.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return ""
}

func (e *ExitCodeError) Unwrap() error { return e.Err }
