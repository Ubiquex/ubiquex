package tfwrite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

func mod(addr core.Address, after map[string]json.RawMessage) core.Modification {
	return core.Modification{Target: addr, After: after}
}

func raw(v string) json.RawMessage { return json.RawMessage(v) }

const sampleTF = `resource "aws_instance" "web" {
  # important comment about instance type
  instance_type = "t3.medium" # inline note
  ami           = var.ami_id
  name          = "web-${var.env}"
  tags = {
    hotfix = "true" # a comment on hotfix
    owner  = "team-a"
  }
  tags2 = {
    hotfix = "true"
    dyn    = var.owner
  }
  ports = [80, 443]
}
`

func TestApplyModification_TopLevelScalar_PreservesRestOfFile(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	if report.Applied[0] != "instance_type" {
		t.Fatalf("Applied = %v, want [instance_type]", report.Applied)
	}
	got := string(out)
	if !strings.Contains(got, `instance_type = "t3.large" # inline note`) {
		t.Fatalf("expected the value changed but the inline comment kept, got:\n%s", got)
	}
	// Everything else byte-identical.
	wantUnchanged := []string{
		"# important comment about instance type",
		`ami           = var.ami_id`,
		`name          = "web-${var.env}"`,
		`hotfix = "true" # a comment on hotfix`,
		`owner  = "team-a"`,
		`ports = [80, 443]`,
	}
	for _, want := range wantUnchanged {
		if !strings.Contains(got, want) {
			t.Errorf("expected unrelated content to survive untouched: %q\ngot:\n%s", want, got)
		}
	}
}

func TestApplyModification_NestedMapKey_PreservesSiblingsAndComments(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"false"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	got := string(out)
	if !strings.Contains(got, `hotfix = "false" # a comment on hotfix`) {
		t.Fatalf("expected hotfix changed with its comment preserved, got:\n%s", got)
	}
	if !strings.Contains(got, `owner  = "team-a"`) {
		t.Errorf("expected the sibling key untouched, got:\n%s", got)
	}
	if !strings.Contains(got, `instance_type = "t3.medium" # inline note`) {
		t.Errorf("expected unrelated attribute untouched, got:\n%s", got)
	}
}

func TestApplyModification_ListAttribute_AtomicReplace(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"ports": raw(`[80,443,8080]`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	if !strings.Contains(string(out), "ports = [80, 443, 8080]") {
		t.Fatalf("expected the whole list replaced, got:\n%s", out)
	}
}

func TestApplyModification_DeclinesExpression_LeavesFileUntouched(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"ami": raw(`"ami-0123456789"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Applied) != 0 {
		t.Fatalf("Applied = %v, want none", report.Applied)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "ami" {
		t.Fatalf("Declined = %+v, want exactly one entry for \"ami\"", report.Declined)
	}
	if report.Declined[0].Expression != "var.ami_id" {
		t.Errorf("Expression = %q, want %q", report.Declined[0].Expression, "var.ami_id")
	}
	if string(out) != sampleTF {
		t.Fatalf("expected the file completely untouched when the only attribute is declined")
	}
}

func TestApplyModification_DeclinesInterpolatedString(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"name": raw(`"web-prod"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "name" {
		t.Fatalf("Declined = %+v, want exactly one entry for \"name\" (a template with interpolation is not a literal)", report.Declined)
	}
}

func TestApplyModification_LiteralKeyAppliedDespiteNonLiteralSibling(t *testing.T) {
	// tags2 has one literal key (hotfix) and one non-literal (dyn = var.owner).
	// Targeting the literal one must succeed regardless of its sibling.
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags2.hotfix": raw(`"false"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	if !strings.Contains(string(out), "dyn    = var.owner") {
		t.Fatalf("expected the non-literal sibling to survive completely untouched, got:\n%s", out)
	}
}

func TestApplyModification_DeclinesNonLiteralSibling(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags2.dyn": raw(`"static-now"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "tags2.dyn" {
		t.Fatalf("Declined = %+v, want exactly one entry for tags2.dyn", report.Declined)
	}
}

func TestApplyModification_DeclinesKeyNotPresent(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.newkey": raw(`"x"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "tags.newkey" {
		t.Fatalf("Declined = %+v, want exactly one entry (write-back never adds new keys)", report.Declined)
	}
}

