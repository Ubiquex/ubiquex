package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd_PrintsVersion(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != Version {
		t.Fatalf("got %q, want %q", got, Version)
	}
}

func TestVersionCmd_Overridden(t *testing.T) {
	orig := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = orig })

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != "1.2.3" {
		t.Fatalf("got %q, want %q", got, "1.2.3")
	}
}
