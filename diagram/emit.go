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
	// blueprintRef is "" for an ordinary resource; otherwise the
	// "<name>:<content_hash>" blueprint.ExpandCalls stamped this
	// resource's own creating create-node with (UBI-74 Slice 6,
	// resolver.ResourceIntent.Sources -> the "sources" key). Real
	// resolved-time truth pulled from the resource's own creating
	// proposal, the same posture dependsOn/crossPins already have --
	// this is what makes grouping by it consistent with this file's own
	// "no synthetic containers... no canonical grouping basis to invent"
	// principle above, not an exception to it: the grouping basis isn't
	// invented, it's read off the exact same real proposal data.
	blueprintRef string
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

		// entry.ProposalID is Fleet's own "latest proposal that touched
		// this address" (core/fleet.go's own doc comment) -- exactly
		// right for crossPins below (a cross-stack pin is re-recorded on
		// every resolve that reads the neighbor, so "most recent touch"
		// is the correct source for it), but WRONG for depends_on/
		// blueprintRef: both live only on the address's own CREATE node,
		// which a later proposal touching the SAME address (a
		// drift_adopt reconciling a sibling resource's own $computed ref
		// against this one's real post-apply state, say) never carries
		// at all -- that later proposal's own Delta.Creates is simply
		// empty. Emit's own doc comment above already promises "pulled
		// from the resource's own creating... proposal" for exactly this
		// data; creatingProposalFor walks the address's full recorded
		// history (core.Ledger.ProposalsForAddress, oldest first) to
		// find the one proposal that actually created it, independent
		// of whatever touched it most recently.
		creating, err := creatingProposalFor(l, entry.Address)
		if err != nil {
			return nil, fmt.Errorf("diagram: emit %s: %w", entry.Address, err)
		}

		var dependsOn []string
		var crossPins []core.ResolutionInput
		var blueprintRef string
		if creating != nil {
			dependsOn = dependsOnFor(creating, entry.Address)
			blueprintRef, _ = blueprintSourceFor(creating, entry.Address)
		}
		if p != nil {
			addrStr := entry.Address.String()
			for _, in := range p.Resolution.Inputs {
				if in.Kind == "cross_stack_pin" && in.From == addrStr {
					crossPins = append(crossPins, in)
				}
			}
		}

		resources = append(resources, emitResource{
			addr:         entry.Address,
			attrs:        attrs,
			dependsOn:    dependsOn,
			crossPins:    crossPins,
			blueprintRef: blueprintRef,
		})
	}

	return emitD2(stack, resources)
}

// creatingProposalFor finds addr's own creating proposal -- the one
// change proposal, anywhere in addr's own recorded history, whose own
// Delta.Creates actually contains addr -- independent of which proposal
// most recently TOUCHED addr (Fleet's own ProposalID, an unrelated
// concept documented on core.FleetEntry). Walks
// core.Ledger.ProposalsForAddress (oldest-first, addr's own full genesis
// chain) rather than assuming entry.ProposalID already IS the creating
// proposal, since a later drift_adopt/modify recorded against addr
// itself (a real, ordinary occurrence, not an edge case -- e.g.
// reconciling a sibling resource's own $computed ref against addr's real
// post-apply state) makes that assumption false. nil, nil if addr was
// never created via a change proposal's own Delta.Creates at all (e.g.
// purely adopted) -- not an error, callers already treat "no creating
// proposal" as "no depends_on/blueprintRef to report," never a
// diagram-emit failure.
func creatingProposalFor(l *core.Ledger, addr core.Address) (*core.Proposal, error) {
	chain, err := l.ProposalsForAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("creating proposal for %s: %w", addr, err)
	}
	for _, p := range chain {
		if p.Kind != core.KindChange {
			continue
		}
		for _, raw := range p.Delta.Creates {
			var node struct {
				Stack string `json:"stack"`
				Type  string `json:"type"`
				Name  string `json:"name"`
			}
			if err := json.Unmarshal(raw, &node); err != nil {
				continue
			}
			if node.Stack == addr.Stack && node.Type == addr.Type && node.Name == addr.Name {
				return p, nil
			}
		}
	}
	return nil, nil
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

