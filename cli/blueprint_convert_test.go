package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const convertTestModule = `
variable "name" {
  type    = string
  default = "app"
}
variable "cidr" {
  type    = string
  default = "10.0.0.0/16"
}
variable "azs" {
  type = list(string)
}
variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_vpc" "this" {
  cidr_block           = var.cidr
  enable_dns_hostnames = true
  tags                 = merge({ Name = var.name }, var.tags)
}

resource "aws_subnet" "private" {
  count = length(var.azs)

  vpc_id             = aws_vpc.this.id
  availability_zone  = element(var.azs, count.index)
  cidr_block         = cidrsubnet(var.cidr, 8, count.index)
}

output "vpc_id" {
  value = aws_vpc.this.id
}
`

func TestBlueprintConvert_EndToEnd(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), convertTestModule)

	outDir := filepath.Join(t.TempDir(), "vpc-net")
	out, err := runUbx(t, nil, "blueprint", "convert", "--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("blueprint convert: %v\noutput:\n%s", err, out)
	}

	// No AI adapter was configured at all -- if convert had accidentally
	// called the intent-provider pipeline, this test would fail with an
	// adapter/credentials error instead of succeeding, so success itself
	// is part of the "genuinely no AI call" proof.

	if !strings.Contains(out, "converted 2 resource(s) (0 skipped)") {
		t.Fatalf("output = %q, want a converted-resource-count line", out)
	}
	// map(string)/merge() are both real, deliberate gaps -- confirm they
	// surface as named questions, not silently dropped.
	if !strings.Contains(out, `variable "tags" declares type map(string)`) {
		t.Fatalf("output missing the map(string) question:\n%s", out)
	}
	if !strings.Contains(out, "function call merge(") {
		t.Fatalf("output missing the merge() question:\n%s", out)
	}

	for _, want := range []string{"go/go.mod", "go/bindings.go", "go/cidrsubnet.go", "go/vpcnet.go", "Ubxfile"} {
		if _, err := os.Stat(filepath.Join(outDir, want)); err != nil {
			t.Fatalf("expected %s to be written: %v", want, err)
		}
	}

	ubxfile, err := os.ReadFile(filepath.Join(outDir, "Ubxfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ubxfile)
	for _, want := range []string{"lang: go", "azs: list(string), required", "cidr: string, default \"10.0.0.0/16\"", "resources: |"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Ubxfile missing %q:\n%s", want, text)
		}
	}
	// tags was dropped (unsupported type) -- must not appear as a params: entry.
	if strings.Contains(text, "tags:") {
		t.Fatalf("Ubxfile should not declare the dropped tags param:\n%s", text)
	}
	// outputs: is empty -- this module has a for_each resource, and
	// vpc_id's own output was declined for that reason (see the CLI
	// output assertion above); confirm it didn't sneak into the Ubxfile.
	if strings.Contains(text, "outputs:") {
		t.Fatalf("Ubxfile should declare no outputs: (for_each+outputs boundary):\n%s", text)
	}

	fn, err := os.ReadFile(filepath.Join(outDir, "go", "vpcnet.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fn), "cidrsubnet(cfg.cidr, 8, azsIndex)") {
		t.Fatalf("vpcnet.go missing the expected cidrsubnet call:\n%s", fn)
	}
}

func TestBlueprintConvert_RequiresFromTerraformAndOut(t *testing.T) {
	if _, err := runUbx(t, nil, "blueprint", "convert", "--out", t.TempDir()); err == nil {
		t.Fatal("expected an error when --from-terraform is missing")
	}
	if _, err := runUbx(t, nil, "blueprint", "convert", "--from-terraform", t.TempDir()); err == nil {
		t.Fatal("expected an error when --out is missing")
	}
}

func TestBlueprintConvert_UnrecognizedCountSkipsResourceLoudly(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), `
resource "aws_instance" "x" {
  count = 3
  ami   = "ami-123"
}
`)
	outDir := filepath.Join(t.TempDir(), "skipped")
	out, err := runUbx(t, nil, "blueprint", "convert", "--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("blueprint convert: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "converted 0 resource(s) (1 skipped)") {
		t.Fatalf("output = %q, want a 0-converted/1-skipped line", out)
	}
	if !strings.Contains(out, "isn't a recognized shape") {
		t.Fatalf("output missing the skipped-resource question:\n%s", out)
	}
}

// TestBlueprintConvert_GeneratedPackageBuilds is this ticket's own
// required "the generated Go actually compiles" proof, hermetic (no
// provider schema fetch, `go build` only) -- the real hashicorp/aws
// resolve proof lives in STATE.md's own live-verification account
// (--source hashicorp/aws is a real network call, not suitable for the
// hermetic suite `go test ./...` must stay).
func TestBlueprintConvert_GeneratedPackageBuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH")
	}
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), convertTestModule)

	outDir := filepath.Join(t.TempDir(), "vpc-net")
	if _, err := runUbx(t, nil, "blueprint", "convert", "--from-terraform", modDir, "--out", outDir, "--lang", "go"); err != nil {
		t.Fatalf("blueprint convert: %v", err)
	}

	sdkGoRoot := sdkGoModuleRootForCLI(t)
	goModPath := filepath.Join(outDir, "go", "go.mod")
	existing, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goModPath, append(existing, []byte("\nreplace github.com/ubiquex/ubx-sdk-go => "+sdkGoRoot+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = filepath.Join(outDir, "go")
	tidy.Env = append(os.Environ(), "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = filepath.Join(outDir, "go")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the converted blueprint package failed: %v\n%s", err, out)
	}
}

func sdkGoModuleRootForCLI(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", "sdk", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected %s to be sdk/go's own module root: %v", root, err)
	}
	return root
}
