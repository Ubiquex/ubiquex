package conformance

import "testing"

var validCategories = map[string]bool{
	"compute": true, "network": true, "iam": true,
	"storage": true, "db": true, "dns": true, "messaging": true,
}

func TestRegistry_NoDuplicateTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Registry {
		if seen[s.Type] {
			t.Errorf("duplicate registry entry for %s", s.Type)
		}
		seen[s.Type] = true
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
	if ByType("aws_s3_bucket") == nil {
		t.Fatal("expected aws_s3_bucket to be in the registry")
	}
	if ByType("aws_totally_made_up") != nil {
		t.Fatal("expected a made-up type to be absent from the registry")
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

// TestRegistry_ApproximatelyFifty locks in the "~50" scope from
// docs/plan.md §M1-2 — not an exact count (the list may grow/shrink a
// little as batches proceed), just a sanity bound against the registry
// silently ballooning or shrinking far from what was actually scoped.
func TestRegistry_ApproximatelyFifty(t *testing.T) {
	n := len(Registry)
	if n < 40 || n > 60 {
		t.Fatalf("len(Registry) = %d, want roughly 50 (40-60)", n)
	}
}
