package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is the ubx build version: a released tag like "0.1.0" for a
// goreleaser build, "dev" for anything else. Overridden at build time via
// -ldflags "-X github.com/ubiquex/ubiquex/cli.Version=...".
var Version = "dev"

// Commit is the short (7-char) commit SHA the binary was built from.
// Overridden at build time via
// -ldflags "-X github.com/ubiquex/ubiquex/cli.Commit=...". Left empty
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

// buildInfoDirty reports whether Go's toolchain stamped this binary as
// built from a working tree with uncommitted changes on top of
// buildInfoRevision's own commit ("vcs.modified" == "true") -- false if
// unknown or clean. UBI-63 session 5: a real, live confusion this would
// have caught immediately -- a fix existed only as an uncommitted
// change, and the commit suffix alone can't distinguish "built from
// exactly that commit" from "built from that commit plus whatever's
// currently sitting uncommitted on top of it," which is precisely what
// a rebuild-before-retesting workflow needs to know. A package var for
// the same override-in-tests reason as buildInfoRevision.
var buildInfoDirty = func() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.modified" {
			return s.Value == "true"
		}
	}
	return false
}

// versionString is what `ubx version` prints: Version alone if no commit
// is known from either source, otherwise "<Version>+<commit>" (with a
// "-dirty" suffix when the build has uncommitted changes on top of that
// commit) -- the same shape whether Version is a released tag
// ("0.1.0+abcdef1") or "dev" ("dev+abcdef1"), so a local build is never
// ambiguous about exactly what it was built from.
func versionString() string {
	commit := Commit
	if commit == "" {
		commit = buildInfoRevision()
	}
	if commit == "" {
		return Version
	}
	v := Version + "+" + commit
	if buildInfoDirty() {
		v += "-dirty"
	}
	return v
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
