package resourcemap

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// testOp and newTestDoc are this file's own small, local DSL for
// building a minimal *openapi3.T with a handful of GET-only paths --
// resourcemap_test.go's own real openapi3.T/openapi3.NewPaths
// construction, generalized to N paths instead of each test hand-rolling
// its own, since every test in this file needs the identical
// "GET-only path -> a JSON response schema" shape.
type testOp struct {
	opID string
	resp *openapi3.SchemaRef
}

func newTestDoc(paths map[string]*testOp) *openapi3.T {
	doc := &openapi3.T{OpenAPI: "3.0.3", Info: &openapi3.Info{Title: "t", Version: "1"}}
	var opts []openapi3.NewPathsOption
	for path, o := range paths {
		opts = append(opts, openapi3.WithPath(path, &openapi3.PathItem{
			Get: &openapi3.Operation{OperationID: o.opID, Responses: responses200(o.resp)},
		}))
	}
	doc.Paths = openapi3.NewPaths(opts...)
	return doc
}

// TestDiscoverDataSources_UBI181Rules_ExcludesOperationAndWatch is the
// real, live-shaped proof the five UBI-181 rules are wired into
// resourcemap's own DiscoverDataSources: Azure's own real
// "OperationStatus"-response operations-polling GET, and Kubernetes' own
// real Watch-prefixed operationId, are both excluded, while a genuine,
// unrelated GET survives.
func TestDiscoverDataSources_UBI181Rules_ExcludesOperationAndWatch(t *testing.T) {
	targetTypeRef := openapi3.NewSchemaRef("#/components/schemas/TargetType",
		openapi3.NewObjectSchema().WithProperty("id", openapi3.NewStringSchema()))
	opStatusRef := openapi3.NewSchemaRef("#/components/schemas/OperationStatus",
		openapi3.NewObjectSchema().WithProperty("status", openapi3.NewStringSchema()))

	doc := newTestDoc(map[string]*testOp{
		"/targetTypes/{id}":         {opID: "targetTypes_get", resp: targetTypeRef},
		"/operations/{operationId}": {opID: "operations_get", resp: opStatusRef},
		"/pods/watch/{namespace}":   {opID: "watchNamespacedPod", resp: targetTypeRef},
	})

	candidates, notes, err := DiscoverDataSources(doc, "azure")
	if err != nil {
		t.Fatalf("DiscoverDataSources: %v", err)
	}

	byTypeName := map[string]bool{}
	for _, c := range candidates {
		byTypeName[c.TypeName] = true
	}

	if !byTypeName["azure_target_type"] {
		t.Errorf("expected the genuine target-type lookup to survive, got candidates: %v", candidates)
	}
	if byTypeName["azure_operation"] || byTypeName["azure_operation_status"] {
		t.Error("expected the operations-status polling GET to be excluded (rule 2), but it was kept")
	}
	for _, c := range candidates {
		if c.Operation != nil && c.Operation.OperationID == "watchNamespacedPod" {
			t.Error("expected the Watch-prefixed operation to be excluded (rule 1, watch path), but it was kept")
		}
	}

	foundNote := false
	for _, n := range notes {
		if n.Detail == "excluded from data-source candidates: operation-status shape -- async job/operation polling, not stored infrastructure data" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Error("expected a Note recording the operation-status exclusion -- must be auditable, never a silent drop")
	}
}

// TestDiscoverDataSources_CollectionEnvelope_UnwrapsToRealItemNoun is the
// real, live-found deriveNoun fix's own proof: a collection-listing GET
// whose response schema is a real envelope wrapper (Azure's own
// "TargetTypeListResult" convention -- a "value" array of $ref'd items
// plus a "nextLink" pagination field, confirmed live against the real
// Azure chaos studio spec) must yield the real item noun
// ("azure_target_type"), never the wrapper's own literal name
// ("azure_target_type_list_result").
func TestDiscoverDataSources_CollectionEnvelope_UnwrapsToRealItemNoun(t *testing.T) {
	itemRef := openapi3.NewSchemaRef("#/components/schemas/TargetType",
		openapi3.NewObjectSchema().WithProperty("id", openapi3.NewStringSchema()))

	envelope := openapi3.NewObjectSchema().
		WithProperty("nextLink", openapi3.NewStringSchema())
	arraySchema := openapi3.NewArraySchema()
	arraySchema.Items = itemRef
	envelope.WithPropertyRef("value", openapi3.NewSchemaRef("", arraySchema))
	envelopeRef := openapi3.NewSchemaRef("#/components/schemas/TargetTypeListResult", envelope)

	doc := newTestDoc(map[string]*testOp{
		"/targetTypes": {opID: "targetTypes_list", resp: envelopeRef},
	})

	candidates, _, err := DiscoverDataSources(doc, "azure")
	if err != nil {
		t.Fatalf("DiscoverDataSources: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate, got %d: %v", len(candidates), candidates)
	}
	if got := candidates[0].TypeName; got != "azure_target_type" {
		t.Errorf("expected the collection envelope to unwrap to the real item noun \"azure_target_type\", got %q", got)
	}
}

// TestDiscoverDataSources_FlatArrayResponse_NotMistakenForEnvelope is the
// negative-path proof collectionItemRefName's own shape test isn't just
// broad enough to strip any name ending in a list-shaped suffix: a real,
// unrelated domain type that merely happens to end in "List" (a flat
// array of strings, no $ref to another named schema -- a genuine
// allow-list, not a wrapper) must NOT be unwrapped.
func TestDiscoverDataSources_FlatArrayResponse_NotMistakenForEnvelope(t *testing.T) {
	flatArraySchema := openapi3.NewArraySchema()
	flatArraySchema.Items = openapi3.NewSchemaRef("", openapi3.NewStringSchema())
	flatArray := openapi3.NewObjectSchema().
		WithPropertyRef("entries", openapi3.NewSchemaRef("", flatArraySchema))
	flatArrayRef := openapi3.NewSchemaRef("#/components/schemas/AllowList", flatArray)

	doc := newTestDoc(map[string]*testOp{
		"/allowList": {opID: "allowList_get", resp: flatArrayRef},
	})

	candidates, _, err := DiscoverDataSources(doc, "azure")
	if err != nil {
		t.Fatalf("DiscoverDataSources: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate, got %d: %v", len(candidates), candidates)
	}
	if got := candidates[0].TypeName; got != "azure_allow_list" {
		t.Errorf("expected the flat-array AllowList to keep its own real name \"azure_allow_list\" (not unwrapped), got %q", got)
	}
}
