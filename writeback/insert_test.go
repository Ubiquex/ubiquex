package writeback

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

const multilineNoCommaTF = `resource "aws_instance" "web" {
  tags = {
    owner = "team-a"
  }
}
`

func TestInsert_MultilineNoTrailingComma(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(multilineNoCommaTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	want := `resource "aws_instance" "web" {
  tags = {
    owner = "team-a"
    hotfix = "true"
  }
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	assertReparsesCleanly(t, out)
}

const multilineTrailingCommaTF = `resource "aws_instance" "web" {
  tags = {
    owner = "team-a",
  }
}
`

func TestInsert_MultilinePreservesTrailingCommaStyle(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(multilineTrailingCommaTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	want := `resource "aws_instance" "web" {
  tags = {
    owner = "team-a",
    hotfix = "true",
  }
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	assertReparsesCleanly(t, out)
}

const singleLineMapTF = `resource "aws_instance" "web" {
  tags = { owner = "team-a" }
}
`

func TestInsert_SingleLineMap(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(singleLineMapTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	want := `resource "aws_instance" "web" {
  tags = { owner = "team-a", hotfix = "true" }
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	assertReparsesCleanly(t, out)
}

const emptyMapTF = `resource "aws_instance" "web" {
  tags = {}
}
`

func TestInsert_EmptyMap(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(emptyMapTF), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	want := `resource "aws_instance" "web" {
  tags = {
    hotfix = "true"
  }
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	assertReparsesCleanly(t, out)
}

func TestInsert_PreservesCommentOnPreviousLastItem(t *testing.T) {
	src := `resource "aws_instance" "web" {
  tags = {
    owner = "team-a" # keep this
  }
}
`
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	out, report, err := ApplyModification([]byte(src), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	if !strings.Contains(string(out), `owner = "team-a" # keep this`) {
		t.Fatalf("expected the comment on the previously-last item to survive, got:\n%s", out)
	}
	if !strings.Contains(string(out), `hotfix = "true"`) {
		t.Fatalf("expected the new key inserted, got:\n%s", out)
	}
	assertReparsesCleanly(t, []byte(out))
}

func TestInsert_DeclinesWhenMapIsAnExpression(t *testing.T) {
	src := `resource "aws_instance" "web" {
  tags = merge(local.common_tags, { owner = "team-a" })
}
`
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(src), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "tags.hotfix" {
		t.Fatalf("Declined = %+v, want exactly one entry (tags is a function call, not a literal object)", report.Declined)
	}
}

func TestInsert_DeclinesWhenMapIsAVariableReference(t *testing.T) {
	src := `resource "aws_instance" "web" {
  tags = var.common_tags
}
`
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(src), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.hotfix": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "tags.hotfix" {
		t.Fatalf("Declined = %+v, want exactly one entry (tags is a variable reference, not a literal object)", report.Declined)
	}
}

func TestInsert_DeclinesMissingIntermediateSegment(t *testing.T) {
	// "tags" exists as a literal object, but "tags.nested" (an intermediate
	// segment before the final ".deep") doesn't -- inserting a whole new
	// nested structure stays out of scope, unlike inserting one leaf key.
	src := `resource "aws_instance" "web" {
  tags = {
    owner = "team-a"
  }
}
`
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(src), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"tags.nested.deep": raw(`"true"`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "tags.nested.deep" {
		t.Fatalf("Declined = %+v, want exactly one entry (missing intermediate segment, not just the final key)", report.Declined)
	}
}

func TestInsert_StillDeclinesAbsentTopLevelAttribute(t *testing.T) {
	// Unchanged from before this follow-up: an attribute that doesn't
	// exist AT ALL is still declined, never inserted.
	src := `resource "aws_instance" "web" {
  tags = {
    owner = "team-a"
  }
}
`
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	_, report, err := ApplyModification([]byte(src), "test.tf", addr,
		mod(addr, map[string]json.RawMessage{"monitoring": raw(`true`)}))
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 1 || report.Declined[0].Path != "monitoring" {
		t.Fatalf("Declined = %+v, want exactly one entry (write-back never adds new attributes)", report.Declined)
	}
}

func TestInsert_TwoNewKeysIntoSameMap_BothPresentAndDeterministic(t *testing.T) {
	addr := core.Address{Stack: "s", Type: "aws_instance", Name: "web"}
	m := mod(addr, map[string]json.RawMessage{
		"tags.hotfix": raw(`"true"`),
		"tags.owner2": raw(`"team-b"`),
	})

	out1, report, err := ApplyModification([]byte(multilineNoCommaTF), "test.tf", addr, m)
	if err != nil {
		t.Fatalf("ApplyModification: %v", err)
	}
	if len(report.Declined) != 0 {
		t.Fatalf("Declined = %+v, want none", report.Declined)
	}
	if len(report.Applied) != 2 {
		t.Fatalf("Applied = %v, want 2 entries", report.Applied)
	}
	if !strings.Contains(string(out1), `hotfix = "true"`) || !strings.Contains(string(out1), `owner2 = "team-b"`) {
		t.Fatalf("expected both new keys present, got:\n%s", out1)
	}
	assertReparsesCleanly(t, out1)

	// Determinism: running it again produces byte-identical output.
	out2, _, err := ApplyModification([]byte(multilineNoCommaTF), "test.tf", addr, m)
	if err != nil {
		t.Fatalf("ApplyModification (second run): %v", err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("non-deterministic output across two identical runs:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", out1, out2)
	}
}

func assertReparsesCleanly(t *testing.T, out []byte) {
	t.Helper()
	if _, err := parseBody(out, "reparse-check.tf"); err != nil {
		t.Fatalf("output does not re-parse as valid HCL: %v\n%s", err, out)
	}
}
