package dsfilter

import "testing"

func TestExcluded_RealFoundCases(t *testing.T) {
	cases := []struct {
		name   string
		c      Candidate
		want   Reason
		wantOK bool
	}{
		// Real, live-found cases from this session's own generated
		// corpus (STATE.md's own reconciliation entry has the full
		// counts) -- not hypotheticals.
		{"gcp operation node", Candidate{Noun: "operation", Path: "projects.locations.operations"}, ReasonOperationStatus, true},
		{"gcp location node", Candidate{Noun: "location", Path: "projects.locations"}, ReasonReferenceDuplication, true},
		{"azure operation status response", Candidate{Noun: "backup_job", ResponseTypeName: "OperationStatus"}, ReasonOperationStatus, true},
		{"kubernetes watch operation name", Candidate{Noun: "namespaced_pod", OperationName: "watchNamespacedPod"}, ReasonWatchPath, true},
		{"kubernetes watch path segment", Candidate{Noun: "pod", Path: "/api/v1/watch/namespaces/{namespace}/pods"}, ReasonWatchPath, true},

		// AWS's own real Smithy-sourced nouns are commonly still plural
		// at this point (Smithy has no singularization step of its own,
		// unlike discoverydoc/resourcemap) -- the trailing-"s" fallback
		// in hasToken must still catch these.
		{"aws plural operations noun", Candidate{Noun: "operations"}, ReasonOperationStatus, true},
		{"aws plural locations noun stays a genuine reference duplicate", Candidate{Noun: "locations"}, ReasonReferenceDuplication, true},
		{"aws plural buckets noun stays genuine", Candidate{Noun: "buckets"}, "", false},

		// Named, not yet live-counted, but exactly the categories UBI-181
		// itself names.
		{"execution record", Candidate{Noun: "pipeline_execution"}, ReasonExecutionOrEvent, true},
		{"event record", Candidate{Noun: "deployment_event"}, ReasonExecutionOrEvent, true},
		{"audit log", Candidate{Noun: "audit_log"}, ReasonExecutionOrEvent, true},
		{"computed availability check", Candidate{Noun: "name_availability"}, ReasonComputedValue, true},
		{"computed usage report", Candidate{Noun: "usage"}, ReasonComputedValue, true},
		{"computed cost estimate", Candidate{Noun: "cost_estimate"}, ReasonComputedValue, true},
		{"region reference", Candidate{Noun: "region"}, ReasonReferenceDuplication, true},
		{"availability zone reference", Candidate{Noun: "availability_zone"}, ReasonReferenceDuplication, true},

		// Real, genuine data sources that must NOT be excluded -- the
		// negative-path proof this filter isn't just broad enough to
		// accidentally eat everything.
		{"genuine instance lookup", Candidate{Noun: "instance"}, "", false},
		{"genuine target type lookup", Candidate{Noun: "target_type"}, "", false},
		{"genuine bucket lookup", Candidate{Noun: "bucket"}, "", false},
		{"genuine repository lookup", Candidate{Noun: "repository"}, "", false},
		{"genuine metric lookup, not a report", Candidate{Noun: "metric"}, "", false},
		// "zone" as a raw substring (not a "_"-delimited token) of an
		// unrelated noun must not spuriously match -- word-boundary
		// check, not bare substring.
		{"unrelated noun containing zone as a raw substring only", Candidate{Noun: "freezone"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Excluded(tc.c)
			if ok != tc.wantOK {
				t.Fatalf("Excluded(%+v) ok = %v, want %v (reason: %q)", tc.c, ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Fatalf("Excluded(%+v) reason = %q, want %q", tc.c, got, tc.want)
			}
		})
	}
}

func TestHasToken_WordBoundary(t *testing.T) {
	if hasToken("zonefile", []string{"zone"}) {
		t.Fatal("hasToken should not match \"zone\" inside \"zonefile\" -- not a word boundary")
	}
	if !hasToken("availability_zone", []string{"availability_zone"}) {
		t.Fatal("hasToken should match an exact phrase")
	}
	if !hasToken("chaos_availability_zone_target", []string{"availability_zone"}) {
		t.Fatal("hasToken should match a multi-word phrase as an internal component")
	}
	if !hasToken("region", []string{"region"}) {
		t.Fatal("hasToken should match a bare, single-token noun")
	}
}

func TestHasPathSegment(t *testing.T) {
	if !hasPathSegment("/api/v1/watch/namespaces/{namespace}/pods", "watch") {
		t.Fatal("expected a slash-separated watch segment to match")
	}
	if !hasPathSegment("projects.locations.operations", "operations") {
		t.Fatal("expected a dot-separated operations segment to match")
	}
	if hasPathSegment("/api/v1/pods", "watch") {
		t.Fatal("expected no false match when watch is not a real segment")
	}
	if hasPathSegment("", "watch") {
		t.Fatal("expected an empty path to never match")
	}
}
