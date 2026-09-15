package cli

import (
	"bytes"
	"strings"
	"testing"
)

// noBuildInfoRevision stubs out buildInfoRevision for tests that don't
// want the real running-under-`go test` VCS stamp leaking into their
// expectations.
func noBuildInfoRevision(t *testing.T) {
	t.Helper()
	orig := buildInfoRevision
	buildInfoRevision = func() string { return "" }
	t.Cleanup(func() { buildInfoRevision = orig })
}

// noBuildInfoDirty stubs out buildInfoDirty for tests asserting an exact
// "+commit" string with no "-dirty" suffix, so they stay deterministic
// regardless of whether the real working tree this test binary was
// compiled from happens to have uncommitted changes.
func noBuildInfoDirty(t *testing.T) {
	t.Helper()
	orig := buildInfoDirty
	buildInfoDirty = func() bool { return false }
	t.Cleanup(func() { buildInfoDirty = orig })
}

func runVersion(t *testing.T) string {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return strings.TrimSpace(out.String())
}

func TestVersionCmd_PrintsPlainVersion_NoCommitAvailable(t *testing.T) {
	noBuildInfoRevision(t)

	if got, want := runVersion(t), Version; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionCmd_Overridden(t *testing.T) {
	noBuildInfoRevision(t)
	orig := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = orig })

	if got, want := runVersion(t), "1.2.3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionCmd_WithCommit(t *testing.T) {
	noBuildInfoRevision(t)
	noBuildInfoDirty(t)
	origVersion, origCommit := Version, Commit
	Version, Commit = "1.2.3", "abc1234"
	t.Cleanup(func() { Version, Commit = origVersion, origCommit })

	if got, want := runVersion(t), "1.2.3+abc1234"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionCmd_FallsBackToBuildInfoRevisionWhenCommitUnset(t *testing.T) {
	noBuildInfoDirty(t)
	origCommit := Commit
	Commit = ""
	t.Cleanup(func() { Commit = origCommit })
	origFn := buildInfoRevision
	buildInfoRevision = func() string { return "deadbee" }
	t.Cleanup(func() { buildInfoRevision = origFn })

	origVersion := Version
	Version = "dev"
	t.Cleanup(func() { Version = origVersion })

	if got, want := runVersion(t), "dev+deadbee"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionCmd_ExplicitCommitTakesPrecedenceOverBuildInfo(t *testing.T) {
	noBuildInfoDirty(t)
	origFn := buildInfoRevision
	buildInfoRevision = func() string { return "shouldnotbeused" }
	t.Cleanup(func() { buildInfoRevision = origFn })

	origVersion, origCommit := Version, Commit
	Version, Commit = "1.2.3", "abc1234"
	t.Cleanup(func() { Version, Commit = origVersion, origCommit })

	if got, want := runVersion(t), "1.2.3+abc1234"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestVersionCmd_DirtyBuild_AppendsDirtySuffix is UBI-63 session 5's own
// coverage for the new "-dirty" indicator: a real, live confusion this
// would have caught immediately -- a fix landed only as an uncommitted
// change, and a rebuild's own version string gave no signal that
// uncommitted work was baked in on top of the last commit.
func TestVersionCmd_DirtyBuild_AppendsDirtySuffix(t *testing.T) {
	noBuildInfoRevision(t)
	origVersion, origCommit := Version, Commit
	Version, Commit = "1.2.3", "abc1234"
	t.Cleanup(func() { Version, Commit = origVersion, origCommit })
	origDirty := buildInfoDirty
	buildInfoDirty = func() bool { return true }
	t.Cleanup(func() { buildInfoDirty = origDirty })

	if got, want := runVersion(t), "1.2.3+abc1234-dirty"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// withDevVersion sets Version to "dev" (checkBuildFreshness's own real
// activation condition -- a released/tagged build has nothing local to
// compare against) for the duration of one test.
func withDevVersion(t *testing.T) {
	t.Helper()
	orig := Version
	Version = "dev"
	t.Cleanup(func() { Version = orig })
}

func withBuildCommit(t *testing.T, commit string) {
	t.Helper()
	orig := buildInfoRevision
	buildInfoRevision = func() string { return commit }
	t.Cleanup(func() { buildInfoRevision = orig })
}

func withGitHEAD(t *testing.T, head string) {
	t.Helper()
	orig := currentGitHEAD
	currentGitHEAD = func() string { return head }
	t.Cleanup(func() { currentGitHEAD = orig })
}

// TestCheckBuildFreshness_MatchingCommit_NoError is the real, common
// case: a binary built from exactly what's currently checked out (the
// normal state right after `make build`) must never block generation.
func TestCheckBuildFreshness_MatchingCommit_NoError(t *testing.T) {
	withDevVersion(t)
	withBuildCommit(t, "abc1234")
	withGitHEAD(t, "abc1234")

	if err := checkBuildFreshness(); err != nil {
		t.Fatalf("expected no error when build commit matches HEAD, got: %v", err)
	}
}

// TestCheckBuildFreshness_StaleCommit_RealError is UBI-186 follow-up's
// own real, live-found failure, reproduced directly: a binary built
// from one commit, run against a checkout whose HEAD has since moved
// (a `git checkout` to a different branch or a merge landing on the
// same branch, without a rebuild in between) -- exactly what let a full
// six-provider regeneration run end to end emitting the wrong binding
// type before anyone noticed. Must fail loud, not silently proceed.
func TestCheckBuildFreshness_StaleCommit_RealError(t *testing.T) {
	withDevVersion(t)
	withBuildCommit(t, "7bcff87")
	withGitHEAD(t, "028451e")

	err := checkBuildFreshness()
	if err == nil {
		t.Fatal("expected an error when the build commit no longer matches HEAD, got nil")
	}
	if !strings.Contains(err.Error(), "7bcff87") || !strings.Contains(err.Error(), "028451e") {
		t.Fatalf("expected the error to name both the stale build commit and the current HEAD, got: %v", err)
	}
}

// TestCheckBuildFreshness_ReleasedBuild_NeverChecked is the real scope
// boundary: Version != "dev" means a real, released/tagged build (a
// goreleaser artifact, say) -- it was never built against anyone's
// local checkout, so comparing it against whatever git repo happens to
// be in the current working directory would be meaningless, not a real
// staleness signal.
func TestCheckBuildFreshness_ReleasedBuild_NeverChecked(t *testing.T) {
	origVersion := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = origVersion })
	withBuildCommit(t, "abc1234")
	withGitHEAD(t, "0000000")

	if err := checkBuildFreshness(); err != nil {
		t.Fatalf("expected a released build to never be checked, got: %v", err)
	}
}

// TestCheckBuildFreshness_NoKnownBuildCommit_SkipsSilently covers a dev
// build with no VCS stamp at all (built with -buildvcs=false, or
// outside a git checkout) -- there is nothing to compare, so this must
// not block generation.
func TestCheckBuildFreshness_NoKnownBuildCommit_SkipsSilently(t *testing.T) {
	withDevVersion(t)
	withBuildCommit(t, "")
	withGitHEAD(t, "0000000")

	if err := checkBuildFreshness(); err != nil {
		t.Fatalf("expected no error with an unknown build commit, got: %v", err)
	}
}

// TestCheckBuildFreshness_NoGitRepo_SkipsSilently covers running ubx
// from outside any git checkout (or without git on PATH) -- this check
// exists to catch a stale LOCAL DEVELOPMENT build, not to require every
// real usage of ubx to happen inside a git repo.
func TestCheckBuildFreshness_NoGitRepo_SkipsSilently(t *testing.T) {
	withDevVersion(t)
	withBuildCommit(t, "abc1234")
	withGitHEAD(t, "")

	if err := checkBuildFreshness(); err != nil {
		t.Fatalf("expected no error when the current HEAD can't be determined, got: %v", err)
	}
}

// TestWarnIfBinaryOlderThanCheckout is the three states, and the two
// silent ones are the point.
//
// checkBuildFreshness refuses on ANY difference, which is right for the
// one rare, expensive command it guards and unusable everywhere else: it
// would fire the moment anyone commits, and a warning that is usually
// noise is one people learn to skip. That would leave the rare real case
// scrolling past with the rest.
func TestWarnIfBinaryOlderThanCheckout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		version    string
		built      string
		head       string
		isAncestor bool
		wantWarn   bool
	}{
		{
			name: "strictly behind: the case that cost time twice",
			// A binary built before commits that have since landed, run
			// in the checkout that contains them. A stale binary and a
			// broken build look identical here, and the output is
			// plausible either way.
			version: "dev", built: "aaaaaaa", head: "bbbbbbb", isAncestor: true, wantWarn: true,
		},
		{
			name:    "equal: nothing to say",
			version: "dev", built: "aaaaaaa", head: "aaaaaaa", isAncestor: true, wantWarn: false,
		},
		{
			// A branch that was squashed and deleted leaves a binary
			// whose commit this repository has never seen. Ordinary, and
			// not staleness.
			name:    "unrelated: a commit this repo has never seen",
			version: "dev", built: "aaaaaaa", head: "bbbbbbb", isAncestor: false, wantWarn: false,
		},
		{
			// A released build was never built against anyone's checkout
			// and has nothing to compare against.
			name:    "a released build says nothing",
			version: "0.6.0", built: "aaaaaaa", head: "bbbbbbb", isAncestor: true, wantWarn: false,
		},
		{
			name:    "outside a git checkout",
			version: "dev", built: "aaaaaaa", head: "", isAncestor: true, wantWarn: false,
		},
		{
			name:    "no VCS stamp at all",
			version: "dev", built: "", head: "bbbbbbb", isAncestor: true, wantWarn: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer swapFreshnessProbes(tc.version, tc.built, tc.head, tc.isAncestor)()

			var buf bytes.Buffer
			warnIfBinaryOlderThanCheckout(&buf)

			if got := buf.Len() > 0; got != tc.wantWarn {
				t.Fatalf("warned = %v, want %v (output %q)", got, tc.wantWarn, buf.String())
			}
			if !tc.wantWarn {
				return
			}
			// Both commits, or the reader cannot tell which way round it
			// is or what to compare against.
			for _, want := range []string{tc.built, tc.head, "make build"} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("the warning does not name %q: %q", want, buf.String())
				}
			}
		})
	}
}

