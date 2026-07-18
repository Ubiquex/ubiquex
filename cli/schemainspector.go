package cli

import (
	"strings"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/provider"
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