// blueprintSourceFor mirrors dependsOnFor exactly (same ad hoc decode of
// p.Delta.Creates, same stack/type/name match), extracting addr's own
// per-resource blueprint provenance instead (UBI-74 Slice 6,
// resolver.ResourceIntent.Sources -> the create node's own "sources"
// key) -- the "blueprint" kind entry blueprint.ExpandCalls stamps every
// resource a blueprint call produces with. false whenever addr wasn't
// created by a blueprint call at all (the common case: an ordinary
// hand-authored create, or any Delta.Modifies touch -- per-resource
// Sources is only ever populated at creation time).
func blueprintSourceFor(p *core.Proposal, addr core.Address) (string, bool) {
	if p.Kind != core.KindChange {
		return "", false
	}
	for _, raw := range p.Delta.Creates {
		var node struct {
			Stack   string              `json:"stack"`
			Type    string              `json:"type"`
			Name    string              `json:"name"`
			Sources []core.IntentSource `json:"sources"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		if node.Stack != addr.Stack || node.Type != addr.Type || node.Name != addr.Name {
			continue
		}
		for _, s := range node.Sources {
			if s.Kind == "blueprint" {
				return s.Ref, true
			}
		}
		return "", false
	}
	return "", false
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
	keyOf := make(map[string]string, len(resources)) // addr.String() -> D2 key (bare, no container prefix)
	for i, r := range resources {
		keyOf[r.addr.String()] = fmt.Sprintf("r%d", i)
	}

	// Blueprint containers (UBI-74 Slice 6): resources sharing the same
	// blueprintRef group into one dashed-border D2 container per ref,
	// container keys assigned "bp0", "bp1", ... in ref-sorted order for
	// determinism. A stack with zero blueprint-sourced resources has
	// blueprintRefs empty, so every resource stays top-level exactly as
	// emitD2 rendered it before this slice -- byte-identical output,
	// never a behavior change for a stack that never called a blueprint.
	seenRefs := map[string]bool{}
	var blueprintRefs []string
	for _, r := range resources {
		if r.blueprintRef == "" || seenRefs[r.blueprintRef] {
			continue
		}
		seenRefs[r.blueprintRef] = true
		blueprintRefs = append(blueprintRefs, r.blueprintRef)
	}
	sort.Strings(blueprintRefs)
	containerKeyOf := make(map[string]string, len(blueprintRefs))
	for i, ref := range blueprintRefs {
		containerKeyOf[ref] = fmt.Sprintf("bp%d", i)
	}

	// fullKeyOf is keyOf's own edge-drawing counterpart: a grouped
	// resource's real D2 address is its container's dotted path
	// ("bpK.rN"), the only address D2 edge syntax can actually reach a
	// nested node through -- keyOf's own bare "rN" stays the node's own
	// LOCAL key (used when writing its block, nested or not), fullKeyOf
	// is always what an edge referencing it must use.
	fullKeyOf := make(map[string]string, len(resources))
	for _, r := range resources {
		bare := keyOf[r.addr.String()]
		if r.blueprintRef == "" {
			fullKeyOf[r.addr.String()] = bare
		} else {
			fullKeyOf[r.addr.String()] = containerKeyOf[r.blueprintRef] + "." + bare
		}
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

	// Ungrouped resources render top-level, exactly as this loop always
	// has -- unchanged for any resource with no blueprintRef.
	for i, r := range resources {
		if r.blueprintRef != "" {
			continue
		}
		key := fmt.Sprintf("r%d", i)
		fmt.Fprintf(&b, "%s: %s {\n", key, d2Quote(r.addr.Name))
		fmt.Fprintf(&b, "  class: %s\n", r.addr.Type)
		if tooltip := attrTooltip(r.attrs); tooltip != "" {
			fmt.Fprintf(&b, "  tooltip: %s\n", d2Quote(tooltip))
		}
		b.WriteString("}\n")
	}
	// Blueprint-sourced resources nest inside their own container block
	// (dashed border, transparent fill -- verified against this
	// project's real d2parser/d2format pipeline before use here), one
	// container per distinct blueprintRef, labeled with the ref itself
	// (short-hash form, matching cli/why.go's own 12-char truncation
	// convention -- diagram can't import cli to reuse displayHash
	// directly, cli already imports diagram, so shortBlueprintRef below
	// is a small, deliberately independent mirror of that one rule).
	for _, ref := range blueprintRefs {
		ckey := containerKeyOf[ref]
		fmt.Fprintf(&b, "%s: %s {\n", ckey, d2Quote(shortBlueprintRef(ref)))
		b.WriteString("  style.stroke-dash: 3\n")
		b.WriteString("  style.fill: transparent\n")
		for i, r := range resources {
			if r.blueprintRef != ref {
				continue
			}
			key := fmt.Sprintf("r%d", i)
			fmt.Fprintf(&b, "  %s: %s {\n", key, d2Quote(r.addr.Name))
			fmt.Fprintf(&b, "    class: %s\n", r.addr.Type)
			if tooltip := attrTooltip(r.attrs); tooltip != "" {
				fmt.Fprintf(&b, "    tooltip: %s\n", d2Quote(tooltip))
			}
			b.WriteString("  }\n")
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
		from := fullKeyOf[r.addr.String()]
		for _, dep := range r.dependsOn {
			to, ok := fullKeyOf[dep]
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

// shortBlueprintRef renders a blueprint container's own label: ref's own
// "<name>:sha256:<hex>" shape (blueprint.ExpandCalls' own stamping, see
// blueprint/invoke.go) with the hash portion truncated to the same
// 12-char short-hash convention cli/why.go's own displayHash already
// established -- a deliberately independent mirror of that one rule
// (diagram can't import cli to reuse displayHash directly; cli already
// imports diagram, the reverse would cycle), not a shared helper. ref
// with no ":" at all (shouldn't happen -- every real blueprintRef comes
// from blueprint.ExpandCalls' own stamping -- but Emit never hard-fails a
// diagram over an unexpected label shape) renders unmodified.
func shortBlueprintRef(ref string) string {
	name, hash, ok := strings.Cut(ref, ":")
	if !ok {
		return ref
	}
	hash = strings.TrimPrefix(hash, "sha256:")
	const shortLen = 12
	if len(hash) > shortLen {
		hash = hash[:shortLen] + "…"
	}
	return name + ":sha256:" + hash
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
