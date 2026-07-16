package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGenTool_CommittedHintsAreUpToDate guards against core/lookuphints'
// committed, generated file silently drifting from conformance.Registry's
// own LookupHint field (UBI-20 workstream 3) -- a hand-edit to either side
// without re-running `go generate ./conformance/...` would otherwise go
// unnoticed until someone actually hit the stale hint in production.
func TestGenTool_CommittedHintsAreUpToDate(t *testing.T) {
	committed, err := os.ReadFile("../core/lookuphints/hints.go")
	if err != nil {
		t.Fatalf("read committed core/lookuphints/hints.go: %v", err)
	}

	regenerated := filepath.Join(t.TempDir(), "hints.go")
	cmd := exec.Command("go", "run", "./gentool", "-out", regenerated)
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go run ./gentool: %v\n%s", err, out)
	}

	fresh, err := os.ReadFile(regenerated)
	if err != nil {
		t.Fatalf("read regenerated file: %v", err)
	}

	if string(committed) != string(fresh) {
		t.Fatalf("core/lookuphints/hints.go is stale -- re-run `go generate ./conformance/...` and commit the result")
	}
}
