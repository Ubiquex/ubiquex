package gotmpl

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// ubxFixtureSource is a hermetic, real-shaped stand-in for
// github.com/ubiquex/ubx-sdk-go/runtime -- the exact public surface
// (ResourceBinding/DataSourceBinding/FieldSpec/FieldMap/Resource/Data/
// Computed) confirmed live against the real, separate ubx-sdk-go repo
// before writing this, kept deliberately minimal (no Intent/Override/
// stack() machinery a generated binding file itself never references).
//
// This exists for exactly one reason, found the hard way (UBI-186):
// go build/deno check/ast.parse on a generated repo ALONE never once
// exercises Resource()/Data() -- a generated file only DECLARES a
// binding var, it never calls the function that consumes it, so a
// wrong binding type (ubx.ResourceBinding where ubx.DataSourceBinding
// was required) compiled clean through every one of those three checks
// and only broke when a real consumer program was written by hand
// against the real runtime. This package pins that discovery into the
// generator's own test suite: ResourceBinding and DataSourceBinding
// here are genuinely distinct Go types (never a shared alias), so a
// generated binding passed to the wrong one of Resource()/Data() fails
// to compile, exactly like the real runtime -- see
// TestConsumer_WrongBindingKind_FailsToCompile below for the proof this
// distinction is load-bearing, not just present.
const ubxFixtureSource = `package runtime

type FieldSpec struct {
	WireName string
	Kind     string
	Fields   FieldMap
}

type FieldMap map[string]FieldSpec

type ResourceBinding struct {
	WireType string
	Fields   FieldMap
}

type DataSourceBinding struct {
	WireType string
	Fields   FieldMap
}

type Computed struct{ address string }

func (c *Computed) Address() string { return c.address }

func Resource(binding ResourceBinding, name string, config any) *Computed {
	return &Computed{address: binding.WireType + "." + name}
}

func Data(binding DataSourceBinding, name string, lookup any) *Computed {
	return &Computed{address: binding.WireType + "." + name}
}
`

// writeConsumerModule lays out files (a GeneratedRepo result) plus the
// hermetic ubx fixture as two real, separate Go modules under dir, with
// a replace directive wiring the generated module's own
// "require github.com/ubiquex/ubx-sdk-go" at the fixture -- mirrors
// exactly the real replace directive this session's own live Amplify
// docs-page verification used against the real, separate ubx-sdk-go
// checkout, just pointed at this hermetic fixture instead so the test
// has no network/submodule dependency.
func writeConsumerModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	// The real ubx-sdk-go repo's own package lives at "runtime/" below
	// its module root (generated code imports
	// "github.com/ubiquex/ubx-sdk-go/runtime", never the bare module
	// path) -- the fixture mirrors that exact layout, not just the
	// module name, so the replace directive resolves the identical
	// import path a real consumer uses.
	fixtureDir := filepath.Join(dir, "ubx-fixture")
	fixtureRuntimeDir := filepath.Join(fixtureDir, "runtime")
	if err := os.MkdirAll(fixtureRuntimeDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", fixtureRuntimeDir, err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "go.mod"), []byte("module github.com/ubiquex/ubx-sdk-go\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write fixture go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixtureRuntimeDir, "runtime.go"), []byte(ubxFixtureSource), 0o644); err != nil {
		t.Fatalf("write fixture runtime.go: %v", err)
	}

	goModPath := filepath.Join(dir, "sdk", "go", "go.mod")
	modEdit := exec.Command("go", "mod", "edit",
		"-require", "github.com/ubiquex/ubx-sdk-go@v0.0.0",
		"-replace", "github.com/ubiquex/ubx-sdk-go=../../ubx-fixture",
		goModPath)
	if out, err := modEdit.CombinedOutput(); err != nil {
		t.Fatalf("go mod edit: %v\n%s", err, out)
	}
}

