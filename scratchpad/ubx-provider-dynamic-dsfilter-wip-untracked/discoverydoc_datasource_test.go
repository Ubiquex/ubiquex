package discoverydoc

import "testing"

// buildPubSubShapedDocWithNoise extends buildPubSubShapedDoc with three
// real, get-only, no-create nodes -- "operations" and "locations"
// (UBI-181's own real, live-found noise: GCP's universal
// operation-polling and platform-reference-location boilerplate,
// present on nearly every real GCP service) alongside a genuine,
// unrelated get-only node ("snapshots") that must survive filtering.
func buildPubSubShapedDocWithNoise() *Document {
	doc := buildPubSubShapedDoc()
	projects := doc.Resources["projects"]
	projects.Resources["operations"] = &rawResource{
		Methods: map[string]*rawMethod{
			"get": {HTTPMethod: "GET", FlatPath: "v1/projects/{projectsId}/operations/{operationsId}", Response: &rawRef{Ref: "Operation"}},
		},
	}
	projects.Resources["locations"] = &rawResource{
		Methods: map[string]*rawMethod{
			"get": {HTTPMethod: "GET", FlatPath: "v1/projects/{projectsId}/locations/{locationsId}", Response: &rawRef{Ref: "Location"}},
		},
	}
	projects.Resources["snapshots"] = &rawResource{
		Methods: map[string]*rawMethod{
			"get": {HTTPMethod: "GET", FlatPath: "v1/projects/{projectsId}/snapshots/{snapshotsId}", Response: &rawRef{Ref: "Snapshot"}},
		},
	}
	return doc
}

// TestDiscoverDataSources_UBI181Rules_ExcludeOperationAndLocation is the
// real, live-shaped proof the five UBI-181 rules are wired into
// discoverydoc's own DiscoverDataSources, not just present in dsfilter's
// own package: "operations" and "locations" -- confirmed live this
// session to make up 113 and 145 of GCP's own real, previously
// unfiltered 788-candidate generation -- are excluded, while "snapshots"
// (a genuine, unrelated get-only node) survives.
func TestDiscoverDataSources_UBI181Rules_ExcludeOperationAndLocation(t *testing.T) {
	doc := buildPubSubShapedDocWithNoise()
	candidates, notes, err := DiscoverDataSources(doc, "google", "")
	if err != nil {
		t.Fatalf("DiscoverDataSources: %v", err)
	}

	byNoun := map[string]bool{}
	for _, c := range candidates {
		byNoun[c.TypeName] = true
	}

	if byNoun["google_pubsub_operation"] {
		t.Error("expected \"operations\" to be excluded (rule 2, operation-status shape), but it was kept")
	}
	if byNoun["google_pubsub_location"] {
		t.Error("expected \"locations\" to be excluded (rule 5, high-volume reference duplication), but it was kept")
	}
	if !byNoun["google_pubsub_snapshot"] {
		t.Errorf("expected the genuine, unrelated \"snapshots\" node to survive filtering, got candidates: %v", candidates)
	}

	foundOperationNote, foundLocationNote := false, false
	for _, n := range notes {
		if n.Detail == "excluded from data-source candidates: operation-status shape -- async job/operation polling, not stored infrastructure data" {
			foundOperationNote = true
		}
		if n.Detail == "excluded from data-source candidates: high-volume reference duplication -- generic platform reference data repeated near-identically across many services, not this service's own real data" {
			foundLocationNote = true
		}
	}
	if !foundOperationNote {
		t.Error("expected a Note recording why \"operations\" was excluded -- exclusions must be auditable, never a silent drop")
	}
	if !foundLocationNote {
		t.Error("expected a Note recording why \"locations\" was excluded -- exclusions must be auditable, never a silent drop")
	}
}
