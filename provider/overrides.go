package provider

// SensitiveOverride names one attribute path ubx treats as sensitive
// regardless of what a provider's own schema says (UBI-24,
// docs/architecture.md -- "Sensitive overrides"). Data, not code -- the
// same posture core/lookuphints already established for teaching-error
// hints (UBI-20): a small, committed, human-readable table.
//
// Path is dot-notation into a resource's OBSERVED JSON (the same
// convention Modification.Before/After and core.dotSet already use),
// not a provider.Attribute reference -- an override exists precisely for
// an attribute the schema itself doesn't (or, for a compound-typed
// attribute like helm_release's own metadata, structurally CAN'T) mark
// Sensitive, so there's no Attribute.Sensitive field to point at. If a
// path segment along the way is a JSON array (a NestingList/NestingSet
// block, or a compound list(object(...))-typed attribute -- Redact's own
// path-walker doesn't distinguish the two, see redactOverridePaths),
// the remaining path is applied to every element.
type SensitiveOverride struct {
	Source string // e.g. "hashicorp/helm" -- matches conformance.Registry/core/lookuphints' own keying (UBI-21)
	Type   string // e.g. "helm_release"
	Path   string // e.g. "metadata.values"
}

// SensitiveOverrides is the whole table. Seeded from UBI-22's own
// finding: helm_release's manifest (the chart's full rendered YAML) and
// metadata.values/metadata.notes (the release's resolved values and any
// chart notes) can carry a Sensitive value's plaintext once a chart
// template renders it into output, yet none of the three is
// Sensitive-flagged in the real hashicorp/helm 2.17.0 schema. A direct
// audit of both hashicorp/kubernetes's ~20 registered types and
// hashicorp/helm (UBI-24) found no further candidates -- every other
// computed attribute checked is structural/identity data the provider
// itself derives, never a rendering of operator-supplied values the way
// these three are (see docs/architecture.md for the full audit writeup).
var SensitiveOverrides = []SensitiveOverride{
	{Source: "hashicorp/helm", Type: "helm_release", Path: "manifest"},
	{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.values"},
	{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.notes"},
}

// OverridePathsFor returns the dot-notation paths to force-redact for
// (source, type), or nil if none are registered.
func OverridePathsFor(source, typeName string) []string {
	var out []string
	for _, o := range SensitiveOverrides {
		if o.Source == source && o.Type == typeName {
			out = append(out, o.Path)
		}
	}
	return out
}
