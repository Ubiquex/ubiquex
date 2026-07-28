package diagram

import (
	"fmt"
	"strings"
)

// parseCrossRefLabel extracts the "<stack>.<type>.<name>" address a
// reference node names from its own label -- docs/diagram-medium.md's
// own cross-stack grammar: a node is a reference "when EITHER its label
// starts with @ ... OR its class is exactly external ... both together
// ... is the canonical/emitted form; either alone is accepted on
// parse." A "class: external" node whose own label carries no "@"
// prefix (e.g. a plain human-readable label with no address in it at
// all) is a genuine authoring mistake, not a guess to paper over --
// refused with a clear message.
func parseCrossRefLabel(label string) (string, error) {
	if !strings.HasPrefix(label, "@") {
		return "", fmt.Errorf("reference node (class: external) has label %q, expected \"@<stack>.<type>.<name>\"", label)
	}
	addr := strings.TrimPrefix(label, "@")
	if addr == "" {
		return "", fmt.Errorf("reference node has an empty address after \"@\"")
	}
	return addr, nil
}
