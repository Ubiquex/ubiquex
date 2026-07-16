package cli

// ExitCodeError lets a command's RunE request a specific process exit code
// -- for `ubx status`'s CI contract (docs/architecture.md — Fleet status:
// 0 clean, 1 drift, 2 unreadable/error), the blanket "any error means exit
// 1" every other command relies on (cmd/ubx/main.go) isn't expressive
// enough. Err may be nil: "drift found" isn't itself a Go error, just a
// reason to exit non-zero after already printing a normal report.
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
