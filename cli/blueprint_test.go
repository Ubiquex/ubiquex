package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// blueprintTestDraft is a pre-resolved intent/v1 document -- the SAME
// wire shape "ubx resolve --from-code --out <file>" produces -- standing
// in for what a blueprint author now writes themselves (via the SDK, or
// "ubx blueprint convert") before checking an Ubxfile in. UBI-224
// removed blueprint build's own intent-provider draft step, so build no
// longer interprets prose -- resources: below always points straight at
// this file.
const blueprintTestDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform blueprint"},
  "resources": [
    {"type": "aws_ecr_repository", "name": "ci-artifacts", "op": "create", "config": {"name": "{repo_name}"}}
  ]
}`

// writeBlueprintTestResources writes blueprintTestDraft to dir/resources.json
// and returns an Ubxfile's own resources: value pointing at it.
func writeBlueprintTestResources(t *testing.T, dir string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, "resources.json"), blueprintTestDraft)
	return "resources.json"
}

func TestBlueprintBuild_WritesCompilableGoPackage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	out, err := runUbx(t, nil, "blueprint", "build", dir, "--lang", "go")
	if err != nil {
		t.Fatalf("blueprint build: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "built 1 resource(s)") {
		t.Fatalf("output = %q, want a built-resource-count line", out)
	}

	for _, want := range []string{"go/go.mod", "go/bindings.go", "go/ciplatform.go"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Fatalf("expected %s to be written: %v", want, err)
		}
	}
	// --lang go alone must not also produce ts/py output.
	if _, err := os.Stat(filepath.Join(dir, "ts")); !os.IsNotExist(err) {
		t.Fatalf("--lang go should not produce a ts/ directory (err = %v)", err)
	}

	fn, err := os.ReadFile(filepath.Join(dir, "go", "ciplatform.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fn), "func CiPlatform(repoName string) {") {
		t.Fatalf("ciplatform.go missing expected signature:\n%s", fn)
	}
}

// TestBlueprintBuild_DefaultLangBuildsAllThree confirms Slice 4's own
// resolved "--lang default" design: omitting --lang entirely builds ALL
// THREE languages from the SAME single parsed resources: document.
func TestBlueprintBuild_DefaultLangBuildsAllThree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	out, err := runUbx(t, nil, "blueprint", "build", dir)
	if err != nil {
		t.Fatalf("blueprint build (no --lang): %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "go, ts, py") {
		t.Fatalf("output = %q, want it to name all three languages", out)
	}
	for _, want := range []string{"go/ciplatform.go", "ts/ciplatform.ts", "py/ciplatform.py"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Fatalf("expected %s to be written: %v", want, err)
		}
	}
}

func TestBlueprintBuild_InvalidLangFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nresources: hello\n")
	if _, err := runUbx(t, nil, "blueprint", "build", dir, "--lang", "rust"); err == nil {
		t.Fatal("expected an error for --lang rust, got nil")
	}
}

func TestBlueprintBuild_MissingUbxfile(t *testing.T) {
	dir := t.TempDir()
	if _, err := runUbx(t, nil, "blueprint", "build", dir); err == nil {
		t.Fatal("expected an error when no Ubxfile exists, got nil")
	}
}

func TestBlueprintBuild_DefaultsToCurrentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if _, err := runUbx(t, nil, "blueprint", "build", "--lang", "go"); err != nil {
		t.Fatalf("blueprint build (no dir arg): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go", "ciplatform.go")); err != nil {
		t.Fatalf("expected go/ciplatform.go to be written into the current directory: %v", err)
	}
}

func writeBuiltBlueprintDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: hello\n")
	// Nested under go/ (Slice 4: sibling per-language output directories),
	// mirroring what `ubx blueprint build --lang go` itself now writes.
	if err := os.MkdirAll(filepath.Join(dir, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go", "go.mod"), "module ciplatform\n\ngo 1.23\n")
	writeFile(t, filepath.Join(dir, "go", "ciplatform.go"), "package ciplatform\n\nfunc CIPlatform(repoName string) {}\n")
	return dir
}

func TestBlueprintPackageVerify_RoundTrip(t *testing.T) {
	dir := writeBuiltBlueprintDir(t)
	out := filepath.Join(t.TempDir(), "ci-platform.tar.gz")

	packOut, err := runUbx(t, nil, "blueprint", "package", dir, "-o", out)
	if err != nil {
		t.Fatalf("blueprint package: %v\noutput:\n%s", err, packOut)
	}
	if !strings.Contains(packOut, "content hash sha256:") {
		t.Fatalf("blueprint package output missing a content hash: %q", packOut)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected tarball at %s: %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blueprint.lock.json")); err != nil {
		t.Fatalf("expected blueprint.lock.json to be written into dir: %v", err)
	}

	verifyOut, err := runUbx(t, nil, "blueprint", "verify", dir)
	if err != nil {
		t.Fatalf("blueprint verify: %v\noutput:\n%s", err, verifyOut)
	}
	if !strings.Contains(verifyOut, "matches") {
		t.Fatalf("blueprint verify output = %q, want a match confirmation", verifyOut)
	}
}

func TestBlueprintVerify_TamperedFile_Fails(t *testing.T) {
	dir := writeBuiltBlueprintDir(t)
	out := filepath.Join(t.TempDir(), "ci-platform.tar.gz")
	if _, err := runUbx(t, nil, "blueprint", "package", dir, "-o", out); err != nil {
		t.Fatalf("blueprint package: %v", err)
	}

	writeFile(t, filepath.Join(dir, "go", "ciplatform.go"), "package ciplatform\n\n// tampered\n")

	if _, err := runUbx(t, nil, "blueprint", "verify", dir); err == nil {
		t.Fatal("blueprint verify: expected an error after tampering, got nil")
	}
}

func TestBlueprintPull_Local(t *testing.T) {
	src := writeBuiltBlueprintDir(t)
	dest := filepath.Join(t.TempDir(), "pulled")

	out, err := runUbx(t, nil, "blueprint", "pull", src, dest)
	if err != nil {
		t.Fatalf("blueprint pull: %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "Ubxfile")); err != nil {
		t.Fatalf("expected pulled dest to contain Ubxfile: %v", err)
	}
}

func TestBlueprintPull_LocalToGitToVerify_RealGoBuild(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Build a real blueprint package, exactly the same as
	// TestBlueprintBuild_WritesCompilableGoPackage.
	built := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(built, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, built)
	writeFile(t, filepath.Join(built, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")
	if _, err := runUbx(t, nil, "blueprint", "build", built, "--lang", "go"); err != nil {
		t.Fatalf("blueprint build: %v", err)
	}

	// Package it -- writes blueprint.lock.json into built/.
	if _, err := runUbx(t, nil, "blueprint", "package", built, "-o", filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("blueprint package: %v", err)
	}

	// "Push" to a real (throwaway, local) git repo -- a plain git init +
	// commit, matching how a founder would push a real built blueprint
	// directory into a real repo.
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "--quiet")
	if err := copyTree(t, built, filepath.Join(repoDir, "ci-platform")); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("-c", "user.name=ubx-test", "-c", "user.email=ubx-test@example.com", "commit", "--quiet", "-m", "add ci-platform blueprint")

	// Pull from a SEPARATE directory, via file:// so Pull genuinely takes
	// the git-clone path rather than a plain local-directory copy.
	dest := filepath.Join(t.TempDir(), "pulled-ci-platform")
	pullOut, err := runUbx(t, nil, "blueprint", "pull", "file://"+repoDir, dest, "--path", "ci-platform")
	if err != nil {
		t.Fatalf("blueprint pull: %v\noutput:\n%s", err, pullOut)
	}

	if _, err := runUbx(t, nil, "blueprint", "verify", dest); err != nil {
		t.Fatalf("blueprint verify (pulled copy): %v", err)
	}

	// A real `go build` against the pulled copy -- the ticket's own bar
	// for "genuinely usable," not just "the right bytes are present."
	// Hermetic: append a `replace` for ubx-sdk-go onto this repo's own
	// local sdk/go, exactly like blueprint/gogen_test.go's own
	// TestGenerateGo_CompilesClean already does (a local replace + GOPROXY=off,
	// no network, no dependency on the real published module).
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	sdkGoDir := filepath.Join(repoRoot, "sdk", "go")
	if _, err := os.Stat(sdkGoDir); err != nil {
		t.Skipf("sdk/go not found at %s, skipping compile check: %v", sdkGoDir, err)
	}
	goModPath := filepath.Join(dest, "go", "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = append(goMod, []byte("\nreplace github.com/ubiquex/ubx-sdk-go => "+sdkGoDir+"\n")...)
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}

	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = filepath.Join(dest, "go")
	buildCmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build (pulled copy): %v: %s", err, out)
	}
}

// copyTree is a small, test-only recursive copy (cli's own test helpers
// have no existing one) -- mirrors blueprint.Pull's own local-copy
// behavior closely enough for this test's purpose (simulating "push a
// built blueprint directory into a git repo").
func copyTree(t *testing.T, src, dest string) error {
	t.Helper()
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, raw, 0o644)
	})
}
