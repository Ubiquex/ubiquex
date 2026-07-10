package cli

import "testing"

func TestNewRootCmd_HasVersionSubcommand(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"version"})
	if err != nil {
		t.Fatalf("find version cmd: %v", err)
	}
	if cmd.Name() != "version" {
		t.Fatalf("got %q, want %q", cmd.Name(), "version")
	}
}
