package cli

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/provider"
)

// fakeGroupSchemas builds a minimal *provider.Schemas with real resource
// types named typeNames -- content of each Schema is irrelevant to
// mergeDynamicProviderGroupMembers, which only ever inspects map keys.
func fakeGroupSchemas(typeNames ...string) *provider.Schemas {
	s := &provider.Schemas{Resources: map[string]*provider.Schema{}}
	for _, n := range typeNames {
		s.Resources[n] = &provider.Schema{}
	}
	return s
}

func TestMergeDynamicProviderGroupMembers_UnionsDistinctMembers(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{name: "google_dataflow", schemas: fakeGroupSchemas("google_dataflow_job")},
		{name: "google_bigquery", schemas: fakeGroupSchemas("google_bigquery_dataset", "google_bigquery_table")},
	}

	merged, _, _, err := mergeDynamicProviderGroupMembers("google", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if len(merged.Resources) != 3 {
		t.Fatalf("expected 3 merged resource types, got %d: %v", len(merged.Resources), merged.Resources)
	}
	for _, want := range []string{"google_dataflow_job", "google_bigquery_dataset", "google_bigquery_table"} {
		if _, ok := merged.Resources[want]; !ok {
			t.Errorf("expected merged Resources to contain %q", want)
		}
	}
}

func TestMergeDynamicProviderGroupMembers_CollidingResourceType_ErrorsNotSilentOverwrite(t *testing.T) {
	// A real collision: two distinct members both declaring the exact
	// same resource type name. Must fail loud, never silently let the
	// second member's entry win.
	members := []dynamicProviderGroupMember{
		{name: "google_dataflow", schemas: fakeGroupSchemas("google_shared_thing")},
		{name: "google_bigquery", schemas: fakeGroupSchemas("google_shared_thing")},
	}

	_, _, _, err := mergeDynamicProviderGroupMembers("google", members)
	if err == nil {
		t.Fatal("expected an error on a real resource-type collision across members, got nil")
	}
	if !strings.Contains(err.Error(), "google_shared_thing") {
		t.Errorf("expected error to name the colliding type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "google_bigquery") {
		t.Errorf("expected error to name the offending member, got: %v", err)
	}
}

func TestMergeDynamicProviderGroupMembers_CollidingDataSource_Errors(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{name: "a", schemas: &provider.Schemas{DataSources: map[string]*provider.Schema{"shared_ds": {}}}},
		{name: "b", schemas: &provider.Schemas{DataSources: map[string]*provider.Schema{"shared_ds": {}}}},
	}

	_, _, _, err := mergeDynamicProviderGroupMembers("group", members)
	if err == nil {
		t.Fatal("expected an error on a real data-source collision across members, got nil")
	}
	if !strings.Contains(err.Error(), "shared_ds") {
		t.Errorf("expected error to name the colliding data source, got: %v", err)
	}
}

func TestMergeDynamicProviderGroupMembers_MergesSignalsAndDescribeExclude(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{
			name:            "a",
			schemas:         fakeGroupSchemas("a_thing"),
			signalsByType:   map[string]map[string]*fieldSignal{"a_thing": {"status": {Enum: []string{"UP", "DOWN"}}}},
			describeExclude: map[string]bool{"a_excluded": true},
		},
		{
			name:            "b",
			schemas:         fakeGroupSchemas("b_thing"),
			signalsByType:   map[string]map[string]*fieldSignal{"b_thing": {"kind": {Enum: []string{"X"}}}},
			describeExclude: map[string]bool{"b_excluded": true},
		},
	}

	_, signals, exclude, err := mergeDynamicProviderGroupMembers("group", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if len(signals) != 2 || signals["a_thing"] == nil || signals["b_thing"] == nil {
		t.Errorf("expected signals for both a_thing and b_thing, got: %v", signals)
	}
	if !exclude["a_excluded"] || !exclude["b_excluded"] {
		t.Errorf("expected describeExclude to union both members' entries, got: %v", exclude)
	}
}

func TestMergeDynamicProviderGroupMembers_NoSignalsOrExcludes_ReturnsNilNotEmptyMap(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{name: "a", schemas: fakeGroupSchemas("a_thing")},
	}

	_, signals, exclude, err := mergeDynamicProviderGroupMembers("group", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if signals != nil {
		t.Errorf("expected nil signals when no member has any, got: %v", signals)
	}
	if exclude != nil {
		t.Errorf("expected nil describeExclude when no member has any, got: %v", exclude)
	}
}

func TestGenerateDynamicProviderGroup_NoMembers_Errors(t *testing.T) {
	_, _, _, err := generateDynamicProviderGroup(t.Context(), 0, "empty-group", "empty-group", nil, nil, "", "", "", nil, "", "", "")
	if err == nil {
		t.Fatal("expected an error for a group with zero declared members, got nil")
	}
	if !strings.Contains(err.Error(), "empty-group") || !strings.Contains(err.Error(), "no members") {
		t.Errorf("expected error to name the group and say no members, got: %v", err)
	}
}

func TestGroupMembersFromParams_ValidList(t *testing.T) {
	params := map[string]any{"members": []any{"a", "b", "c"}}
	members, err := groupMembersFromParams(params)
	if err != nil {
		t.Fatalf("groupMembersFromParams: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(members) != len(want) {
		t.Fatalf("expected %v, got %v", want, members)
	}
	for i, m := range want {
		if members[i] != m {
			t.Errorf("expected members[%d]=%q, got %q", i, m, members[i])
		}
	}
}

func TestGroupMembersFromParams_MissingKey_Errors(t *testing.T) {
	if _, err := groupMembersFromParams(map[string]any{}); err == nil {
		t.Fatal("expected an error for a group with no members key, got nil")
	}
}

func TestGroupMembersFromParams_EmptyList_Errors(t *testing.T) {
	if _, err := groupMembersFromParams(map[string]any{"members": []any{}}); err == nil {
		t.Fatal("expected an error for a group with an empty members list, got nil")
	}
}

func TestGroupMembersFromParams_NonStringEntry_Errors(t *testing.T) {
	if _, err := groupMembersFromParams(map[string]any{"members": []any{"a", 42}}); err == nil {
		t.Fatal("expected an error for a non-string members entry, got nil")
	}
}

func TestRepoNameFromGroupParams_NoOverride_DefaultsToGroupName(t *testing.T) {
	got := repoNameFromGroupParams(map[string]any{}, "google_all")
	if got != "google_all" {
		t.Errorf("expected default to groupName %q, got %q", "google_all", got)
	}
}

func TestRepoNameFromGroupParams_Override_UsedInstead(t *testing.T) {
	// The real bug this override exists to fix: a group named "google_all"
	// (distinct from its own "google" member, avoiding the --only
	// ambiguity) must still be able to claim the real repo identity
	// "google" that member already uses.
	got := repoNameFromGroupParams(map[string]any{"repo_name": "google"}, "google_all")
	if got != "google" {
		t.Errorf("expected repo_name override %q, got %q", "google", got)
	}
}

func TestRepoNameFromGroupParams_WrongType_FallsBackToGroupName(t *testing.T) {
	got := repoNameFromGroupParams(map[string]any{"repo_name": 42}, "google_all")
	if got != "google_all" {
		t.Errorf("expected fallback to groupName on wrong-typed repo_name, got %q", got)
	}
}
