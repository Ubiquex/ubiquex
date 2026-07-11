package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is the ubx build version: a released tag like "0.1.0" for a
// goreleaser build, "dev" for anything else. Overridden at build time via
// -ldflags "-X github.com/ubiquex/ubiquex-cli/cli.Version=...".
var Version = "dev"

// Commit is the short (7-char) commit SHA the binary was built from.
// Overridden at build time via
// -ldflags "-X github.com/ubiquex/ubiquex-cli/cli.Commit=...". Left empty
// for a plain `go build` with no ldflags -- versionString falls back to
// buildInfoRevision in that case, which reads the same information from
// Go's own VCS build-info stamping (present automatically for any build
// run inside a git checkout).
var Commit = ""

// buildInfoRevision returns the short commit SHA Go's toolchain stamped
// into the running binary, or "" if none is available (built outside a
// git checkout, or VCS stamping disabled via -buildvcs=false). A package
// var, not a plain call, so tests can override it instead of depending on
// the real build/VCS state.
var buildInfoRevision = func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}

// versionString is what `ubx version` prints: Version alone if no commit
// is known from either source, otherwise "<Version>+<commit>" -- the same
// shape whether Version is a released tag ("0.1.0+abcdef1") or "dev"
// ("dev+abcdef1"), so a local build is never ambiguous about exactly what
// it was built from.
func versionString() string {
	commit := Commit
	if commit == "" {
		commit = buildInfoRevision()
	}
	if commit == "" {
		return Version
	}
	return Version + "+" + commit
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ubx version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return nil
		},
	}
}
