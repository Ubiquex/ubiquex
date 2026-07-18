package cli

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// parseYAMLGeneric parses a YAML config file into a genericTree, in
// strict mode: implicit type coercion that would silently lose
// information is refused as a hard error, never guessed (UBI-19's
// original determinism objection to YAML, carried forward rather than
// dropped now that YAML is finally supported).
//
// Confirmed directly against gopkg.in/yaml.v3 before writing this,
// rather than assumed from YAML's general reputation: its own implicit
// resolver already treats `no`/`yes`/`on`/`off` (any case) as `!!str`,
// never `!!bool` -- the classic YAML 1.1 Norway-problem ambiguity this
// project worried about doesn't actually exist in this specific library,
// so there is nothing to reject for that case. What IS real: a bare,
// unquoted `6.60` silently decodes as `float64(6.6)` -- the distinction
// between "6.60" and "6.6" as a provider version string is gone with no
// error raised anywhere. That's the one real coercion risk this parser
// guards against, via a round-trip check on every plain (unquoted)
// numeric scalar: format the resolved number back to text canonically
// and compare against the token actually written; any mismatch is a
// hard error. A quoted scalar is exempt by construction -- quoting is
// exactly how a YAML author already asserts "treat this as a string."
func parseYAMLGeneric(path string) (genericTree, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return genericTree{}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level must be a mapping (key: value pairs), not %s", path, yamlKindName(root.Kind))
	}
	return yamlMappingToGeneric(root, path)
}

func yamlMappingToGeneric(node *yaml.Node, path string) (genericTree, error) {
	out := genericTree{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		v, err := yamlNodeToGeneric(valNode, path, keyNode.Value)
		if err != nil {
			return nil, err
		}
		out[keyNode.Value] = v
	}
	return out, nil
}

func yamlNodeToGeneric(node *yaml.Node, path, key string) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		return yamlMappingToGeneric(node, path)
	case yaml.SequenceNode:
		out := make([]any, 0, len(node.Content))
		for _, c := range node.Content {
			v, err := yamlNodeToGeneric(c, path, key)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.AliasNode:
		return yamlNodeToGeneric(node.Alias, path, key)
	case yaml.ScalarNode:
		return yamlScalarToGeneric(node, path, key)
	default:
		return nil, fmt.Errorf("%s: %s: unsupported YAML node", path, key)
	}
}

// yamlScalarToGeneric is strict mode's real enforcement point.
func yamlScalarToGeneric(node *yaml.Node, path, key string) (any, error) {
	quoted := node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
	if quoted {
		// The author explicitly asserted "this is a string" by quoting
		// it -- never coerced, never round-trip-checked.
		return node.Value, nil
	}
	switch node.Tag {
	case "!!bool":
		b, err := strconv.ParseBool(node.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: ambiguous boolean %q", path, key, node.Value)
		}
		return b, nil
	case "!!int":
		n, err := strconv.ParseInt(node.Value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %q resolves as a number this parser doesn't trust: %v", path, key, node.Value, err)
		}
		if strconv.FormatInt(n, 10) != node.Value {
			return nil, fmt.Errorf("%s: %s: %q would silently narrow to %d -- quote it if a string was meant, or write it as %d if a number was meant", path, key, node.Value, n, n)
		}
		return float64(n), nil
	case "!!float":
		f, err := strconv.ParseFloat(node.Value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %q resolves as a number this parser doesn't trust: %v", path, key, node.Value, err)
		}
		if strconv.FormatFloat(f, 'g', -1, 64) != node.Value {
			return nil, fmt.Errorf("%s: %s: %q would silently narrow to %v -- quote it if a string was meant (e.g. a version like \"6.60\"), or write its exact canonical form if a number was meant", path, key, node.Value, f)
		}
		return f, nil
	case "!!null":
		return nil, nil
	default: // "!!str" and anything else this resolver ever produces
		return node.Value, nil
	}
}

func yamlKindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	case yaml.DocumentNode:
		return "a document"
	default:
		return "an unrecognized node"
	}
}
