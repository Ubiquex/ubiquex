package blueprint

import (
	"strings"
	"testing"
)

// UBI-134: fast, unit-level coverage for parseCrossRefArg/renderArgLiteral's
// own ParamCrossRef case -- cli/blueprint_crossref_test.go covers the
// real, end-to-end CLI path (all three calling mediums, a real $cross
// "stack" mode resolution against a real neighbor); this file covers the
// parsing/rendering logic itself in isolation, no Deno/hermetic sandbox
// needed.

func TestParseCrossRefArg_WellFormed(t *testing.T) {
	stackName, to, err := parseCrossRefArg("@networking.aws_vpc.main.id")
	if err != nil {
		t.Fatalf("parseCrossRefArg: %v", err)
	}
	if stackName != "networking" {
		t.Errorf("stackName = %q, want \"networking\"", stackName)
	}
	if to != "networking.aws_vpc.main.id" {
		t.Errorf("to = %q, want \"networking.aws_vpc.main.id\"", to)
	}
}

func TestParseCrossRefArg_DeeperAttrPath(t *testing.T) {
	stackName, to, err := parseCrossRefArg("@networking.aws_db_instance.primary.settings.enabled")
	if err != nil {
		t.Fatalf("parseCrossRefArg: %v", err)
	}
	if stackName != "networking" {
		t.Errorf("stackName = %q, want \"networking\"", stackName)
	}
	if to != "networking.aws_db_instance.primary.settings.enabled" {
		t.Errorf("to = %q, want \"networking.aws_db_instance.primary.settings.enabled\"", to)
	}
}

func TestParseCrossRefArg_MissingAtPrefix_Refused(t *testing.T) {
	_, _, err := parseCrossRefArg("networking.aws_vpc.main.id")
	if err == nil {
		t.Fatal("expected a refusal for a value missing the \"@\" prefix")
	}
	if !strings.Contains(err.Error(), `must start with "@"`) {
		t.Errorf("error = %v, want it to name the missing \"@\" prefix", err)
	}
}

func TestParseCrossRefArg_TooFewSegments_Refused(t *testing.T) {
	_, _, err := parseCrossRefArg("@networking.aws_vpc.main")
	if err == nil {
		t.Fatal("expected a refusal for a reference with no attribute path")
	}
	if !strings.Contains(err.Error(), "well-formed") {
		t.Errorf("error = %v, want it to name the malformed shape", err)
	}
}

func TestParseCrossRefArg_EmptySegment_Refused(t *testing.T) {
	_, _, err := parseCrossRefArg("@networking..main.id")
	if err == nil {
		t.Fatal("expected a refusal for a reference with an empty segment")
	}
}

func TestRenderArgLiteral_CrossRef_Go(t *testing.T) {
	p := Param{Name: "vpc_id", Type: ParamCrossRef, Required: true}
	got, err := renderArgLiteral("go", p, "@networking.aws_vpc.main.id")
	if err != nil {
		t.Fatalf("renderArgLiteral: %v", err)
	}
	want := `sdk.CrossStack("networking", "networking.aws_vpc.main.id")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderArgLiteral_CrossRef_TS(t *testing.T) {
	p := Param{Name: "vpc_id", Type: ParamCrossRef, Required: true}
	got, err := renderArgLiteral("ts", p, "@networking.aws_vpc.main.id")
	if err != nil {
		t.Fatalf("renderArgLiteral: %v", err)
	}
	want := `crossStack("networking", "networking.aws_vpc.main.id")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderArgLiteral_CrossRef_Python(t *testing.T) {
	p := Param{Name: "vpc_id", Type: ParamCrossRef, Required: true}
	got, err := renderArgLiteral("py", p, "@networking.aws_vpc.main.id")
	if err != nil {
		t.Fatalf("renderArgLiteral: %v", err)
	}
	want := `sdk.cross_stack("networking", "networking.aws_vpc.main.id")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderArgLiteral_CrossRef_MalformedRef_Refused(t *testing.T) {
	p := Param{Name: "vpc_id", Type: ParamCrossRef, Required: true}
	_, err := renderArgLiteral("go", p, "not-a-real-reference")
	if err == nil {
		t.Fatal("expected renderArgLiteral to refuse a malformed cross_ref argument")
	}
	if !strings.Contains(err.Error(), `param "vpc_id"`) {
		t.Errorf("error = %v, want it to name the param", err)
	}
}

func TestParseParamSpec_CrossRef_Required(t *testing.T) {
	p, err := parseParamSpec("vpc_id", "cross_ref, required")
	if err != nil {
		t.Fatalf("parseParamSpec: %v", err)
	}
	if p.Type != ParamCrossRef || !p.Required {
		t.Errorf("got %+v, want Type=ParamCrossRef, Required=true", p)
	}
}

func TestParseParamSpec_CrossRef_DefaultRefused(t *testing.T) {
	_, err := parseParamSpec("vpc_id", `cross_ref, default "x"`)
	if err == nil {
		t.Fatal("expected a refusal -- a cross_ref param has no sensible default")
	}
	if !strings.Contains(err.Error(), "no sensible default") {
		t.Errorf("error = %v, want it to explain why", err)
	}
}

func TestParamType_CrossRef_LanguageTypes(t *testing.T) {
	if got := ParamCrossRef.GoType(); got != "sdk.CrossMarker" {
		t.Errorf("GoType() = %q, want \"sdk.CrossMarker\"", got)
	}
	if got := ParamCrossRef.TSType(); got != "any" {
		t.Errorf("TSType() = %q, want \"any\"", got)
	}
	if got := ParamCrossRef.PyType(); got != "Any" {
		t.Errorf("PyType() = %q, want \"Any\"", got)
	}
}
