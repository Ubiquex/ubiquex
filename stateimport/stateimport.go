// Package stateimport parses Terraform state v4 JSON — UBI-18's bulk
// onboarding enumeration source (docs/architecture.md — Bulk onboarding).
// This file is read exactly once, at onboarding: a border-crossing
// artifact, never an ongoing dependency. ubx never opens a state file
// again after this package hands back the resources in it; the ledger
// owns everything from that point on. Named for its role (importing
// identity to bootstrap onboarding), not the Terraform file format it
// happens to read (UBI-52 — docs/source-tree.md).
package stateimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrMalformedState wraps every parse-time failure — genuinely
// unparseable JSON, or an unsupported state format version. Anything
// else "wrong" about a state file's content (a resource missing its own
// id, an unrecognized type) surfaces later, per resource, as an ordinary
// skip — never a hard failure here (docs/architecture.md: "the walk
// never aborts").
var ErrMalformedState = errors.New("malformed terraform state")

// ErrNoIdentity means a state instance's attributes have no id value at
// all — genuinely malformed data, distinct from simply lacking one of
// BuildLookup's augmentation table's extra fields.
var ErrNoIdentity = errors.New("state instance has no id attribute")

// ManagedResource is one managed-resource instance extracted from a state
// file — exactly what the bulk-onboarding walk needs, nothing more.
type ManagedResource struct {
	// Type/Name are Terraform's own resource type and name. Name already
	// folds in a count/for_each index using Terraform's own address
	// convention (e.g. "web[0]", `web["us-east-1"]`) — ubx's own Address
	// has no separate index concept, so every instance needs its own
	// distinct name to become its own adoptable resource.
	Type string
	Name string

	// Module is the dotted module path this instance lives in ("" for
	// the root module, "network" for module.network, "network.subnet"
	// for module.network.module.subnet) — recorded as an intent.summary
	// hint by the caller, never an automatic stack split
	// (docs/architecture.md).
	Module string

	// Attributes is this instance's raw recorded state attributes — used
	// only to build a lookup key (BuildLookup). Never trusted as the
	// resource's actual current state, which always comes from a live
	// ReadResource call instead (state provides identity, never truth).
	Attributes map[string]json.RawMessage
}

// stateV4 is exactly the subset of Terraform's state v4 JSON shape ubx
// reads. Anything else in a real state file (outputs, lineage, serial,
// check_results, ...) is simply never unmarshaled into this — Go's
// decoder ignores unrecognized fields by default, which is exactly the
// "read only what we need" posture this package is scoped to.
type stateV4 struct {
	Version   int              `json:"version"`
	Resources []resourceRecord `json:"resources"`
}

type resourceRecord struct {
	Module    string           `json:"module"`
	Mode      string           `json:"mode"`
	Type      string           `json:"type"`
	Name      string           `json:"name"`
	Instances []instanceRecord `json:"instances"`
}

type instanceRecord struct {
	IndexKey   json.RawMessage            `json:"index_key"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// Parse decodes a Terraform state v4 JSON document into its managed
// resource instances, in file order. "mode":"data" (data sources) and any
// other non-"managed" entry is dropped outright — they aren't resources
// ubx could ever adopt into a ledger.
func Parse(data []byte) ([]ManagedResource, error) {
	var raw stateV4
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedState, err)
	}
	if raw.Version != 4 {
		return nil, fmt.Errorf("%w: unsupported state version %d (only v4 is supported)", ErrMalformedState, raw.Version)
	}

	var out []ManagedResource
	for _, r := range raw.Resources {
		if r.Mode != "managed" {
			continue
		}
		module := modulePath(r.Module)
		for _, inst := range r.Instances {
			name := r.Name
			if len(inst.IndexKey) > 0 {
				name = fmt.Sprintf("%s[%s]", r.Name, indexKeyString(inst.IndexKey))
			}
			out = append(out, ManagedResource{
				Type:       r.Type,
				Name:       name,
				Module:     module,
				Attributes: inst.Attributes,
			})
		}
	}
	return out, nil
}

// modulePath turns Terraform's own dotted module-address form
// ("module.network" or "module.network.module.subnet") into a plain
// dotted path ("network" or "network.subnet"). "" (the root module,
// which carries no "module" field in a real state file) stays "".
func modulePath(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ".")
	var names []string
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] == "module" {
			names = append(names, parts[i+1])
		}
	}
	return strings.Join(names, ".")
}

// indexKeyString renders a count/for_each index_key the way Terraform's
// own resource addresses do: a bare number for count ("0"), a quoted
// string for for_each (`"us-east-1"`).
func indexKeyString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return `"` + s + `"`
	}
	return string(raw)
}

// extraLookupAttrs augments the default {"id": ...} lookup for the small
// set of types empirically known (conformance/registry.go's Notes,
// pinned in ubiquex-docs' cli/lookup.mdx) to need more than their bare
// id. This is NOT mechanically derived from conformance.TypeSpec's
// IdentityFields, which answers a related but distinct question (which
// attributes carry stable identity for CloudTrail attribution) — the two
// don't always coincide: aws_sqs_queue's IdentityFields includes "url" as
// a distinct field, but its lookup needs no separate "url" key at all,
// since "id" already equals it. Every other RealSafe type with additional
// IdentityFields (aws_iam_policy, aws_sqs_queue, aws_sns_topic, aws_vpc)
// needs no augmentation here for the same reason: its bare id already is
// what an extra field would have contributed. See docs/architecture.md's
// "Building a lookup from state, per type".
var extraLookupAttrs = map[string][]string{
	"aws_s3_bucket": {"bucket"},
	"aws_iam_role":  {"name"},
	"aws_iam_user":  {"name"},
}

// BuildLookup constructs the ReadResource lookup for one managed
// resource, from its own recorded state attributes.
func BuildLookup(resourceType string, attrs map[string]json.RawMessage) (json.RawMessage, error) {
	id, ok := attrs["id"]
	if !ok || len(id) == 0 || string(id) == "null" {
		return nil, ErrNoIdentity
	}
	lookup := map[string]json.RawMessage{"id": id}
	for _, extra := range extraLookupAttrs[resourceType] {
		if v, ok := attrs[extra]; ok {
			lookup[extra] = v
		}
	}
	b, err := json.Marshal(lookup)
	if err != nil {
		return nil, fmt.Errorf("build lookup: %w", err)
	}
	return b, nil
}
