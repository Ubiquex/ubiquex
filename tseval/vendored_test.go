package tseval

import (
	"os"
	"path/filepath"
	"testing"
)

// vendored_test.go is the thing that makes vendoring safe (UBI-254).
//
// tseval embeds a COPY of two files that really live in
// ubx-sdk-typescript, checked out here as the sdk/ts submodule. The
// copy exists because the Go module proxy zips a repository WITHOUT
// submodule contents, so importing github.com/ubiquex/ubiquex/blueprint
// from anywhere outside this repository failed to build: sdk/ts and
// sdk/py were simply absent from the published module.
//
// Two copies of the same content is a real hazard, and this project has
// been bitten by it before, so the copy is only defensible while
// something proves the two agree. That is this test, and it is why it
// was written before the copy existed rather than after.
//
// It runs for real in CI, which checks out submodules (ci.yml's own
// `submodules: true`). It skips only when the submodule is genuinely
// absent, which is a shallow or submodule-less checkout, never a
// divergence.
func TestVendoredAssetsMatchTheSubmodule(t *testing.T) {
	cases := []struct {
		vendored  string
		submodule string
	}{
		{filepath.Join("vendored", "evaluator", "guards.ts"), filepath.Join("..", "sdk", "ts", "evaluator", "guards.ts")},
		{filepath.Join("vendored", "runtime", "src", "index.ts"), filepath.Join("..", "sdk", "ts", "runtime", "src", "index.ts")},
	}

	for _, c := range cases {
		t.Run(c.vendored, func(t *testing.T) {
			source, err := os.ReadFile(c.submodule)
			if os.IsNotExist(err) {
				t.Skipf("%s is not checked out, so there is nothing to compare against", c.submodule)
			}
			if err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(c.vendored)
			if err != nil {
				t.Fatalf("the vendored copy is missing or unreadable, so the published module would not build: %v", err)
			}
			if string(copied) != string(source) {
				t.Errorf("%s has drifted from %s.\nRe-sync it (make vendor-assets) rather than editing the copy: the submodule is the source, the copy exists only so the Go module proxy can see it.\nvendored=%d bytes, source=%d bytes", c.vendored, c.submodule, len(copied), len(source))
			}
		})
	}
}
