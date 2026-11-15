package cli

import (
	"os/exec"
	"testing"
)

// runGit runs git in dir, failing the test on error. It lived with the
// pr_merge verify tests until UBI-300 removed them.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
