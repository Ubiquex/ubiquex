package discovery

import "testing"

func TestSuggestStacks_ByGroupTag(t *testing.T) {
	resources := []DiscoveredResource{
		{ARN: "arn:aws:sqs:us-east-1:1:q1", Tags: map[string]string{"Project": "payments"}},
		{ARN: "arn:aws:sqs:us-east-1:1:q2", Tags: map[string]string{"Project": "payments"}},
		{ARN: "arn:aws:sqs:us-east-1:1:q3", Tags: map[string]string{"Project": "networking"}},
		{ARN: "arn:aws:sqs:us-east-1:1:q4", Tags: map[string]string{}}, // no Project tag at all
	}
	groups := SuggestStacks(resources, "Project")
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3 (payments, networking, ungrouped), got %+v", len(groups), groups)
	}
	if groups[0].Suggestion != "networking" || len(groups[0].Resources) != 1 {
		t.Fatalf("groups[0] = %+v, want networking with 1 resource (alphabetical order)", groups[0])
	}
	if groups[1].Suggestion != "payments" || len(groups[1].Resources) != 2 {
		t.Fatalf("groups[1] = %+v, want payments with 2 resources", groups[1])
	}
	if groups[2].Suggestion != "" || len(groups[2].Resources) != 1 {
		t.Fatalf("groups[2] = %+v, want the ungrouped bucket last, with 1 resource", groups[2])
	}
}

func TestSuggestStacks_NamingPrefixHeuristic_NoGroupTagGiven(t *testing.T) {
	resources := []DiscoveredResource{
		{ARN: "arn:aws:sqs:us-east-1:1:payments-queue-1"},
		{ARN: "arn:aws:sqs:us-east-1:1:payments-queue-2"},
		{ARN: "arn:aws:sqs:us-east-1:1:networking_vpc"},
		{ARN: "arn:aws:sqs:us-east-1:1:nosepararator"},
	}
	groups := SuggestStacks(resources, "")
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3 (payments, networking, ungrouped), got %+v", len(groups), groups)
	}
	if groups[0].Suggestion != "networking" {
		t.Fatalf("groups[0].Suggestion = %q, want networking", groups[0].Suggestion)
	}
	if groups[1].Suggestion != "payments" || len(groups[1].Resources) != 2 {
		t.Fatalf("groups[1] = %+v, want payments with 2 resources", groups[1])
	}
	if groups[2].Suggestion != "" || len(groups[2].Resources) != 1 {
		t.Fatalf("groups[2] = %+v, want the ungrouped bucket with the no-separator resource", groups[2])
	}
}

// TestSuggestStacks_NeverAssignsOnlySuggests is a structural guard,
// matching docs/discovery.md's own explicit requirement: SuggestStacks
// returns data only, no side effect anywhere -- there is no Stack field
// on DiscoveredResource for it to have mutated in the first place.
func TestSuggestStacks_NeverAssignsOnlySuggests(t *testing.T) {
	resources := []DiscoveredResource{{ARN: "arn:aws:sqs:us-east-1:1:q1", TerraformType: "aws_sqs_queue"}}
	_ = SuggestStacks(resources, "Project")
	if resources[0].ARN != "arn:aws:sqs:us-east-1:1:q1" || resources[0].TerraformType != "aws_sqs_queue" {
		t.Fatal("SuggestStacks must never mutate its own input resources")
	}
}
