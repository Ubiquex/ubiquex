package cli

import (
	"encoding/json"
	"io"

	"github.com/ubiquex/ubiquex/core"
)

// jsonFormatVersion is the schema version stamped into every --json
// payload's top-level "format" field (UBI-20 workstream 2,
// docs/architecture.md — --json on scan, status, why). It is a schema
// version, distinct from the product's own Version -- bumped only when a
// verb's JSON *shape* changes incompatibly, so a scripted consumer can
// check it once and know exactly what fields to expect.
const jsonFormatVersion = 1

// addressJSON is core.Address's --json rendering -- broken into its three
// fields rather than the single dotted string the human output uses, so a
// consumer never has to re-parse a "stack.type.name" string (which, unlike
// the CLI argument form, has no escaping rule for a name containing a dot
// itself -- see core.ParseAddress).
type addressJSON struct {
	Stack string `json:"stack"`
	Type  string `json:"type"`
	Name  string `json:"name"`
}

func addressToJSON(a core.Address) addressJSON {
	return addressJSON{Stack: a.Stack, Type: a.Type, Name: a.Name}
}

// writeJSON marshals v as indented JSON followed by a trailing newline --
// the same shape `json.MarshalIndent` plus `fmt.Fprintln` would produce,
// factored out since every --json code path needs exactly this.
func writeJSON(out io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = out.Write(append(b, '\n'))
	return err
}