func TestApplyModification_DeclinesAttributeNotPresent(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"monitoring": raw(`true`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "monitoring" {
		t.Fatalf("Declined = %+v, want exactly one entry (write-back never adds new attributes)", report.Declined)
	}
}

func TestApplyModification_DeclinesNestedPathIntoNonObject(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"instance_type.sub": raw(`"x"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "instance_type.sub" {
		t.Fatalf("Declined = %+v, want exactly one entry (instance_type isn't an object)", report.Declined)
	}
}

func TestApplyModification_PartialApplication_MixOfAppliedAndDeclined(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{
			"instance_type": raw(`"t3.large"`),
			"ami":           raw(`"ami-new"`), // will be declined
		}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0] != "instance_type" {
		t.Fatalf("Applied = %v, want [instance_type]", report.Applied)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "ami" {
		t.Fatalf("Declined = %+v, want [ami]", report.Declined)
	}
	if !strings.Contains(string(out), `instance_type = "t3.large"`) {
		t.Errorf("expected instance_type applied, got:\n%s", out)
	}
	if !strings.Contains(string(out), "ami           = var.ami_id") {
		t.Errorf("expected ami left untouched, got:\n%s", out)
	}
}

func TestApplyModification_ResourceBlockAbsent(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "does-not-exist"}
	_, _, err := ApplyModification([]byte(sampleTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if !errors.Is(err, ErrResourceBlockNotFound) {
		t.Fatalf("got %v, want ErrResourceBlockNotFound", err)
	}
}

const duplicateBlockTF = `resource "aws_instance" "web" {
  instance_type = "t3.medium"
}
resource "aws_instance" "web" {
  instance_type = "t3.small"
}
`

func TestApplyModification_MultipleMatchingBlocks(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, _, err := ApplyModification([]byte(duplicateBlockTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if !errors.Is(err, ErrMultipleResourceBlocks) {
		t.Fatalf("got %v, want ErrMultipleResourceBlocks", err)
	}
}

// weirdFormattingTF exercises unusual-but-valid HCL: tabs for indentation,
// no spaces around "=", a compact same-line object literal, and a trailing
// comment on the closing brace.
const weirdFormattingTF = "resource \"aws_instance\" \"web\" {\n" +
	"\tinstance_type=\"t3.medium\"\n" +
	"\ttags={hotfix=\"true\",owner=\"team-a\"}\n" +
	"} # trailing\n"

func TestApplyModification_WeirdButValidFormattingSurvives(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(weirdFormattingTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"false"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	want := "resource \"aws_instance\" \"web\" {\n" +
		"\tinstance_type=\"t3.medium\"\n" +
		"\ttags={hotfix=\"false\",owner=\"team-a\"}\n" +
		"} # trailing\n"
	if string(out) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestFindAndApply_SearchesDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "network.tf"), "resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n}\n")
	writeFile(t, filepath.Join(dir, "compute.tf"), sampleTF)

	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	path, newContent, report, err := FindAndApply(dir, addr, mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if err != nil {
		t.Fatalf("FindAndApply: %v", err)
	}
	if filepath.Base(path) != "compute.tf" {
		t.Fatalf("path = %q, want compute.tf", path)
	}
	if len(report.Applied) != 1 {
		t.Fatalf("Applied = %v", report.Applied)
	}
	if !strings.Contains(string(newContent), `instance_type = "t3.large"`) {
		t.Fatalf("expected the change applied, got:\n%s", newContent)
	}
}

func TestFindAndApply_NoMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "network.tf"), "resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n}\n")

	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, _, _, err := FindAndApply(dir, addr, mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if !errors.Is(err, ErrResourceBlockNotFound) {
		t.Fatalf("got %v, want ErrResourceBlockNotFound", err)
	}
}

func TestFindAndApply_MatchesAcrossTwoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.tf"), "resource \"aws_instance\" \"web\" {\n  instance_type = \"t3.medium\"\n}\n")
	writeFile(t, filepath.Join(dir, "b.tf"), "resource \"aws_instance\" \"web\" {\n  instance_type = \"t3.small\"\n}\n")

	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, _, _, err := FindAndApply(dir, addr, mod(addr, map[string]json.RawMessage{"instance_type": raw(`"t3.large"`)}))
	if !errors.Is(err, ErrMultipleResourceBlocks) {
		t.Fatalf("got %v, want ErrMultipleResourceBlocks", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
