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

	merged, _, _, _, err := mergeDynamicProviderGroupMembers("google", members)
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

	_, _, _, _, err := mergeDynamicProviderGroupMembers("google", members)
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

// TestMergeDynamicProviderGroupMembers_MultipleCollisions_ReportsSameOneEveryRun
// is UBI-190's own real regression: with two or more genuinely
// unresolved collisions present in the same merge (the real, live
// Datadog v1/v2 shape, confirmed against the actual live spec before
// this fix -- application_key_response and user_response both
// colliding at once), the merge must fail loud on the SAME
// alphabetically-first colliding type name every time, not whichever
// one Go's own randomized map iteration order happens to hit first.
// Before the fix, this exact scenario (live-verified, not simulated)
// reported "application_key_response" on some runs and "user_response"
// on others against byte-identical input -- ordering-dependence
// CLAUDE.md's own determinism rule forbids and UBI-182's snapshot
// hashing depends on not happening. Runs the merge many times over the
// same map-shaped input specifically because a single run cannot
// distinguish "always deterministic" from "got lucky once" -- Go's
// randomized map order needs several draws to surface if sorting
// didn't actually fix it.
func TestMergeDynamicProviderGroupMembers_MultipleCollisions_ReportsSameOneEveryRun(t *testing.T) {
	var firstErr string
	for i := 0; i < 50; i++ {
		members := []dynamicProviderGroupMember{
			{name: "datadog", schemas: fakeGroupSchemas("datadog_application_key_response", "datadog_user_response", "datadog_monitor")},
			{name: "datadog_v2", schemas: fakeGroupSchemas("datadog_application_key_response", "datadog_user_response", "datadog_incident")},
		}
		_, _, _, _, err := mergeDynamicProviderGroupMembers("datadog_all", members)
		if err == nil {
			t.Fatal("expected an error on real, unresolved collisions, got nil")
		}
		if i == 0 {
			firstErr = err.Error()
			if !strings.Contains(firstErr, "datadog_application_key_response") {
				t.Errorf("expected the alphabetically-first colliding type to be reported, got: %v", firstErr)
			}
			continue
		}
		if err.Error() != firstErr {
			t.Fatalf("collision report varied across runs of identical input (run %d):\nfirst: %s\nnow:   %s", i, firstErr, err.Error())
		}
	}
}

func TestMergeDynamicProviderGroupMembers_ExcludedCollision_DropsExcludedSideNotBothOrError(t *testing.T) {
	// UBI-175 Datadog v1/v2: a declared exclude on the losing member
	// resolves the collision by dropping that member's own entry --
	// the other member's entry wins under its own plain, unprefixed
	// wire name. Distinct from the no-exclude case above, which must
	// still fail loud.
	members := []dynamicProviderGroupMember{
		{name: "datadog", schemas: fakeGroupSchemas("event_response", "monitor")},
		{name: "datadog_v2", schemas: fakeGroupSchemas("event_response", "incident"), exclude: map[string]bool{"event_response": true}},
	}

	merged, _, _, _, err := mergeDynamicProviderGroupMembers("datadog_all", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if len(merged.Resources) != 3 {
		t.Fatalf("expected 3 merged resource types (event_response once, plus monitor and incident), got %d: %v", len(merged.Resources), merged.Resources)
	}
	for _, want := range []string{"event_response", "monitor", "incident"} {
		if _, ok := merged.Resources[want]; !ok {
			t.Errorf("expected merged Resources to contain %q", want)
		}
	}
}

func TestGroupExcludeFromParams_ParsesNestedMemberTable(t *testing.T) {
	params := map[string]any{
		"exclude": map[string]any{
			"datadog_v2": []any{"event_response", "application_key_response", "user_response"},
		},
	}
	exclude, err := groupExcludeFromParams(params)
	if err != nil {
		t.Fatalf("groupExcludeFromParams: %v", err)
	}
	member := exclude["datadog_v2"]
	if len(member) != 3 || !member["event_response"] || !member["application_key_response"] || !member["user_response"] {
		t.Errorf("expected 3 excluded names for datadog_v2, got: %v", member)
	}
}

func TestGroupExcludeFromParams_Absent_ReturnsNilNotError(t *testing.T) {
	exclude, err := groupExcludeFromParams(map[string]any{"members": []any{"a"}})
	if err != nil {
		t.Fatalf("groupExcludeFromParams: %v", err)
	}
	if exclude != nil {
		t.Errorf("expected nil exclude when no exclude table declared, got: %v", exclude)
	}
}

func TestMergeDynamicProviderGroupMembers_CollidingDataSource_Errors(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{name: "a", schemas: &provider.Schemas{DataSources: map[string]*provider.Schema{"shared_ds": {}}}},
		{name: "b", schemas: &provider.Schemas{DataSources: map[string]*provider.Schema{"shared_ds": {}}}},
	}

	_, _, _, _, err := mergeDynamicProviderGroupMembers("group", members)
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

	_, signals, _, exclude, err := mergeDynamicProviderGroupMembers("group", members)
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

// TestMergeDynamicProviderGroupMembers_MergesNamespaces is UBI-98's own
// real coverage: namespacesByType unions across members exactly like
// signalsByType does, since a real dynamic_provider_group (e.g.
// azure_all/google_all) is the same real code path AWS's own future
// group-based data-source generation will need too.
func TestMergeDynamicProviderGroupMembers_MergesNamespaces(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{
			name:             "aws",
			schemas:          fakeGroupSchemas("aws_instance"),
			namespacesByType: map[string]string{"aws_instance": "ec2"},
		},
		{
			name:             "aws_route53",
			schemas:          fakeGroupSchemas("aws_route53_record"),
			namespacesByType: map[string]string{"aws_route53_record": "route53"},
		},
	}

	_, _, namespaces, _, err := mergeDynamicProviderGroupMembers("group", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if len(namespaces) != 2 || namespaces["aws_instance"] != "ec2" || namespaces["aws_route53_record"] != "route53" {
		t.Errorf("expected namespaces for both aws_instance and aws_route53_record, got: %v", namespaces)
	}
}

func TestMergeDynamicProviderGroupMembers_NoSignalsOrExcludes_ReturnsNilNotEmptyMap(t *testing.T) {
	members := []dynamicProviderGroupMember{
		{name: "a", schemas: fakeGroupSchemas("a_thing")},
	}

	_, signals, namespaces, exclude, err := mergeDynamicProviderGroupMembers("group", members)
	if err != nil {
		t.Fatalf("mergeDynamicProviderGroupMembers: %v", err)
	}
	if signals != nil {
		t.Errorf("expected nil signals when no member has any, got: %v", signals)
	}
	if namespaces != nil {
		t.Errorf("expected nil namespaces when no member has any, got: %v", namespaces)
	}
	if exclude != nil {
		t.Errorf("expected nil describeExclude when no member has any, got: %v", exclude)
	}
}

func TestGenerateDynamicProviderGroup_NoMembers_Errors(t *testing.T) {
	_, _, _, err := generateDynamicProviderGroup(t.Context(), 0, "empty-group", "empty-group", nil, nil, nil, "", "", "", nil, "", "", "")
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
