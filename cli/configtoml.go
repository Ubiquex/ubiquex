package cli

import "github.com/BurntSushi/toml"

// parseTOMLGeneric parses a TOML config file into a genericTree.
// BurntSushi/toml already decodes nested tables as nested
// map[string]interface{} when the destination is a generic map -- since
// genericTree is a plain alias for that exact type (`type genericTree =
// map[string]any`), no conversion step is needed at all; the decoded
// value already IS a genericTree, recursively.
func parseTOMLGeneric(path string) (genericTree, error) {
	var tree genericTree
	if _, err := toml.DecodeFile(path, &tree); err != nil {
		return nil, err
	}
	if tree == nil {
		tree = genericTree{}
	}
	return tree, nil
}
