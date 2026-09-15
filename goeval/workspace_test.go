package goeval

import (
	"context"
	"golang.org/x/mod/modfile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkspaceMonorepo builds a repo holding a workspace and two
// modules: an SDK program, and a library it reaches through the
// workspace rather than through its own go.mod.
//
// That is the ordinary reason to have a workspace at all, and the case
// that could not be evaluated before this.
func writeWorkspaceMonorepo(t *testing.T, workspaceAt string, extra string) (repo, entry string) {
	t.Helper()
	sdkGo, err := filepath.Abs("../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	repo = t.TempDir()
	stack := filepath.Join(repo, "stack")
	lib := filepath.Join(repo, "lib")
	for _, d := range []string{stack, lib} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(lib, "go.mod"), "module example.com/lib\n\ngo 1.23\n")
	write(filepath.Join(lib, "lib.go"), "package lib\n\nfunc Name() string { return \"w1\" }\n")
	write(filepath.Join(stack, "go.mod"),
		"module example.com/stack\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n\nreplace github.com/ubiquex/ubx-sdk-go => "+sdkGo+"\n")
	write(filepath.Join(stack, "main.go"), `package main

import (
	"example.com/lib"
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack("demo", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "monorepo"})
		sdk.Resource(
			sdk.ResourceBinding{WireType: "fake_widget", Fields: sdk.FieldMap{"Name": {WireName: "name"}}},
			lib.Name(),
			struct {
				Name string
			}{Name: lib.Name()},
		)
	}))
}
`)

	switch workspaceAt {
	case "parent":
		// The common monorepo shape: workspace at the repo root, modules
		// beneath it. Not copied with the module, so the workspace
		// vanished entirely and example.com/lib became unresolvable.
		write(filepath.Join(repo, "go.work"), "go 1.23\n\nuse (\n\t./stack\n\t./lib\n)\n"+extra)
	case "beside":
		// Workspace in the module's own directory, so it IS copied, and
		// workspace mode then made GOFLAGS=-mod=mod illegal.
		write(filepath.Join(stack, "go.work"), "go 1.23\n\nuse (\n\t.\n\t"+lib+"\n)\n"+extra)
	default:
		t.Fatalf("unknown workspace placement %q", workspaceAt)
	}
	return repo, filepath.Join(stack, "main.go")
}

// TestEvaluate_WorkspaceAboveModuleRoot is the layout that lost its
// workspace in the copy: every module the workspace provided became
// unresolvable, with GOPROXY=off naming the proxy rather than the real
// cause.
func TestEvaluate_WorkspaceAboveModuleRoot(t *testing.T) {
	_, entry := writeWorkspaceMonorepo(t, "parent", "")
	doc, err := Evaluate(context.Background(), entry)
	if err != nil {
		t.Fatalf("a program in a monorepo workspace must evaluate: %v", err)
	}
	if !strings.Contains(string(doc), "fake_widget") {
		t.Fatalf("evaluated document is missing the resource: %s", doc)
	}
}

// TestEvaluate_WorkspaceBesideGoMod is the layout that WAS copied, where
// workspace mode made this evaluator's own GOFLAGS=-mod=mod illegal.
func TestEvaluate_WorkspaceBesideGoMod(t *testing.T) {
	_, entry := writeWorkspaceMonorepo(t, "beside", "")
	if _, err := Evaluate(context.Background(), entry); err != nil {
		t.Fatalf("a program whose module root holds the workspace must evaluate: %v", err)
	}
}

// TestEvaluate_WorkspaceReplaceIsPreserved: a workspace `replace` is how
// people point at a module that is not published, which is the same
// monorepo case this fix is about. Dropping it while keeping `use` would
// turn a working build into "cannot find module providing package" with
// nothing naming the cause, the same shape a merged import map exists to
// avoid.
func TestEvaluate_WorkspaceReplaceIsPreserved(t *testing.T) {
	repo, entry := writeWorkspaceMonorepo(t, "parent", "")

	// A module the program requires, provided ONLY by a workspace
	// replace pointing at a local directory.
	helper := filepath.Join(repo, "helper")
	if err := os.MkdirAll(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, s := range map[string]string{
		filepath.Join(helper, "go.mod"):    "module example.com/helper\n\ngo 1.23\n",
		filepath.Join(helper, "helper.go"): "package helper\n\nfunc Tag() string { return \"tagged\" }\n",
	} {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Declared as a requirement, satisfied by the workspace replace.
	stackMod := filepath.Join(filepath.Dir(entry), "go.mod")
	data, err := os.ReadFile(stackMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stackMod, append(data, []byte("\nrequire example.com/helper v0.0.0\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(repo, "go.work")
	if err := os.WriteFile(work,
		[]byte("go 1.23\n\nuse (\n\t./stack\n\t./lib\n)\n\nreplace example.com/helper => ./helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Import it, so the replace has to actually resolve.
	main := filepath.Join(filepath.Dir(entry), "main.go")
	src, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(src),
		`"example.com/lib"`, "\"example.com/helper\"\n\t\"example.com/lib\"", 1)
	patched = strings.Replace(patched, "lib.Name(),\n\t\t\tstruct", "lib.Name()+helper.Tag(),\n\t\t\tstruct", 1)
	if err := os.WriteFile(main, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Evaluate(context.Background(), entry); err != nil {
		t.Fatalf("a workspace replace must survive the module copy: %v", err)
	}
}

// TestFindWorkspace_GoworkOff: GOWORK=off means no workspace, which has
// to be honoured or this would force one on a program that disabled it.
func TestFindWorkspace_GoworkOff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, workspaceFileName), []byte("go 1.23\n\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findWorkspace(dir); got == "" {
		t.Fatal("a go.work in the module root should be found")
	}
	t.Setenv("GOWORK", "off")
	if got := findWorkspace(dir); got != "" {
		t.Fatalf("GOWORK=off must disable workspace mode, got %q", got)
	}
}

// TestWriteBuildWorkspace_GoDirectiveTakesTheHighestMember is the bug a
// declared blueprint exposed in this file's own first version.
//
// The workspace's go directive used to come from the program's own
// go.mod, on the reasoning that a workspace cannot demand less than what
// the program was written against. True, and not the whole rule: Go also
// refuses a workspace that lists a module wanting MORE than the
// workspace declares.
//
//	go: module .../blueprint listed in go.work file requires
//	go >= 1.26.3, but go.work lists go 1.23
//
// A blueprint is published independently of the stacks that call it, so
// being built against a newer toolchain is ordinary rather than an edge.
func TestWriteBuildWorkspace_GoDirectiveTakesTheHighestMember(t *testing.T) {
	writeMod := func(dir, mod, goVersion string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "module " + mod + "\n"
		if goVersion != "" {
			body += "\ngo " + goVersion + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	root := t.TempDir()
	build := filepath.Join(root, "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	moduleRoot := writeMod(filepath.Join(root, "stack"), "example.com/stack", "1.23")
	moduleCopy := writeMod(filepath.Join(build, "stack"), "example.com/stack", "1.23")
	newer := writeMod(filepath.Join(root, "bp-newer"), "example.com/newer", "1.26.3")
	older := writeMod(filepath.Join(root, "bp-older"), "example.com/older", "1.21")

	t.Setenv("GOWORK", "off")
	dest, err := writeBuildWorkspace(build, moduleRoot, moduleCopy, []string{newer, older})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := modfile.ParseWork(dest, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wf.Go == nil {
		t.Fatalf("the workspace needs a go directive, or it implicitly requires 1.18:\n%s", data)
	}
	if wf.Go.Version != "1.26.3" {
		t.Fatalf("go directive = %q, want 1.26.3, the highest any member asks for:\n%s", wf.Go.Version, data)
	}
}

// TestHighestGoDirective_ComparesAsVersionsNotStrings: string ordering
// puts "1.9" above "1.23", which would synthesize a workspace that
// refuses the very module it was built for.
func TestHighestGoDirective_ComparesAsVersionsNotStrings(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("module example.com/m\n\ngo "+v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := highestGoDirective(nil, []string{write("a", "1.9"), write("b", "1.23")}); got != "1.23" {
		t.Errorf("got %q, want 1.23: 1.23 is newer than 1.9 despite sorting before it", got)
	}
	// A go.mod with no directive, or one that is not there at all, is not
	// an error and must not become the answer.
	missing := filepath.Join(dir, "absent", "go.mod")
	if got := highestGoDirective(nil, []string{missing}); got != "" {
		t.Errorf("got %q, want no directive at all", got)
	}
}
