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
	// This module's only resource is skipped, so nothing is converted,
	// which is now a failure rather than a quiet success -- see
	// TestBlueprintConvert_NothingConverted_Fails for why. The loudness
	// this test exists to check is unchanged and still asserted below.
	if err == nil {
		t.Fatalf("expected converting zero resources to fail:\n%s", out)
	}
	requireExitCode(t, err, 1, out)
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

// convertNothingModule is a module where nothing converts: a DERIVED
// conditional count (`length(var.x) > 0`), which is deliberately not
// supported because it is not a declared bool param and so has no
// create_if term to name, plus an output wrapped in try(). Reduced from
// terraform-aws-modules/terraform-aws-sqs, which uses this form too.
//
// This fixture has moved twice as create_if widened: it originally used
// `count = var.create ? 1 : 0`, then `var.create && var.create_dlq`,
// both of which now convert. The derived form is the boundary the
// conjunction shape deliberately does not grow past.
const convertNothingModule = `
variable "create" {
  type    = bool
  default = true
}

variable "redrive_policy" {
  type = list(string)
}

resource "aws_sqs_queue" "this" {
  count      = var.create && length(var.redrive_policy) > 0 ? 1 : 0
  queue_name = "q"
}

output "arn" {
  value = try(aws_sqs_queue.this[0].arn, null)
}
`

// Converting nothing is a failed conversion, not a quiet one.
//
// Measured 2026-09-08: converting terraform-aws-sqs skipped all eight
// resources, dropped all ten outputs, printed "converted 0 resource(s)
// (8 skipped)" and exited 0. The empty blueprint it left behind builds,
// packages and content-hashes exactly like a real one, so nothing
// downstream would have caught it either.
func TestBlueprintConvert_NothingConverted_Fails(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), convertNothingModule)
	outDir := filepath.Join(t.TempDir(), "bp")

	out, err := runUbx(t, nil, "blueprint", "convert",
		"--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err == nil {
		t.Fatalf("converting zero resources must not report success:\n%s", out)
	}
	requireExitCode(t, err, 1, out)

	all := out + errText(err)
	// The verdict has to name the consequence, not just the count.
	for _, want := range []string{"nothing was converted", "is empty and describes none of that module"} {
		if !strings.Contains(all, want) {
			t.Errorf("error does not say %q:\n%s", want, all)
		}
	}
	// And the questions explaining WHY must still be shown, since the
	// error deliberately does not repeat them.
	if !strings.Contains(all, "isn't a recognized shape") {
		t.Errorf("the questions explaining which constructs were refused were lost:\n%s", all)
	}
}

// A module that converts at least one resource still succeeds, so the
// new check cannot turn a partial conversion into a failure. Partial is
// the normal case and is already reported through questions.
func TestBlueprintConvert_PartialConversion_StillSucceeds(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), convertNothingModule+`
resource "aws_sqs_queue" "plain" {
  queue_name = "plain"
}
`)
	outDir := filepath.Join(t.TempDir(), "bp")

	out, err := runUbx(t, nil, "blueprint", "convert",
		"--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("a partial conversion must still succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "converted 1 resource(s)") {
		t.Fatalf("expected 1 converted resource, got:\n%s", out)
	}
}

// The summary states what a conversion preserved, not only how many
// resources it produced.
//
// "converted N resource(s)" was a misleading measure. Converting
// terraform-aws-modules/terraform-aws-sqs reported six resources, of
// which four retained exactly one attribute each, all of them region,
// having lost queue_url and their entire policy document. The questions
// said so individually, 71 of them, and nothing summarised it.
func TestBlueprintConvert_ReportsAttributeRetention(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), `
variable "keep" {
  type = string
}

resource "fake_widget" "gutted" {
  name = var.keep
  tags = jsonencode({ a = 1 })
  size = try(var.missing, null)
}
`)
	outDir := filepath.Join(t.TempDir(), "bp")
	out, err := runUbx(t, nil, "blueprint", "convert",
		"--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("blueprint convert: %v\n%s", err, out)
	}
	if !strings.Contains(out, "attribute(s) retained") {
		t.Fatalf("summary does not report attribute retention:\n%s", out)
	}
	// A resource that lost most of itself is named, with what remains, so
	// the reader does not have to reconstruct it from the questions.
	if !strings.Contains(out, "lost more than half their attributes") {
		t.Fatalf("summary does not name the gutted resource:\n%s", out)
	}
	if !strings.Contains(out, "kept 1 of 3 (name)") {
		t.Fatalf("summary does not say what actually survived:\n%s", out)
	}
}

// A resource that lost an attribute its provider marks Required is
// refused, the same rule as an empty conversion one level down. A
// fake_widget without "name" would fail at the provider or create
// something meaningless.
func TestBlueprintConvert_RefusesResourceMissingRequiredAttribute(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), `
resource "fake_widget" "nameless" {
  name = try(var.nope, null)
  tags = {}
}
`)
	outDir := filepath.Join(t.TempDir(), "bp")
	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "blueprint", "convert",
		"--from-terraform", modDir, "--out", outDir, "--lang", "go",
		"--check-against-provider", fakeProviderBinary)

	// Every resource was refused, so nothing converted, which the existing
	// zero-resource rule turns into a failure.
	all := out + errText(err)
	if !strings.Contains(all, "lost required attribute(s) name") {
		t.Fatalf("expected the resource to be refused for its missing required attribute:\n%s", all)
	}
	if !strings.Contains(all, "would fail at the provider or create something meaningless") {
		t.Errorf("refusal does not say why it matters:\n%s", all)
	}
}

// Opt-in: without a provider to check against, no required-attribute
// check runs and the conversion is reported as-is. The converter has no
// schema of its own, and a converted blueprint resolves against whatever
// the calling stack configures, which is unknowable at convert time.
func TestBlueprintConvert_WithoutCheckAgainst_NoRequiredCheck(t *testing.T) {
	modDir := t.TempDir()
	writeFile(t, filepath.Join(modDir, "main.tf"), `
resource "fake_widget" "nameless" {
  name = try(var.nope, null)
  tags = {}
}
`)
	outDir := filepath.Join(t.TempDir(), "bp")
	out, err := runUbx(t, nil, "blueprint", "convert",
		"--from-terraform", modDir, "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("without --check-against the conversion must proceed: %v\n%s", err, out)
	}
	if strings.Contains(out, "lost required attribute") {
		t.Fatalf("a required-attribute check ran without being asked for:\n%s", out)
	}
}
