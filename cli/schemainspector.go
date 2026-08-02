package cli

import (
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// schemaInspectorAdapter implements resolver.SchemaInspector against a
// real *provider.Schemas dump -- the one place that needs both packages,
// same "adapter lives at the boundary" posture stateReaderAdapter already
// established for core.StateReader/executor.Applier (UBI-23/26): core and
// core/resolver stay provider-import-free; only cli bridges them.
type schemaInspectorAdapter struct {
	schemas *provider.Schemas
}

func newSchemaInspector(schemas *provider.Schemas) resolver.SchemaInspector {
	return schemaInspectorAdapter{schemas: schemas}
}

func (s schemaInspectorAdapter) HasType(typeName string) bool {
	_, ok := s.schemas.Resources[typeName]
	return ok
}

func (s schemaInspectorAdapter) IsComputed(typeName, attrPath string) bool {
	rs, ok := s.schemas.Resources[typeName]
	if !ok {
		return false
	}
	a, ok := attributeAt(rs.Block, attrPath)
	return ok && a.Computed
}

func (s schemaInspectorAdapter) IsSensitive(typeName, attrPath string) bool {
	rs, ok := s.schemas.Resources[typeName]
	if !ok {
		return false
	}
	a, ok := attributeAt(rs.Block, attrPath)
	return ok && a.Sensitive
}

// UnknownConfigKeys delegates to provider.UnknownConfigKeys (UBI-66)
// against typeName's own real schema Block -- the same recursive,
// suggestion-carrying walk provider/schemakeys.go implements once, not
// reimplemented here. An unknown type or a nil config reports no issues:
// resolver.InferProvider's own ErrUnknownType already refuses the former
// before this is ever asked, and resolveOnce's later "config must be a
// JSON object" check is where the latter is caught instead.
func (s schemaInspectorAdapter) UnknownConfigKeys(typeName string, config map[string]interface{}) []resolver.ConfigKeyIssue {
	rs, ok := s.schemas.Resources[typeName]
	if !ok || config == nil {
		return nil
	}
	issues := provider.UnknownConfigKeys(rs.Block, config)
	if len(issues) == 0 {
		return nil
	}
	out := make([]resolver.ConfigKeyIssue, len(issues))
	for i, iss := range issues {
		out[i] = resolver.ConfigKeyIssue{Path: iss.Path, Suggestion: iss.Suggestion}
	}
	return out
}

// MissingRequiredKeys delegates to provider.MissingRequiredKeys (UBI-90)
// against typeName's own real schema Block -- the same recursive walk
// provider/schemakeys.go implements once, not reimplemented here. An
// unknown type or a nil config reports no issues, same reasoning as
// UnknownConfigKeys immediately above.
func (s schemaInspectorAdapter) MissingRequiredKeys(typeName string, config map[string]interface{}) []resolver.RequiredAttributeIssue {
	rs, ok := s.schemas.Resources[typeName]
	if !ok || config == nil {
		return nil
	}
	issues := provider.MissingRequiredKeys(rs.Block, config)
	if len(issues) == 0 {
		return nil
	}
	out := make([]resolver.RequiredAttributeIssue, len(issues))
	for i, iss := range issues {
		out[i] = resolver.RequiredAttributeIssue{Path: iss.Path}
	}
	return out
}

// resourceTypeSchemaInspector implements resolver.SchemaInspector against
// the type-erased resourceSchemas map executor.Applier.Schema returns
// (map[string]any, each value a concrete *provider.Schema boxed as any --
// cli/stateadapter.go's own stateReaderAdapter.Schema) -- NOT the
// concrete *provider.Schemas schemaInspectorAdapter needs, which a
// providerPool-launched Applier never hands back (UBI-43 session 5,
// cli/status.go's own multi-provider fleet-grouping). HasType is a plain
// map lookup; IsComputed/IsSensitive are harmless always-false stubs,
// since resolver.InferProvider -- the only caller this adapter is ever
// used for -- calls HasType alone, never the other two. Reusing the
// already-launched pool entry this way avoids launching a second copy of
// every declared provider just to answer "who owns this type."
type resourceTypeSchemaInspector struct {
	resourceSchemas map[string]any
}

func (s resourceTypeSchemaInspector) HasType(typeName string) bool {
	_, ok := s.resourceSchemas[typeName]
	return ok
}

func (s resourceTypeSchemaInspector) IsComputed(typeName, attrPath string) bool { return false }

func (s resourceTypeSchemaInspector) IsSensitive(typeName, attrPath string) bool { return false }

// UnknownConfigKeys is an always-nil stub, matching IsComputed/
// IsSensitive above: resolver.InferProvider -- the only caller this
// adapter is ever used for (cli/status.go's/cli/scanall.go's own
// multi-provider fleet-grouping, never a real resolver.Resolve call) --
// never calls it.
func (s resourceTypeSchemaInspector) UnknownConfigKeys(typeName string, config map[string]interface{}) []resolver.ConfigKeyIssue {
	return nil
}

// MissingRequiredKeys is an always-nil stub, matching UnknownConfigKeys
// immediately above -- resolver.InferProvider, the only caller this
// adapter is ever used for, never calls it either.
func (s resourceTypeSchemaInspector) MissingRequiredKeys(typeName string, config map[string]interface{}) []resolver.RequiredAttributeIssue {
	return nil
}

// attributeAt walks a dot-notation path into a schema Block, recursing
// into NestedBlocks the same way provider.Redact/blockObjectType already
// do -- a path can only ever bottom out at a plain Attribute (a nested
// block itself, named alone with nothing after it, has no Computed/
// Sensitive flag of its own to report).
func attributeAt(block provider.Block, path string) (provider.Attribute, bool) {
	name, rest, hasRest := strings.Cut(path, ".")
	for _, a := range block.Attributes {
		if a.Name == name {
			if hasRest {
				return provider.Attribute{}, false
			}
			return a, true
		}
	}
	for _, nb := range block.NestedBlocks {
		if nb.TypeName == name {
			if !hasRest {
				return provider.Attribute{}, false
			}
			return attributeAt(nb.Block, rest)
		}
	}
	return provider.Attribute{}, false
}
