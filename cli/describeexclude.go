// Real, general, config-declared exclusion mechanism for description
// generation -- any provider's own config may declare a real
// describe_exclude list naming resource wire types whose real field
// count is pathological relative to their real usage (deeply-nested
// visualization/configuration schemas nobody hand-authors, confirmed
// live for AWS's own CloudFormation-sourced QuickSight resources, see
// descriptionCoverage.Excluded's own doc comment). Deliberately NOT an
// AWS special case: this file has no AWS-specific logic at all, only a
// generic map[string]any -> map[string]bool extraction any provider's
// own config table can populate. What counts as "pathological" is a
// real, human, config-authored judgment call -- this package never
// infers it from field counts or any other heuristic.
package cli

import "github.com/ubiquex/ubiquex/sdk/codegen/ir"

// describeExcludeKey is the real, well-known config key this mechanism
// reads -- a [dynamic_providers.<name>] table's own top-level key
// (alongside schema_source/schema_url/base_url/...) for a dynamic
// provider, or a [provider_configs.<source>] table's own key for a real
// Terraform-registry (thirdparty) provider. Same key, same shape, same
// extraction code either way -- see generateOneProvider/
// generateOneDynamicProvider's own call sites (cli/sdk.go).
const describeExcludeKey = "describe_exclude"

// describeExcludeFromParams extracts a real describe_exclude list from
// params -- a real string array in TOML decodes to []any of string
// values (BurntSushi/toml's own real decode shape, the same generic
// map[string]any every other per-provider config value in this pipeline
// already flows through). Absent, wrong-typed, or empty all resolve to
// nil (no exclusion) -- a config author's typo here fails open (nothing
// excluded, coverage stays real and complete) rather than silently
// skipping resources no one meant to skip; this package makes no
// attempt to warn on a malformed entry, since a nil result is
// indistinguishable from "never declared" and either is a safe default.
func describeExcludeFromParams(params map[string]any) map[string]bool {
	if params == nil {
		return nil
	}
	raw, ok := params[describeExcludeKey]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]bool, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			out[s] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// partitionDescribeTypes splits types into describeTypes (every resource
// NOT named in exclude -- these alone ever reach enrichDescriptions) and
// excludedTypes (named in exclude -- codegen still receives the full,
// unfiltered original types slice unchanged; only description
// generation ever sees this split). Order is preserved within each
// output slice -- types is already sorted by writeGeneratedSDK's own
// caller, and this function must not reintroduce map-iteration
// nondeterminism.
func partitionDescribeTypes(types []*ir.ResourceType, exclude map[string]bool) (describeTypes, excludedTypes []*ir.ResourceType) {
	if len(exclude) == 0 {
		return types, nil
	}
	describeTypes = make([]*ir.ResourceType, 0, len(types))
	for _, rt := range types {
		if exclude[rt.WireType] {
			excludedTypes = append(excludedTypes, rt)
			continue
		}
		describeTypes = append(describeTypes, rt)
	}
	return describeTypes, excludedTypes
}

// countAllFields recursively counts every real field in fields,
// regardless of DescriptionSource -- the identical real recursive walk
// countExisting/walkNested (cli/sdkdescribe.go) already use for the
// Sourced tally, reused here (not reinvented) to give Excluded's own
// real field count using the exact same "what counts as a field" rule
// as every other bucket in descriptionCoverage.
func countAllFields(fields []ir.Field) int {
	n := len(fields)
	for i := range fields {
		walkNested(fields[i].Type, func(nested []ir.Field) {
			n += countAllFields(nested)
		})
	}
	return n
}
