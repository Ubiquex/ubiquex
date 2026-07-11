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
	origVersion, origCommit := Version, Commit
	Version, Commit = "1.2.3", "abc1234"
	t.Cleanup(func() { Version, Commit = origVersion, origCommit })

	if got, want := runVersion(t), "1.2.3+abc1234"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionCmd_FallsBackToBuildInfoRevisionWhenCommitUnset(t *testing.T) {
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