// TestWarnIfBinaryOlderThanCheckout_AsksGitOnlyWhenItHasTo: the ancestry
// probe is a subprocess, and the equal case is by far the most common
// one, so it must not pay for it.
func TestWarnIfBinaryOlderThanCheckout_AsksGitOnlyWhenItHasTo(t *testing.T) {
	calls := 0
	defer swapFreshnessProbes("dev", "aaaaaaa", "aaaaaaa", true)()
	restore := binaryIsAncestorOfHEAD
	binaryIsAncestorOfHEAD = func(string) bool { calls++; return true }
	defer func() { binaryIsAncestorOfHEAD = restore }()

	var buf bytes.Buffer
	warnIfBinaryOlderThanCheckout(&buf)
	if calls != 0 {
		t.Errorf("an equal commit must not shell out to git, got %d call(s)", calls)
	}
}

// swapFreshnessProbes replaces the three package vars the check reads and
// returns a func restoring them, so no test depends on a real checkout.
func swapFreshnessProbes(version, built, head string, isAncestor bool) func() {
	oldVersion, oldBuilt, oldHead, oldAnc := Version, buildInfoRevision, currentGitHEAD, binaryIsAncestorOfHEAD
	Version = version
	buildInfoRevision = func() string { return built }
	currentGitHEAD = func() string { return head }
	binaryIsAncestorOfHEAD = func(string) bool { return isAncestor }
	return func() {
		Version, buildInfoRevision, currentGitHEAD, binaryIsAncestorOfHEAD = oldVersion, oldBuilt, oldHead, oldAnc
	}
}
