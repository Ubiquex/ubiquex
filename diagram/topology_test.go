package diagram

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestTopology_ExcludesSummarySourcesAmbiguity(t *testing.T) {
	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_vpc", Name: "main-vpc", Op: resolver.OpCreate},
		},
	}
	intent.Intent.Summary = "should never appear in the topology view"
	intent.Intent.Sources = []core.IntentSource{{Kind: "document", Ref: "payments.d2"}}

	got, err := Topology(intent)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if strings.Contains(string(got), "should never appear") {
		t.Fatalf("topology leaked intent.summary: %s", got)
	}
	if strings.Contains(string(got), `"summary"`) || strings.Contains(string(got), `"sources"`) {
		t.Fatalf("topology leaked a summary/sources key: %s", got)
	}
}

func TestTopology_SortedByTypeThenName_IndependentOfInputOrder(t *testing.T) {
	base := &resolver.IntentFile{Stack: "payments", Resources: []resolver.ResourceIntent{
		{Type: "aws_vpc", Name: "z-vpc", Op: resolver.OpCreate},
		{Type: "aws_db_instance", Name: "a-db", Op: resolver.OpCreate},
		{Type: "aws_db_instance", Name: "b-db", Op: resolver.OpCreate},
	}}
	reversed := &resolver.IntentFile{Stack: "payments", Resources: []resolver.ResourceIntent{
		{Type: "aws_db_instance", Name: "b-db", Op: resolver.OpCreate},
		{Type: "aws_db_instance", Name: "a-db", Op: resolver.OpCreate},
		{Type: "aws_vpc", Name: "z-vpc", Op: resolver.OpCreate},
	}}

	got, err := Topology(base)
	if err != nil {
		t.Fatalf("Topology(base): %v", err)
	}
	got2, err := Topology(reversed)
	if err != nil {
		t.Fatalf("Topology(reversed): %v", err)
	}
	if string(got) != string(got2) {
		t.Fatalf("Topology is sensitive to input order:\nbase:     %s\nreversed: %s", got, got2)
	}

	dbIdx := strings.Index(string(got), `"aws_db_instance"`)
	vpcIdx := strings.Index(string(got), `"aws_vpc"`)
	if dbIdx == -1 || vpcIdx == -1 || dbIdx > vpcIdx {
		t.Fatalf("expected aws_db_instance before aws_vpc (sorted by type), got: %s", got)
	}
}

func TestTopology_IncludesDependsOn(t *testing.T) {
	intent := &resolver.IntentFile{Stack: "payments", Resources: []resolver.ResourceIntent{
		{Type: "aws_db_instance", Name: "db", Op: resolver.OpCreate, DependsOn: []string{"payments.aws_vpc.main-vpc"}},
	}}
	got, err := Topology(intent)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if !strings.Contains(string(got), `"payments.aws_vpc.main-vpc"`) {
		t.Fatalf("expected depends_on to be included, got: %s", got)
	}
}

func TestTopology_ExcludesConfig(t *testing.T) {
	intent := &resolver.IntentFile{Stack: "payments", Resources: []resolver.ResourceIntent{
		{Type: "aws_vpc", Name: "main-vpc", Op: resolver.OpCreate, Config: []byte(`{"cidr_block":"10.0.0.0/16"}`)},
	}}
	got, err := Topology(intent)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if strings.Contains(string(got), "cidr_block") {
		t.Fatalf("topology leaked config content, want it excluded: %s", got)
	}
}

func TestTopology_DeterministicAcrossRepeatedCalls(t *testing.T) {
	intent := &resolver.IntentFile{Stack: "payments", Resources: []resolver.ResourceIntent{
		{Type: "aws_db_instance", Name: "db", Op: resolver.OpCreate, DependsOn: []string{"payments.aws_vpc.main-vpc"}},
		{Type: "aws_vpc", Name: "main-vpc", Op: resolver.OpCreate},
	}}
	first, err := Topology(intent)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	for i := 0; i < 4; i++ {
		got, err := Topology(intent)
		if err != nil {
			t.Fatalf("Topology (rerun): %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("Topology changed across repeated calls with identical input")
		}
	}
}
