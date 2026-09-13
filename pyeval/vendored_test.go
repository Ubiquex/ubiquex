package pyeval

import (
	"os"
	"path/filepath"
	"testing"
)

// vendored_test.go is tseval/vendored_test.go's own sibling, for the
// same reason and with the same discipline (UBI-254): pyeval embeds a
// COPY of a file that really lives in ubx-sdk-python, because the Go
// module proxy zips a repository without submodule contents and the
// original is therefore absent from the published module.
//
// See tseval/vendored_test.go for the full account.
func TestVendoredAssetsMatchTheSubmodule(t *testing.T) {
	vendored := filepath.Join("vendored", "ubx_sdk", "__init__.py")
	submodule := filepath.Join("..", "sdk", "py", "ubx_sdk", "__init__.py")

	source, err := os.ReadFile(submodule)
	if os.IsNotExist(err) {
		t.Skipf("%s is not checked out, so there is nothing to compare against", submodule)
	}
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(vendored)
	if err != nil {
		t.Fatalf("the vendored copy is missing or unreadable, so the published module would not build: %v", err)
	}
	if string(copied) != string(source) {
		t.Errorf("%s has drifted from %s.\nRe-sync it (make vendor-assets) rather than editing the copy: the submodule is the source, the copy exists only so the Go module proxy can see it.\nvendored=%d bytes, source=%d bytes", vendored, submodule, len(copied), len(source))
	}
}
