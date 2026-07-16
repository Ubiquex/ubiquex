package conformance

import "testing"

var validCategories = map[string]bool{
	"compute": true, "network": true, "iam": true,
	"storage": true, "db": true, "dns": true, "messaging": true,
}

// TestRegistry_NoDuplicateTypes checks (Source, Type) uniqueness, not bare
// Type (UBI-21): two different providers legitimately never collide on
// type name today, but the registry's own KEY is the pair, and a
// duplicate-Type-only check would pass right over an actual duplicate
// (Source, Type) entry hiding behind a difference in field order.
func TestRegistry_NoDuplicateTypes(t *testing.T) {
	seen := map[[2]string]bool{}
	for _, s := range Registry {
		key := [2]string{s.Source, s.Type}
		if seen[key] {
			t.Errorf("duplicate registry entry for %s/%s", s.Source, s.Type)
		}
		seen[key] = true
	}
}

// TestRegistry_EverySourceIsAWSOrGCP guards against a typo'd Source
// silently creating a third, untracked provider bucket.
func TestRegistry_EverySourceIsAWSOrGCP(t *testing.T) {
	for _, s := range Registry {
		if s.Source != "hashicorp/aws" && s.Source != "hashicorp/google" {
			t.Errorf("%s: unrecognized Source %q", s.Type, s.Source)
		}
	}
}

func TestRegistry_ValidCategories(t *testing.T) {
	for _, s := range Registry {
		if !validCategories[s.Category] {
			t.Errorf("%s: unrecognized category %q", s.Type, s.Category)
		}
	}
}

func TestRegistry_ImplementedEntriesAreDocumented(t *testing.T) {
	for _, s := range Registry {
		if !s.Implemented {
			continue
		}
		if s.Notes == "" {
			t.Errorf("%s: marked Implemented but has no Notes — quirks must be documented, not silently known", s.Type)
		}
		if len(s.IdentityFields) == 0 {
			t.Errorf("%s: marked Implemented but has no IdentityFields", s.Type)
		}
	}
}

func TestRegistry_ByType(t *testing.T) {
	if ByType("hashicorp/aws", "aws_s3_bucket") == nil {
		t.Fatal("expected aws_s3_bucket to be in the registry")
	}
	if ByType("hashicorp/aws", "aws_totally_made_up") != nil {
		t.Fatal("expected a made-up type to be absent from the registry")
	}
	if ByType("hashicorp/google", "google_storage_bucket") == nil {
		t.Fatal("expected google_storage_bucket to be in the registry")
	}
	// Keyed by (source, type), not type alone: the same bare type name
	// under the wrong source must not resolve (UBI-21).
	if ByType("hashicorp/google", "aws_s3_bucket") != nil {
		t.Fatal("expected aws_s3_bucket to be absent under the wrong Source")
	}
}

// TestRegistry_NoThirdState enforces UBI-9's own completion criterion: every
// type is either Implemented (VERIFIED — real or fake fixture) or PARKED
// (Implemented: false with a documented Notes reason, the aws_iam_group
// precedent). An entry with neither — Implemented: false and no Notes at
// all — means a type nobody has actually gotten to yet, which UBI-9 isn't
// done until zero of those remain.
func TestRegistry_NoThirdState(t *testing.T) {
	for _, s := range Registry {
		if !s.Implemented && s.Notes == "" {
			t.Errorf("%s: neither Implemented nor documented as parked — third state not allowed", s.Type)
		}
	}
}

// TestRegistry_ApproximatelyFifty_AWS locks in the "~50" scope from
// docs/plan.md §M1-2 — not an exact count (the list may grow/shrink a
// little as batches proceed), just a sanity bound against the registry
// silently ballooning or shrinking far from what was actually scoped.
// Scoped to hashicorp/aws specifically (UBI-21 split this bound by
// source; see the GCP counterpart below) since a second provider's own
// entry count is a completely independent scope decision.
func TestRegistry_ApproximatelyFifty_AWS(t *testing.T) {
	n := countBySource(t, "hashicorp/aws")
	if n < 40 || n > 60 {
		t.Fatalf("hashicorp/aws entries = %d, want roughly 50 (40-60)", n)
	}
}

// TestRegistry_ApproximatelyForty_GCP is the GCP counterpart, locking in
// docs/plan.md §M1-2 GCP resource type list's own ~40-type scope (UBI-21
// Stage 1).
func TestRegistry_ApproximatelyForty_GCP(t *testing.T) {
	n := countBySource(t, "hashicorp/google")
	if n < 30 || n > 50 {
		t.Fatalf("hashicorp/google entries = %d, want roughly 40 (30-50)", n)
	}
}

func countBySource(t *testing.T, source string) int {
	t.Helper()
	n := 0
	for _, s := range Registry {
		if s.Source == source {
			n++
		}
	}
	return n
}
