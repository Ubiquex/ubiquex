// emit.go is UBI-47 slice 4's own half of the diagram medium: the render
// direction, docs/diagram-medium.md's own "FoldState -> canonical D2" --
// the literal converse of parse.go's own D2-to-intent translation, walking
// a stack's own live resources (core.Ledger.Fleet + FoldState, the same
// read `ubx status`'s own fleet walk already performs) and producing the
// canonical D2 text `ubx render` writes out, one flat top-level node per
// live resource -- no synthetic containers, the same "no canonical
// grouping basis to invent" reasoning docs/diagram-medium.md's own render-
// direction section already gives.
package diagram

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"oss.terrastruct.com/d2/d2format"
	"oss.terrastruct.com/d2/d2parser"

	"github.com/ubiquex/ubiquex/core"
)

// emitResource is one live resource's own emit-time view, gathered before
// any D2 text is built -- keeps the text-building pass itself free of any
// further ledger reads, matching parse.go's own two-pass shape (classify
// everything first, translate second).
type emitResource struct {
	addr      core.Address
	attrs     map[string]interface{}
	dependsOn []string // canonical addresses, this resource's own stack only (a $ref-derived depends_on could in principle be empty; a $cross-derived one never contributes here, see below)
	crossPins []core.ResolutionInput
}

// Emit walks stack's own live resources in l and produces the canonical
// D2 rendering of their current, resolved shape -- deterministic (parsed
// through d2parser then re-serialized via d2format.Format, the identical
// idempotent canonical formatter docs/diagram-medium.md confirmed
// empirically parse.go already relies on) so two Emit calls against an
// unchanged ledger produce byte-identical output, the property
// `ubx render --check` needs.
//
// No synthetic containers, no synthetic depends_on: every edge drawn here
// is real, resolved-time truth pulled from the resource's own creating/
// most-recently-modifying proposal -- never re-derived by guessing at the
// diagram's own original authored structure, which Emit has no access to
// and shouldn't need (a human editing the emitted file by hand can add
// their own grouping afterward, the identical "generated, reviewable,
// human-editable-adjacent artifact" posture `ubx sdk gen`'s own bindings
// already established).
func Emit(l *core.Ledger, stack string) ([]byte, error) {
	fleet, err := l.Fleet(stack)
	if err != nil {
		return nil, fmt.Errorf("diagram: emit %s: %w", stack, err)
	}

	sort.Slice(fleet, func(i, j int) bool {
		if fleet[i].Address.Type != fleet[j].Address.Type {
			return fleet[i].Address.Type < fleet[j].Address.Type
		}
		return fleet[i].Address.Name < fleet[j].Address.Name
	})

	proposals := map[string]*core.Proposal{}
	getProposal := func(id string) (*core.Proposal, error) {
		if id == "" {
			return nil, nil
		}
		if p, ok := proposals[id]; ok {
			return p, nil
		}
		p, err := l.Read(id)
		if err != nil {
			return nil, err
		}
		proposals[id] = p
		return p, nil
	}

	resources := make([]emitResource, 0, len(fleet))
	for _, entry := range fleet {
		state, found, err := l.FoldState(entry.Address)
		if err != nil {
			return nil, fmt.Errorf("diagram: emit %s: %w", entry.Address, err)
		}
		if !found {
			return nil, fmt.Errorf("diagram: emit %s: reported live by Fleet but FoldState found nothing", entry.Address)
		}
		var attrs map[string]interface{}
		if err := json.Unmarshal(state, &attrs); err != nil {
			return nil, fmt.Errorf("diagram: emit %s: decode state: %w", entry.Address, err)
		}

		p, err := getProposal(entry.ProposalID)
		if err != nil {
			return nil, fmt.Errorf("diagram: emit %s: read proposal %s: %w", entry.Address, entry.ProposalID, err)
		}

		var dependsOn []string
		var crossPins []core.ResolutionInput
		if p != nil {
			dependsOn = dependsOnFor(p, entry.Address)
			addrStr := entry.Address.String()
			for _, in := range p.Resolution.Inputs {
				if in.Kind == "cross_stack_pin" && in.From == addrStr {
					crossPins = append(crossPins, in)
				}
			}
		}

		resources = append(resources, emitResource{
			addr:      entry.Address,
			attrs:     attrs,
			dependsOn: dependsOn,
			crossPins: crossPins,
		})
	}

	return emitD2(stack, resources)
}

