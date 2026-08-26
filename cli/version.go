package cli

import (
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"

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

// currentGitHEAD returns the short (7-char) commit SHA of the git repo
// the current working directory is inside, or "" if the cwd isn't
// inside one (or `git` itself isn't on PATH) -- a package var, the same
// override-in-tests shape buildInfoRevision/buildInfoDirty already use,
// so checkBuildFreshness's own tests never depend on a real git
// checkout's actual state.
var currentGitHEAD = func() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(out))
	if len(head) < 7 {
		return ""
	}
	return head[:7]
}

// checkBuildFreshness is UBI-186 follow-up's own real, live-found fix:
// a full six-provider `ubx sdk gen` regeneration once ran end to end
// against a binary built from a stale commit (a PR branch's own tip,
// created before a real fix had merged into main) -- every generated
// data source silently carried the wrong binding type, and nothing
// caught the mismatch before generation started, only an unrelated
// manual spot-check hours later. This closes that gap: called at the
// top of `sdk gen`'s own RunE, before any real generation work begins.
//
// Deliberately compares against the CURRENT git HEAD of whatever
// checkout the command is running from, not literally "main" -- an
// active development session legitimately builds and tests from a
// feature branch (this whole real fix was found and built that way),
// and requiring literal "main" would false-positive on that entirely
// normal workflow. "Binary was built from exactly what's currently
// checked out, whatever that is" is the real, general property that
// actually prevents the failure mode found live: build, then `git
// checkout` something else (a different branch, or the same branch
// after a merge landed) without rebuilding.
//
// Only activates for a local "dev" build with a known VCS-stamped
// commit -- Version != "dev" means a real, released/tagged build,
// which was never built against anyone's local checkout and has
// nothing meaningful to compare against. Skips silently (returns nil,
// not an error) when the commit or the git HEAD can't be determined at
// all -- built outside a git checkout, VCS stamping disabled, git not
// on PATH, or cwd genuinely outside any git repo -- rather than
// blocking a use this check was never meant to police.
func checkBuildFreshness() error {
	if Version != "dev" {
		return nil
	}
	built := buildInfoRevision()
	if built == "" {
		return nil
	}
	head := currentGitHEAD()
	if head == "" {
		return nil
	}
	if built == head {
		return nil
	}
	return fmt.Errorf(
		"ubx was built from commit %s, but this checkout's current HEAD is %s -- rebuild (`make build`) before running generation. A stale binary here previously ran a full multi-provider regeneration silently emitting the wrong codegen output for every data source (UBI-186 follow-up) before anyone noticed",
		built, head,
	)
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
