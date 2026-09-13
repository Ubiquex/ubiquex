package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sdkgen_strict_test.go runs a real schema-consuming path against the
// strict-v6 fixture (UBI-252).
//
// Asserting that the strict fixture is narrow, which
// provider/fakeprovider_strict_test.go does, is only half the value. The
// other half is running something real against it, because narrowness
// only matters where ubx's own code meets it.
//
// codegen is the right path to pick: it consumes the whole schema and
// needs no apply, so it exercises realistic narrowness end to end
// through the actual CLI. Every previous generation went through a
// fixture whose resources all carried a convenient "id", which 0 of
// 1687 real AWS dynamic-provider types do.
func TestSDKGen_AgainstAResourceWithNoID(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	env := []string{
		"FAKEPROVIDER_MODE=strict-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}
	out, err := runUbx(t, env, "sdk", "gen", "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("ubx sdk gen against strict-v6: %v\noutput: %s", err, out)
	}

	// Both strict resources have to generate: the one with a required
	// attribute and the one with none. The second is the 14% case that
	// no permissive fixture can reach.
	var generated []string
	err = filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			generated = append(generated, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) == 0 {
		t.Fatalf("nothing generated:\n%s", out)
	}

	joined := strings.Join(generated, "\n")
	for _, want := range []string{"widget", "opaque"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no bindings generated for %q, so the no-id case was skipped rather than handled:\n%s", want, joined)
		}
	}

	// And nothing invented an Id field for resources that have none.
	for _, path := range generated {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(body), "\tId ") || strings.Contains(string(body), "\tID ") {
			t.Errorf("%s declares an Id field for a resource whose schema has none", path)
		}
	}
}