// dependsOnFor finds addr's own depends_on list within p's Delta -- a
// create node's own {stack,type,name,depends_on} (decoded ad hoc, the
// exact same shape resolver.go's own resolveOnce builds it in) or a
// modify's own typed Modification.DependsOn. Neither found (addr's
// latest-touching proposal recorded it some other way -- e.g. a bare
// live_state resolution.inputs touch, never a create/modify of addr
// itself) returns nil, never an error: a resource rendering without
// dependency edges is a legitimate, if less complete, rendering, not a
// failure.
func dependsOnFor(p *core.Proposal, addr core.Address) []string {
	if p.Kind != core.KindChange {
		return nil
	}
	for _, raw := range p.Delta.Creates {
		var node struct {
			Stack     string   `json:"stack"`
			Type      string   `json:"type"`
			Name      string   `json:"name"`
			DependsOn []string `json:"depends_on"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		if node.Stack == addr.Stack && node.Type == addr.Type && node.Name == addr.Name {
			return node.DependsOn
		}
	}
	for i := range p.Delta.Modifies {
		if p.Delta.Modifies[i].Target == addr {
			return p.Delta.Modifies[i].DependsOn
		}
	}
	return nil
}

// emitD2 builds the actual D2 source text (deterministic key assignment,
// classes block, node blocks, edges) and runs it through d2parser ->
// d2format.Format for the canonical byte form -- reusing D2's own
// formatter as the canonical serializer rather than hand-rolling one,
// docs/diagram-medium.md's own confirmed-idempotent reasoning.
func emitD2(stack string, resources []emitResource) ([]byte, error) {
	// Deterministic key assignment: r0..rN in the already (type, name)-
	// sorted resource order, never the resource's own name -- a synthetic,
	// collision-free key by construction, since two different-typed
	// resources can legally share the same Name (only the full (type,
	// name) pair is unique), and a D2 key built by joining them with "."
	// would collide with D2's own container-nesting separator (the exact
	// trap docs/diagram-medium.md's own canonical-subset section already
	// found and avoided on the parse side). The resource's own name still
	// renders in full, as the node's own D2 label -- nothing about
	// readability is lost, only the internal key is synthetic.
	keyOf := make(map[string]string, len(resources)) // addr.String() -> D2 key
	for i, r := range resources {
		keyOf[r.addr.String()] = fmt.Sprintf("r%d", i)
	}

	// Reference nodes: one per distinct neighbor address any resource's
	// own cross_stack_pin(s) name, deduplicated -- two resources pinning
	// the identical neighbor address share one reference node rather than
	// drawing a redundant duplicate, a deliberate rendering choice (never
	// mandated by docs/diagram-medium.md's own render-direction text,
	// which only says a reference node gets annotated with its pin, not
	// how many copies of it a diagram with multiple referencing resources
	// should draw).
	refPins := map[string]core.ResolutionInput{} // neighbor addr.String() -> its own cross_stack_pin (pinned_head/ledger_dir)
	for _, r := range resources {
		for _, pin := range r.crossPins {
			refPins[pin.Resource] = pin
		}
	}
	refAddrs := make([]string, 0, len(refPins))
	for addr := range refPins {
		refAddrs = append(refAddrs, addr)
	}
	sort.Strings(refAddrs)
	refKeyOf := make(map[string]string, len(refAddrs))
	for i, addr := range refAddrs {
		refKeyOf[addr] = fmt.Sprintf("ref%d", i)
	}

	var b strings.Builder

	classes := make(map[string]bool, len(resources)+1)
	for _, r := range resources {
		classes[r.addr.Type] = true
	}
	if len(refAddrs) > 0 {
		classes["external"] = true
	}
	if len(classes) > 0 {
		classNames := make([]string, 0, len(classes))
		for c := range classes {
			classNames = append(classNames, c)
		}
		sort.Strings(classNames)
		b.WriteString("classes: {\n")
		for _, c := range classNames {
			fmt.Fprintf(&b, "  %s: {}\n", c)
		}
		b.WriteString("}\n")
	}

	for i, r := range resources {
		key := fmt.Sprintf("r%d", i)
		fmt.Fprintf(&b, "%s: %s {\n", key, d2Quote(r.addr.Name))
		fmt.Fprintf(&b, "  class: %s\n", r.addr.Type)
		if tooltip := attrTooltip(r.attrs); tooltip != "" {
			fmt.Fprintf(&b, "  tooltip: %s\n", d2Quote(tooltip))
		}
		b.WriteString("}\n")
	}
	for _, addr := range refAddrs {
		pin := refPins[addr]
		fmt.Fprintf(&b, "%s: %s {\n", refKeyOf[addr], d2Quote("@"+addr))
		b.WriteString("  class: external\n")
		fmt.Fprintf(&b, "  tooltip: %s\n", d2Quote("pinned_head: "+pin.PinnedHead))
		b.WriteString("}\n")
	}

	type edge struct{ from, to string }
	var edges []edge
	for _, r := range resources {
		from := keyOf[r.addr.String()]
		for _, dep := range r.dependsOn {
			to, ok := keyOf[dep]
			if !ok {
				// A depends_on target outside this stack's own live fleet
				// (shouldn't happen -- resolve-time validation already
				// requires every depends_on address to exist -- but Emit
				// never hard-fails a whole diagram over one stale edge,
				// matching the rest of this function's "annotate, don't
				// refuse" posture).
				continue
			}
			edges = append(edges, edge{from: from, to: to})
		}
		for _, pin := range r.crossPins {
			edges = append(edges, edge{from: from, to: refKeyOf[pin.Resource]})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})
	for _, e := range edges {
		fmt.Fprintf(&b, "%s -> %s\n", e.from, e.to)
	}

	m, err := d2parser.Parse(stack+".d2", strings.NewReader(b.String()), nil)
	if err != nil {
		return nil, fmt.Errorf("diagram: emit %s: internal: constructed invalid D2: %w", stack, err)
	}
	return []byte(d2format.Format(m)), nil
}

// attrTooltip summarizes attrs' own top-level keys, sorted, as a single-
// line "key: value; key: value" string -- the render direction's own
// "attribute annotations, real current ledger values" half of the lossy-
// medium rule (docs/diagram-medium.md), rendered via D2's own tooltip:
// attribute (a hover annotation, deliberately not the visible label --
// keeps the diagram itself scannable, attributes available on demand).
// Only top-level keys: a deeply nested object's own full contents would
// make an already-long tooltip unwieldy, and every top-level key is
// already enough to identify which attribute to go inspect via `ubx why`
// for full detail. Empty for a resource with no attributes at all
// (returns "").
func attrTooltip(attrs map[string]interface{}) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+scalarString(attrs[k]))
	}
	return strings.Join(parts, "; ")
}

// scalarString renders v for a tooltip: a plain string as itself (no
// added quotes -- easier to read in a hover tooltip than an escaped JSON
// literal), anything else (number, bool, null, nested object/array) via
// compact JSON, the same "don't invent a second serialization" reasoning
// this whole arc already applies to reusing d2format over hand-rolling
// one.
func scalarString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// d2Quote wraps s as a D2 double-quoted string literal -- backslash and
// double-quote escaped, real newlines escaped too (an attribute value
// containing one would otherwise break the single-line quoted form this
// function always produces). Emit never emits a bare/unquoted label or
// key: quoting uniformly is always valid D2 and sidesteps needing to know
// D2's own bare-identifier grammar rules at all, the same reasoning
// parse.go's own cross-stack label handling already leans on.
func d2Quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}
