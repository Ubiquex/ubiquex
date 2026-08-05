// outputs.go is UBI-128's own run-time half: resolving a blueprint call's
// own declared outputs: into real addresses, and rewriting every
// PROVISIONAL output $ref a calling medium (the diagram medium, so far)
// may have emitted into a real one. See ubxfile.go's own Output type and
// docs/blueprint.md's "outputs:" section for the full design.
package blueprint

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// blueprintOutputRefPrefix is resolver.BlueprintOutputRefPrefix (core/
// resolver/resolver.go has the full account of the marker's own shape
// and why it's exported from there rather than owned privately here) --
// aliased locally so every reference below stays as short as before that
// move.
const blueprintOutputRefPrefix = resolver.BlueprintOutputRefPrefix

// resolveCallOutputs builds outputKey -> real resolved "<stack>.<type>.
// <name>.<attr>[.<nested>...]" address for ONE call's own declared
// outputs (ubxfile.Outputs), matched by SLUG against the resources this
// SAME call just produced (real types/names, fresh from actually
// invoking the blueprint -- Ubxfile parsing alone has no type
// information at all, see Output's own doc comment). Returns an empty,
// non-nil map (never an error) when the blueprint declares no outputs --
// the overwhelming common case, completely unaffected.
func resolveCallOutputs(callingStack string, ubxfile *Ubxfile, resources []resolver.ResourceIntent) (map[string]string, error) {
	out := make(map[string]string, len(ubxfile.Outputs))
	if len(ubxfile.Outputs) == 0 {
		return out, nil
	}

	byName := map[string]resolver.ResourceIntent{}
	ambiguous := map[string]bool{}
	for _, ri := range resources {
		if _, ok := byName[ri.Name]; ok {
			ambiguous[ri.Name] = true
		}
		byName[ri.Name] = ri
	}

	for _, o := range ubxfile.Outputs {
		slug, attr, ok := strings.Cut(o.Target, ".")
		if !ok {
			return nil, fmt.Errorf("output %q: target %q must be \"<resource-slug>.<attribute>\"", o.Name, o.Target)
		}
		if ambiguous[slug] {
			return nil, fmt.Errorf("output %q: resource slug %q is ambiguous -- more than one resource this call produced shares that name", o.Name, slug)
		}
		ri, found := byName[slug]
		if !found {
			return nil, fmt.Errorf("output %q: no resource with slug %q was produced by this call", o.Name, slug)
		}
		out[o.Name] = fmt.Sprintf("%s.%s.%s.%s", callingStack, ri.Type, ri.Name, attr)
	}
	return out, nil
}

// rewriteBlueprintOutputRefs walks every resource's own Config
// (recursively) looking for a PROVISIONAL $ref (blueprintOutputRefPrefix)
// and replaces it with the real resolved address from outputAddr
// (aggregated across every call in this document, keyed "<CallName>:
// <outputKey>" -- ExpandCalls' own doc comment). Runs ONCE, after every
// BlueprintCalls entry has already been expanded into real resources --
// a placeholder can legitimately be embedded in ANY resource's own
// config, not only ones the SAME call produced (an ordinary
// hand-authored resource referencing a blueprint's own output is exactly
// the point). A placeholder that doesn't resolve against outputAddr is a
// hard, named error -- never silently left as an unresolved marker or
// dropped.
//
// Deliberately does NOT walk into a JSON-embedded ref one level down
// inside a string value (gogen.go's own renderEmbeddedRefString/
// embeddedRefPattern handle that for an ordinary $ref) -- a real,
// narrower scope than ordinary $ref resolution, named here rather than
// silently assumed to work: an output reference nested inside a
// stringified JSON policy document, say, isn't supported yet.
func rewriteBlueprintOutputRefs(intent *resolver.IntentFile, outputAddr map[string]string) error {
	if len(outputAddr) == 0 {
		return nil
	}
	for i := range intent.Resources {
		var v any
		if err := json.Unmarshal(intent.Resources[i].Config, &v); err != nil {
			return fmt.Errorf("resource %s.%s: decode config: %w", intent.Resources[i].Type, intent.Resources[i].Name, err)
		}
		rewritten, changed, err := rewriteOutputRefsInValue(v, outputAddr)
		if err != nil {
			return fmt.Errorf("resource %s.%s: %w", intent.Resources[i].Type, intent.Resources[i].Name, err)
		}
		if !changed {
			continue
		}
		raw, err := json.Marshal(rewritten)
		if err != nil {
			return fmt.Errorf("resource %s.%s: %w", intent.Resources[i].Type, intent.Resources[i].Name, err)
		}
		intent.Resources[i].Config = raw
	}
	return nil
}

func rewriteOutputRefsInValue(v any, outputAddr map[string]string) (any, bool, error) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 1 {
			if refObj, ok := t["$ref"]; ok {
				if refMap, ok := refObj.(map[string]any); ok && len(refMap) == 1 {
					if to, ok := refMap["to"].(string); ok && strings.HasPrefix(to, blueprintOutputRefPrefix) {
						key := strings.TrimPrefix(to, blueprintOutputRefPrefix)
						real, found := outputAddr[key]
						if !found {
							return nil, false, fmt.Errorf("output reference %q: no such blueprint call/output declared in this document", to)
						}
						return map[string]any{"$ref": map[string]any{"to": real}}, true, nil
					}
				}
			}
		}
		changedAny := false
		out := make(map[string]any, len(t))
		for k, vv := range t {
			rv, changed, err := rewriteOutputRefsInValue(vv, outputAddr)
			if err != nil {
				return nil, false, err
			}
			out[k] = rv
			changedAny = changedAny || changed
		}
		return out, changedAny, nil
	case []any:
		changedAny := false
		out := make([]any, len(t))
		for i, vv := range t {
			rv, changed, err := rewriteOutputRefsInValue(vv, outputAddr)
			if err != nil {
				return nil, false, err
			}
			out[i] = rv
			changedAny = changedAny || changed
		}
		return out, changedAny, nil
	default:
		return v, false, nil
	}
}