// runGoBuild runs `go build ./...` from dir/sdk/go, returning combined
// output and whether it succeeded -- the actual, real compile step
// go build/deno check/ast.parse on a generated repo ALONE never
// exercises (see ubxFixtureSource's own doc comment).
func runGoBuild(t *testing.T, dir string) (string, bool) {
	t.Helper()
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = filepath.Join(dir, "sdk", "go")
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// TestConsumer_ResourceAndDataSource_RealCompile is the real consumer
// program this package's own generated output has to satisfy: a
// resource binding passed to Resource(), a data source binding of the
// SAME wire type passed to Data() (hashicorp/aws's own real
// aws_instance shape, both a resource and a data source), against a
// hermetic but genuinely nominally-typed stand-in for the real runtime.
// If codegen ever again emits ubx.ResourceBinding for a data source (or
// vice versa), this fails to compile -- the exact class of bug
// go build/deno check/ast.parse on the generated repo alone cannot see.
func TestConsumer_ResourceAndDataSource_RealCompile(t *testing.T) {
	resource := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resource.RealNamespace = "ec2"

	dataSource := rt("aws_instance", scalarField("id", ir.ScalarString, true, false, false, false))
	dataSource.RealNamespace = "ec2"
	dataSource.IsDataSource = true

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resource, dataSource}, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	dir := t.TempDir()
	writeConsumerModule(t, dir, files)

	consumer := `package consumertest

import (
	ubx "github.com/ubiquex/ubx-sdk-go/runtime"
	"github.com/ubiquex/ubx-sdk-aws/sdk/go/aws/ec2"
	dataec2 "github.com/ubiquex/ubx-sdk-aws/sdk/go/aws/data/ec2"
)

func UseBoth() {
	ubx.Resource(ec2.Instance, "example", ec2.InstanceConfig{})
	ubx.Data(dataec2.Instance, "example", dataec2.InstanceConfig{})
}
`
	consumerPath := filepath.Join(dir, "sdk", "go", "consumertest", "consumer.go")
	if err := os.MkdirAll(filepath.Dir(consumerPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatalf("write consumer.go: %v", err)
	}

	out, ok := runGoBuild(t, dir)
	if !ok {
		t.Fatalf("real consumer program failed to compile against the generated resource + data source bindings:\n%s", out)
	}
}

// TestConsumer_WrongBindingKind_FailsToCompile is
// TestConsumer_ResourceAndDataSource_RealCompile's own negative-path
// proof that the check above has real teeth: passing the RESOURCE's
// own binding to Data() (the exact swap this whole test file exists to
// catch) must fail to compile against the hermetic fixture, the same
// way it fails against the real runtime (confirmed live this session:
// go build genuinely rejects a ResourceBinding where a DataSourceBinding
// is required -- unlike TypeScript's structural typing, which does NOT
// catch this swap, confirmed by direct deno check test; see this
// repo's own PR/session notes for that finding).
func TestConsumer_WrongBindingKind_FailsToCompile(t *testing.T) {
	resource := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resource.RealNamespace = "ec2"

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resource}, "1.23")
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	dir := t.TempDir()
	writeConsumerModule(t, dir, files)

	// Deliberately wrong: ec2.Instance is a ResourceBinding, but this
	// passes it to Data(), which requires a DataSourceBinding.
	consumer := `package consumertest

import (
	ubx "github.com/ubiquex/ubx-sdk-go/runtime"
	"github.com/ubiquex/ubx-sdk-aws/sdk/go/aws/ec2"
)

func Misuse() {
	ubx.Data(ec2.Instance, "example", ec2.InstanceConfig{})
}
`
	consumerPath := filepath.Join(dir, "sdk", "go", "consumertest", "consumer.go")
	if err := os.MkdirAll(filepath.Dir(consumerPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatalf("write consumer.go: %v", err)
	}

	out, ok := runGoBuild(t, dir)
	if ok {
		t.Fatalf("expected passing a ResourceBinding to Data() to fail to compile, but go build succeeded:\n%s", out)
	}
}
