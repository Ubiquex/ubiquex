// Package diagram is UBI-47's own topology parser (docs/diagram-medium.md):
// a real .d2 file's own topology (nodes, containers, edges) into an
// intent/v1 draft (resolver.IntentFile) -- the parse half of the
// diagram medium. No LLM anywhere in this path; every interpretive gap
// (an uninferable or ambiguous node type) becomes a visible, non-
// blocking-or-blocking question, reusing core.Intent's own ambiguity
// fields (UBI-41) rather than refusing outright.
//
// Only oss.terrastruct.com/d2's own narrow parser/compiler subpackages
// are imported here (d2compiler, d2graph) -- confirmed empirically
// (UBI-47 session 1) to pull in none of that module's own heavy
// rendering machinery (d2renderers/d2layouts/d2plugin/d2exporter,
// playwright-go, image/PDF libraries).
package diagram

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2graph"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// externalClass is the D2 class name docs/diagram-medium.md's own
// cross-stack grammar recognizes -- a node is a reference declaration,
// never a create, when its label starts with "@" OR it carries this
// class (either alone is accepted on parse; both together, as the
// render direction emits, is the canonical form).
const externalClass = "external"

// ubxRequiredKey is UBI-91's own narrow attribute-annotation escape hatch:
// a D2 node child named exactly this, one level down (D2's own dotted-
// path shorthand for a nested map, e.g. "ubx_required.assume_role_policy:
// value"), carries values for attributes the real provider schema marks
// Required -- and ONLY those. Everything else about the medium stays
// topology-only (docs/diagram-medium.md's founding "lossy-medium rule");
// this closes exactly the gap UBI-90 exposed (a diagram-authored resource
// with a Required attribute no topology signal could ever express) and no
// more -- see ubxRequiredConfig's own doc comment for the scope-discipline
// refusal this reserved name's own sibling values are checked against.
const ubxRequiredKey = "ubx_required"

// ubxBlueprintClass is UBI-74 Slice 5's own diagram calling convention:
// a node classed exactly this is never a resource-create at all -- its
// own attributes (read directly, no ubx_required-style nested reserved
// subtree needed, since EVERY attribute here is meaningful literal
// content, not an escape hatch layered on top of topology-only parsing)
// name a blueprint to call plus its own parameter values. Reuses the
// EXACT structural-attribute-reading mechanism ubx_required already
// established (D2's own dotted-path shorthand compiling to a real child
// object per attribute, Label.Value holding the raw text) -- zero AI
// anywhere in this path, per the original design (docs/blueprint.md's
// own "Cross-medium calling" section).
const ubxBlueprintClass = "ubx_blueprint"

// blueprintRefAttr/blueprintRefKeyAttr/blueprintPathAttr are the three
// reserved attribute names a ubx_blueprint node's own children may use
// -- blueprintRefAttr is required (which blueprint to call, a local
// path or git URL, mirroring blueprint.Pull's own addressing exactly,
// UBI-74 Slice 3); blueprintRefKeyAttr/blueprintPathAttr are optional,
// meaningful only for a git Blueprint reference (mirroring blueprint.
// Pull's own --ref/--path parameters). Every OTHER child on the node is
// a blueprint call parameter, keyed by its own D2 identifier.
const (
	blueprintRefAttr    = "blueprint"
	blueprintRefKeyAttr = "blueprint_ref"
	blueprintPathAttr   = "blueprint_path"
)

// ubxOverrideClass is UBI-86 Part 2's own diagram calling convention,
// mirroring ubxBlueprintClass exactly: a node classed exactly this is
// never a resource-create -- its own attributes (read directly, the
// identical structural-attribute-reading mechanism ubx_blueprint already
// established, zero AI) name a target resource address plus the
// attribute patch to apply against it. Reuses ubxRequiredAttrValue's own
// "ref:<node>.<attr-path>" sigil (UBI-95) for a config value that needs
// to reference a sibling node, never a second value-parsing mechanism.
const ubxOverrideClass = "ubx_override"

// overrideAddressAttr is the one reserved attribute name a ubx_override
// node's own children may use -- required (the target resource's own
// canonical "<stack>.<type>.<name>" address). Every OTHER child on the
// node is one config field to override, keyed by its own D2 identifier
// -- the real WIRE attribute name, matching how sdk.Override's own
// config map keys already work (never this document's own Config-struct
// field-name translation, since there is no ResourceBinding for the
// override's own target in scope here).
const overrideAddressAttr = "address"

// Options configures Parse. NeighborLedgers maps a referenced stack name
// to an explicit ledger_dir override (docs/diagram-medium.md's own
// "--neighbor-ledger <stack>=<path>", repeatable on the CLI, slice 3 --
// this package only consumes the already-parsed map). A stack not named
// here falls back to the "../<stack>" sibling-directory convention
// already established by every cross-stack worked example elsewhere in
// this project's own docs (docs/resolver.md's own $cross examples).
// BaseDir anchors both the convention and any relative override path --
// normally the directory containing the .d2 file itself.
type Options struct {
	NeighborLedgers map[string]string
	BaseDir         string
}

// Parse compiles filename's own D2 topology (read from r) into an
// intent/v1 draft for stack. providers is the stack's own declared
// provider set (docs/architecture.md's Multi-provider stacks) -- every
// typed node's own class: value is resolved against it via
// resolver.InferProvider (UBI-43), completely unchanged.
//
// A D2 compile error (row 6, docs/diagram-medium.md's adversarial
// program) is returned verbatim, never swallowed or partially recovered
// from. Everything past that point never hard-fails the whole draft for
// one bad node: an uninferable/ambiguous type becomes a blocking
// question (row 2/2b) and the node is simply excluded from Resources;
// an external node whose own stack can't be resolved to a real
// ledger_dir (row 7) is the one exception that DOES hard-fail parsing
// entirely, since a diagram that can't even locate what it's pointing
// at has nothing reviewable to fall back to for that reference.
func Parse(filename string, r io.Reader, stack string, providers []resolver.DeclaredProvider, opts Options) (*resolver.IntentFile, error) {
	g, _, err := d2compiler.Compile(filename, r, nil)
	if err != nil {
		return nil, fmt.Errorf("diagram: parse %s: %w", filename, err)
	}

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         stack,
	}

	objs := sortedLeaves(g)

	// First pass: classify every leaf object -- a resource, a reference
	// (external) node, or an unresolvable node -- before translating any
	// edge, since an edge's own translation needs to know what both its
	// endpoints resolved to.
	kind := make(map[string]nodeKind, len(objs))
	resourceName := make(map[string]string, len(objs)) // AbsID -> resolved ubx resource name
	refAddress := make(map[string]string, len(objs))   // AbsID -> the "@stack.type.name" address a reference node names
	resourceIndex := make(map[string]int, len(objs))   // AbsID -> index into intent.Resources, for depends_on backfill

	// pending holds every resource node's own d2graph.Object + resolved
	// type + provider, keyed by absID -- ubx_required config-building
	// (below) is deferred out of this pass entirely (UBI-95): a $ref
	// value inside one node's own ubx_required block may name ANOTHER
	// node not yet reached here, since objs is sorted by absID, not by
	// declaration or topological order. pendingOrder preserves objs' own
	// deterministic order for the second pass below -- ranging over the
	// pending map directly would make config-building order (and this
	// package's own error-ordering, on a multi-node failure) depend on
	// Go's deliberately-randomized map iteration, which this project's
	// own determinism discipline never allows anywhere near resolved
	// output.
	pending := make(map[string]pendingResource, len(objs))
	var pendingOrder []string

	// pendingBlueprintCalls (UBI-128) is a ubx_blueprint node's own
	// mirror of pending above -- populated in this same first pass so
	// resolveRefTarget (below) can resolve a ref: sigil naming a
	// blueprint call node's own bare D2 identifier, not just an ordinary
	// resource node's. A blueprint call has no known resource address
	// until it's actually expanded (blueprint.ExpandCalls, after this
	// package returns) -- resolveRefTarget's own blueprintCallRefTargetType
	// sentinel, below, lets ubxRequiredAttrValue emit a PROVISIONAL
	// resolver.BlueprintOutputRefPrefix marker instead of an ordinary
	// "<stack>.<type>.<name>.<attr>" address in this case.
	pendingBlueprintCalls := make(map[string]pendingBlueprintCall)

	// pendingOverrideObjs holds every ubx_override-classed node's own
	// d2graph.Object, in objs' own deterministic order -- UBI-86 Part 2's
	// own mirror of pendingOrder above: an override's own config value
	// may carry a "ref:<node>.<attr-path>" sigil (the same escape hatch
	// ubx_required already established, UBI-95) naming another node not
	// yet reached here, so building the actual resolver.Override waits
	// for the identical second pass that resolves ubx_required's own
	// $ref values, below.
	var pendingOverrideObjs []*d2graph.Object

	var questions []core.Question
	var defaults []core.AmbiguityNote

	for _, obj := range objs {
		absID := absID(obj)
		label := nodeLabel(obj)

		if strings.HasPrefix(label, "@") || hasClass(obj.Classes, externalClass) {
			addr, err := parseCrossRefLabel(label)
			if err != nil {
				return nil, fmt.Errorf("diagram: %s: %w", absID, err)
			}
			refAddr, ok := core.ParseAddress(addr)
			if !ok {
				return nil, fmt.Errorf("diagram: %s: %q is not a valid <stack>.<type>.<name> address", absID, addr)
			}
			ledgerDir, err := resolveNeighborLedgerDir(refAddr.Stack, opts)
			if err != nil {
				return nil, fmt.Errorf("diagram: %s: %w", absID, err)
			}
			kind[absID] = nodeKindReference
			refAddress[absID] = addr
			_ = ledgerDir // recorded for the informational note built below, not a wire-level $cross (see "Cross-stack edges" note)
			continue
		}

		if hasClass(obj.Classes, ubxBlueprintClass) {
			call, err := blueprintCallFromNode(obj, label, opts)
			if err != nil {
				return nil, fmt.Errorf("diagram: %s: %w", absID, err)
			}
			// UBI-128: CallName is this node's own bare D2 identifier,
			// implicit -- exactly the same identifier a ref: sigil already
			// uses to name an ordinary sibling resource (resolveRefTarget,
			// below), never a second naming scheme. Always set (D2 always
			// gives every object a real ID), so a ref: naming this node
			// always has a CallName to resolve against, whether or not
			// this call's own outputs actually end up referenced.
			call.CallName = obj.ID
			kind[absID] = nodeKindBlueprintCall
			intent.BlueprintCalls = append(intent.BlueprintCalls, call)
			pendingBlueprintCalls[absID] = pendingBlueprintCall{obj: obj, callName: call.CallName}
			continue
		}

		if hasClass(obj.Classes, ubxOverrideClass) {
			// UBI-86 Part 2: zero AI, a structural attribute read exactly
			// like ubx_blueprint above -- deferred to the second pass
			// (below, alongside ubx_required) since a config value here
			// may name a sibling node via the same "ref:<node>.<attr-path>"
			// sigil ubx_required already supports.
			kind[absID] = nodeKindOverride
			pendingOverrideObjs = append(pendingOverrideObjs, obj)
			continue
		}

		typeClass, ok := firstNonExternalClass(obj.Classes)
		if !ok {
			questions = append(questions, core.Question{
				Text:     fmt.Sprintf("node %q has no class: attribute -- ubx can't infer its resource type from a label alone. Add a class: naming the real provider type (e.g. \"class: aws_db_instance\").", absID),
				Affects:  []string{absID},
				Blocking: true,
			})
			kind[absID] = nodeKindUnresolved
			continue
		}

		dp, err := resolver.InferProvider(providers, typeClass, nil)
		if err != nil {
			questions = append(questions, core.Question{
				Text:     fmt.Sprintf("node %q (class %q): %v", absID, typeClass, err),
				Affects:  []string{absID},
				Blocking: true,
			})
			kind[absID] = nodeKindUnresolved
			continue
		}

		name := label
		if name == "" {
			name = obj.ID
		}
		kind[absID] = nodeKindResource
		resourceName[absID] = name
		resourceIndex[absID] = len(intent.Resources)
		pending[absID] = pendingResource{obj: obj, typeClass: typeClass, dp: dp}
		pendingOrder = append(pendingOrder, absID)
		intent.Resources = append(intent.Resources, resolver.ResourceIntent{
			Type: typeClass,
			Name: name,
			Op:   resolver.OpCreate,
			// Config is filled in below, once every resource in this
			// diagram (not just the ones already seen) has a known
			// type+name a ubx_required $ref value could target.
		})
	}

	// byBareID lets a ubx_required $ref name a top-level node by its
	// own bare D2 identifier (the common case -- e.g. "role", matching
	// how every other node reference already reads in a diagram this
	// size) rather than requiring its full absID, while still accepting
	// the full absID directly for a node nested inside a container.
	byBareID := make(map[string]string, len(pending)+len(pendingBlueprintCalls))
	for absID, pr := range pending {
		if pr.obj.ID != absID {
			byBareID[pr.obj.ID] = absID
		}
	}
	for absID, pb := range pendingBlueprintCalls {
		if pb.obj.ID != absID {
			byBareID[pb.obj.ID] = absID
		}
	}
	resolveRefTarget := func(identifier string) (targetType, targetName string, ok bool) {
		targetAbsID := identifier
		_, directResource := pending[identifier]
		_, directBlueprintCall := pendingBlueprintCalls[identifier]
		if !directResource && !directBlueprintCall {
			if viaBareID, found := byBareID[identifier]; found {
				targetAbsID = viaBareID
			}
		}
		// UBI-128: a ref: naming a blueprint call node resolves to its
		// own CallName, tagged with blueprintCallRefTargetType so
		// ubxRequiredAttrValue (below) knows attrPath names a DECLARED
		// OUTPUT, never a real provider attribute -- checked first since
		// a blueprint call node is never also in resourceIndex.
		if pb, found := pendingBlueprintCalls[targetAbsID]; found {
			return blueprintCallRefTargetType, pb.callName, true
		}
		idx, found := resourceIndex[targetAbsID]
		if !found {
			return "", "", false
		}
		return intent.Resources[idx].Type, intent.Resources[idx].Name, true
	}

	// Second pass (of what UBI-91 originally treated as one): build
	// every resource's own ubx_required config now, in objs' own
	// deterministic order -- the only point where a ubx_required $ref
	// value can be resolved against a sibling node, since every
	// resource's own type+name is finally known regardless of where in
	// the diagram it was declared.
	for _, absID := range pendingOrder {
		pr := pending[absID]
		config, err := ubxRequiredConfig(pr.obj, pr.typeClass, pr.dp, stack, resolveRefTarget)
		if err != nil {
			return nil, fmt.Errorf("diagram: %s: %w", absID, err)
		}
		intent.Resources[resourceIndex[absID]].Config = config
	}

	// UBI-86 Part 2: every ubx_override node's own resolver.Override,
	// built here (not the first pass) for the identical reason
	// ubx_required's own config is -- a "ref:<node>.<attr-path>" value
	// may name a sibling node whose type/name is only fully known once
	// every node in the diagram has been classified.
	for _, obj := range pendingOverrideObjs {
		ov, err := overrideFromNode(obj, stack, resolveRefTarget)
		if err != nil {
			return nil, fmt.Errorf("diagram: %s: %w", absID(obj), err)
		}
		intent.Overrides = append(intent.Overrides, ov)
	}

	// Third pass: edges -> DependsOn (docs/schema.md's UBI-47
	// amendment) for a resource-to-resource edge, or a visible,
	// non-blocking note for a resource-to-reference edge (see
	// "Cross-stack edges," below).
	for _, e := range g.Edges {
		srcID := strings.Join(e.Src.AbsIDArray(), ".")
		dstID := strings.Join(e.Dst.AbsIDArray(), ".")
		srcKind, dstKind := kind[srcID], kind[dstID]

		if srcKind != nodeKindResource {
			// An edge from a container, a reference node, or an
			// unresolved node carries no ubx-legible meaning as a
			// dependency source -- silently a no-op, matching this
			// design's own "containers are pure grouping" posture: an
			// edge FROM something that isn't itself a create has nothing
			// to attach a dependency to.
			continue
		}
		srcAddr := fmt.Sprintf("%s.%s.%s", stack, intent.Resources[resourceIndex[srcID]].Type, resourceName[srcID])

		switch dstKind {
		case nodeKindResource:
			dstAddr := fmt.Sprintf("%s.%s.%s", stack, intent.Resources[resourceIndex[dstID]].Type, resourceName[dstID])
			ri := &intent.Resources[resourceIndex[srcID]]
			if !containsString(ri.DependsOn, dstAddr) {
				ri.DependsOn = append(ri.DependsOn, dstAddr)
			}
		case nodeKindReference:
			// A real, named v1 limitation (docs/diagram-medium.md's own
			// "Cross-stack edges" section), not an oversight: a $cross
			// marker has to live inside a specific config attribute path,
			// and a topology-only edge names no attribute at all -- the
			// same gap DependsOn exists to close for intra-stack edges,
			// but $cross's own wire shape can't be reduced to a bare
			// address the way an intra-stack dependency can (there's no
			// "wait for creation" ordering concern to express instead).
			// Recorded as visible, non-blocking content rather than
			// silently dropped.
			defaults = append(defaults, core.AmbiguityNote{
				Text:    fmt.Sprintf("resource %q is drawn with a relationship to external reference %q -- the diagram medium does not yet emit a wire-level $cross reference for a topology-only edge with no named attribute. Add an explicit $cross reference in the resource's own config by hand if this needs to be enforced structurally.", srcAddr, refAddress[dstID]),
				Affects: []string{srcAddr},
			})
		}
	}

	for i := range intent.Resources {
		sort.Strings(intent.Resources[i].DependsOn)
	}

	intent.Intent = core.Intent{
		Assumptions: nil,
		Defaults:    defaults,
		Questions:   questions,
	}

	return intent, nil
}

type nodeKind int

const (
	nodeKindResource nodeKind = iota
	nodeKindReference
	nodeKindUnresolved
	// nodeKindBlueprintCall (UBI-74 Slice 5) is inert everywhere the
	// existing edge-translation logic (Parse's own "Third pass," below)
	// already treats "anything that isn't nodeKindResource/
	// nodeKindReference" as having no ubx-legible meaning as an edge
	// endpoint -- no changes needed there at all: a blueprint call has
	// no known resource address until it's EXPANDED (blueprint.
	// ExpandCalls, after this package returns), so wiring a topology
	// edge to/from one isn't supported this slice, silently a no-op,
	// same as an edge touching an unresolved node already is.
	nodeKindBlueprintCall
	// nodeKindOverride (UBI-86 Part 2) is inert everywhere the existing
	// edge-translation logic already treats "anything that isn't
	// nodeKindResource/nodeKindReference" as having no ubx-legible
	// meaning as an edge endpoint -- the identical reasoning
	// nodeKindBlueprintCall's own doc comment already gives, no changes
	// needed there either: an override targets an address by NAME, not
	// by diagram topology, so wiring a topology edge to/from an override
	// node isn't a thing this design needs at all.
	nodeKindOverride
)

// pendingResource carries what Parse's first pass already knows about a
// resource node -- its own d2graph.Object, resolved type, and resolved
// provider -- through to the second pass, where its ubx_required config
// (including any $ref value naming a sibling node, UBI-95) actually gets
// built. See Parse's own "pending" doc comment for why config-building
// is deferred out of the first pass entirely.
type pendingResource struct {
	obj       *d2graph.Object
	typeClass string
	dp        resolver.DeclaredProvider
}

// pendingBlueprintCall (UBI-128) is a ubx_blueprint node's own mirror of
// pendingResource -- carried from Parse's first pass to resolveRefTarget,
// where a ref: sigil naming this node's own bare D2 identifier resolves
// to its CallName rather than a resource type+name.
type pendingBlueprintCall struct {
	obj      *d2graph.Object
	callName string
}

// blueprintCallRefTargetType is resolveRefTarget's own sentinel
// targetType value (UBI-128): never a real provider type name (every
// real one is a bare lowercase_with_underscores identifier; this is
// deliberately not), so ubxRequiredAttrValue can tell a resolved
// identifier apart from an ordinary resource ref without a second return
// value threaded through every existing caller. targetName in this case
// holds the blueprint call's own CallName, not a resource name.
const blueprintCallRefTargetType = "\x00blueprint_call"

// sortedLeaves returns every non-container object in g, sorted by its
// own absolute ID path -- determinism is this package's own
// responsibility to supply (d2graph.Graph.Objects is populated in parse
// order, not guaranteed stable across independently-constructed graphs
// with the same real content in a different declaration order).
//
// UBI-91: a resource node carrying a "ubx_required.<attr>: value" block
// (D2's own dotted-path shorthand for a nested map) compiles to a REAL
// child object named "ubx_required" in d2graph's own object tree -- which
// would otherwise make the resource node itself look like a container
// (non-empty ChildrenArray) and get silently excluded here, exactly the
// failure mode confirmed empirically before writing this fix (a resource
// with a ubx_required block stopped being classified as a resource at
// all). A node whose ONLY child is that one reserved name is still
// treated as a leaf; the reserved subtree itself (the "ubx_required"
// object and everything under it -- attribute names/values, never real
// topology) is excluded entirely, never independently classified as its
// own resource/container/reference node.
func sortedLeaves(g *d2graph.Graph) []*d2graph.Object {
	var leaves []*d2graph.Object
	for _, obj := range g.Objects {
		if inUbxRequiredSubtree(obj) {
			continue
		}
		// A DESCENDANT of a ubx_blueprint/ubx_override-classed node (its
		// own "blueprint:"/"widget_name:"/etc. attribute children) is
		// excluded here too -- a real, pre-existing gap UBI-128's own
		// live diagram verification caught: the hasClass checks just
		// below only ever matched the classed node ITSELF (a real,
		// separate *d2graph.Object in g.Objects, distinct from every one
		// of its own children), so a child like "platform.blueprint" fell
		// through to the ordinary "len(ChildrenArray) == 0" leaf check
		// below, got independently classified as an ordinary resource
		// node, and refused for having no class: attribute -- a spurious
		// blocking Question on every diagram ever using either node kind,
		// silently undetected until now because no prior test asserted
		// on intent.Intent.Questions for one of these nodes, only that
		// intent.Resources stayed empty (which it always did anyway,
		// coincidentally, since nodeKindUnresolved never appends to
		// Resources either).
		if inBlueprintOrOverrideSubtree(obj) {
			continue
		}
		// A ubx_blueprint node's own children are ALWAYS just its own
		// literal attribute values (blueprintCallFromNode reads them
		// directly, below) -- never real topology, so this node is
		// always a leaf regardless of how many children it has, the
		// same reasoning onlyUbxRequiredChild already applies to an
		// ordinary resource node's own reserved ubx_required subtree.
		if hasClass(obj.Classes, ubxBlueprintClass) {
			leaves = append(leaves, obj)
			continue
		}
		// A ubx_override node's own children are likewise always just its
		// own literal attribute values (overrideFromNode reads them
		// directly, below) -- never real topology, same reasoning as
		// ubx_blueprint immediately above.
		if hasClass(obj.Classes, ubxOverrideClass) {
			leaves = append(leaves, obj)
			continue
		}
		if len(obj.ChildrenArray) == 0 || onlyUbxRequiredChild(obj) {
			leaves = append(leaves, obj)
		}
	}
	sort.SliceStable(leaves, func(i, j int) bool {
		return absID(leaves[i]) < absID(leaves[j])
	})
	return leaves
}

// inUbxRequiredSubtree reports whether obj is the reserved "ubx_required"
// container itself, or anything nested under it -- attribute names and
// values, never real topology, so never independently classified.
func inUbxRequiredSubtree(obj *d2graph.Object) bool {
	for _, seg := range obj.AbsIDArray() {
		if seg == ubxRequiredKey {
			return true
		}
	}
	return false
}

// inBlueprintOrOverrideSubtree reports whether obj is a DESCENDANT of a
// ubx_blueprint- or ubx_override-classed node -- checked from obj.Parent
// upward, never obj itself, so the classed node's own leaf-hood (matched
// separately, by its own hasClass check in sortedLeaves) is untouched;
// only its children (and their own children, if any) are excluded from
// independent classification.
func inBlueprintOrOverrideSubtree(obj *d2graph.Object) bool {
	for p := obj.Parent; p != nil; p = p.Parent {
		if hasClass(p.Classes, ubxBlueprintClass) || hasClass(p.Classes, ubxOverrideClass) {
			return true
		}
	}
	return false
}

// onlyUbxRequiredChild reports whether obj's own children are exactly one
// object, the reserved "ubx_required" container -- the shape a resource
// node with a ubx_required block, and nothing else nested under it,
// compiles to. A node with ubx_required PLUS a genuine additional child
// still looks like (and is treated as) an ordinary container, unchanged
// from before this existed -- mixing the escape hatch with real nested
// topology isn't a supported pattern this fix adds new handling for.
func onlyUbxRequiredChild(obj *d2graph.Object) bool {
	return len(obj.ChildrenArray) == 1 && obj.ChildrenArray[0].ID == ubxRequiredKey
}

func absID(obj *d2graph.Object) string {
	return strings.Join(obj.AbsIDArray(), ".")
}

// nodeLabel returns obj's own D2 label, or its bare identifier when no
// distinct label was given (D2's own default) -- docs/diagram-medium.md's
// own "Node naming" section: ubx resource names come from the label,
// never the D2 key.
func nodeLabel(obj *d2graph.Object) string {
	if obj.Label.Value != "" {
		return obj.Label.Value
	}
	return obj.ID
}

func hasClass(classes []string, name string) bool {
	for _, c := range classes {
		if c == name {
			return true
		}
	}
	return false
}

// blueprintCallFromNode reads a ubx_blueprint-classed node's own
// children directly (D2's own dotted-path shorthand -- "blueprint:
// value" inside the node's braces compiles to a real child object named
// "blueprint" whose own Label.Value holds the raw text, exactly the
// shape ubxRequiredConfig already reads one level deeper) into a
// resolver.BlueprintCall: blueprintRefAttr (required) plus
// blueprintRefKeyAttr/blueprintPathAttr (both optional, git-only) are
// the three reserved names; every OTHER child is a blueprint call
// parameter, verbatim raw text -- type coercion against the target
// blueprint's own declared params happens entirely at expansion time
// (blueprint.ExpandCalls), never here (this package has no schema/
// Ubxfile access at all, by design -- docs/diagram-medium.md's own
// "lossy-medium," zero-I/O parsing discipline).
//
// A relative (non-URL, non-absolute) blueprint: value is resolved
// against opts.BaseDir here, once -- the same convention this package's
// own neighbor-ledger resolution already establishes for "relative to
// what," so a BlueprintCall's own Blueprint field is always either an
// already-absolute local path or a genuine URL by the time this
// function returns, never ambiguous about its own base directory later.
func blueprintCallFromNode(obj *d2graph.Object, label string, opts Options) (resolver.BlueprintCall, error) {
	call := resolver.BlueprintCall{Name: label, Args: map[string]string{}}
	var haveBlueprint bool
	for _, child := range obj.ChildrenArray {
		switch child.ID {
		case blueprintRefAttr:
			call.Blueprint = child.Label.Value
			haveBlueprint = true
		case blueprintRefKeyAttr:
			call.Ref = child.Label.Value
		case blueprintPathAttr:
			call.Path = child.Label.Value
		default:
			call.Args[child.ID] = child.Label.Value
		}
	}
	if !haveBlueprint || strings.TrimSpace(call.Blueprint) == "" {
		return resolver.BlueprintCall{}, fmt.Errorf("ubx_blueprint node %q is missing a %q attribute naming the blueprint to call", label, blueprintRefAttr)
	}
	call.Blueprint = resolveBlueprintRefBaseDir(call.Blueprint, opts.BaseDir)
	return call, nil
}

// overrideFromNode reads a ubx_override-classed node's own children
// directly (the identical D2 dotted-path-shorthand mechanism
// blueprintCallFromNode already reads) into a resolver.Override:
// overrideAddressAttr (required) is the target resource's own address,
// read as a plain string (never a "ref:" sigil -- an address names a
// resource by its own resolved identity, not a value to resolve). Every
// OTHER child is one config field to override, its own raw D2 text
// resolved via ubxRequiredAttrValue -- the SAME value-parsing
// ubx_required already uses (an ordinary JSON string literal, or a
// "ref:<node>.<attr-path>" sigil resolved into a real wire $ref object
// via resolveTarget), never a second, parallel value-parsing mechanism.
func overrideFromNode(obj *d2graph.Object, stack string, resolveTarget func(identifier string) (targetType, targetName string, ok bool)) (resolver.Override, error) {
	var address string
	var haveAddress bool
	config := map[string]json.RawMessage{}
	for _, child := range obj.ChildrenArray {
		if child.ID == overrideAddressAttr {
			address = child.Label.Value
			haveAddress = true
			continue
		}
		raw, err := ubxRequiredAttrValue(child.Label.Value, child.ID, stack, resolveTarget)
		if err != nil {
			return resolver.Override{}, err
		}
		config[child.ID] = raw
	}
	if !haveAddress || strings.TrimSpace(address) == "" {
		return resolver.Override{}, fmt.Errorf("ubx_override node is missing a %q attribute naming the resource to override", overrideAddressAttr)
	}
	if _, ok := core.ParseAddress(address); !ok {
		return resolver.Override{}, fmt.Errorf("ubx_override node: %q is not a valid <stack>.<type>.<name> address", address)
	}
	if len(config) == 0 {
		return resolver.Override{}, fmt.Errorf("ubx_override node %q has no config attributes to override -- add at least one besides %q", address, overrideAddressAttr)
	}
	return resolver.Override{Address: address, Config: config}, nil
}

// resolveBlueprintRefBaseDir joins a relative local blueprint reference
// against baseDir -- a git URL (contains "://") or an already-absolute
// path passes through unchanged.
func resolveBlueprintRefBaseDir(ref, baseDir string) string {
	if strings.Contains(ref, "://") || filepath.IsAbs(ref) || baseDir == "" {
		return ref
	}
	return filepath.Join(baseDir, ref)
}

// firstNonExternalClass returns the one class name docs/diagram-medium.md's
// own type-annotation convention cares about -- a node legally carries
// at most one type-meaningful class in v1 (multiple classes on one node
// is valid D2, but ubx's own convention doesn't define what more than
// one type class would mean, so only the first non-"external" class is
// consulted; a node with none at all is the "no inferable type" case).
func firstNonExternalClass(classes []string) (string, bool) {
	for _, c := range classes {
		if c != externalClass {
			return c, true
		}
	}
	return "", false
}

// ubxRequiredConfig builds obj's own resolved Config -- "{}" (the
// unmodified topology-only default) unless obj carries a "ubx_required"
// child, in which case every one of ITS OWN children is one supplied
// attribute value (d2graph's own compiled shape for "ubx_required.<attr>:
// value", confirmed empirically: a real child object named <attr>, whose
// own Label.Value holds the raw text -- a bare scalar like a name/ARN, or
// the full text of a block-string JSON policy document alike, since every
// attribute in this escape hatch's own real-world scope (assume_role_
// policy, policy, name, role, policy_arn) is a plain STRING-typed
// provider attribute, even the two whose own content happens to BE JSON
// text -- so every value here is encoded as a JSON string, never
// re-parsed into a nested object).
//
// Scope discipline, strict, checked here (not left to resolve-time,
// UBI-90's own MissingRequiredKeys check -- that check only ever
// confirms a Required attribute HAS a value, it has no opinion on
// whether a NON-required one showing up here should have been allowed to
// in the first place): dp.Schema.MissingRequiredKeys(typeClass, an EMPTY
// config) reports every attribute the real schema marks Required for
// typeClass -- exactly the escape hatch's own legal target set. Any
// ubx_required.<attr> naming something outside that set (Optional,
// Computed, or not a real attribute at all) is refused immediately, hard
// -- this is deliberately NOT a general attribute-authoring mechanism;
// topology-only stays the rule for everything else UBI-47 already
// established.
func ubxRequiredConfig(obj *d2graph.Object, typeClass string, dp resolver.DeclaredProvider, stack string, resolveTarget func(identifier string) (targetType, targetName string, ok bool)) (json.RawMessage, error) {
	var required *d2graph.Object
	for _, child := range obj.ChildrenArray {
		if child.ID == ubxRequiredKey {
			required = child
			break
		}
	}
	if required == nil {
		return json.RawMessage("{}"), nil
	}

	requiredNames := map[string]bool{}
	for _, issue := range dp.Schema.MissingRequiredKeys(typeClass, map[string]interface{}{}) {
		requiredNames[issue.Path] = true
	}

	config := make(map[string]json.RawMessage, len(required.ChildrenArray))
	for _, attr := range required.ChildrenArray {
		if !requiredNames[attr.ID] {
			return nil, fmt.Errorf("ubx_required.%s: %q is not a required attribute on %s -- use --from-doc or an SDK program to set optional attributes", attr.ID, attr.ID, typeClass)
		}
		raw, err := ubxRequiredAttrValue(attr.Label.Value, attr.ID, stack, resolveTarget)
		if err != nil {
			return nil, err
		}
		config[attr.ID] = raw
	}
	b, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal ubx_required config: %w", err)
	}
	return b, nil
}

// ubxRequiredRefPrefix is UBI-95's own extension to the ubx_required
// escape hatch (UBI-91): a value of the shape "ref:<node-identifier>.
// <attr-path>" names another node IN THIS SAME DIAGRAM by its own D2
// identifier (or full absolute dotted path, for a node nested inside a
// container) plus a real attribute on that node's own resolved provider
// schema -- resolved into a genuine wire {"$ref": {"to": "<stack>.
// <type>.<name>.<attr-path>"}} object, the exact shape core/resolver's
// own resolveRef already consumes for every other medium (core/resolver/
// refs.go), never a new resolution mechanism of this package's own.
//
// Deliberately spelled without a leading "$" -- confirmed live (twice,
// identically) that D2's own compiler reserves "$" for its own variable-
// substitution grammar UNCONDITIONALLY, even inside an otherwise-plain
// quoted string ("substitutions must begin on {"): a "$ref:..." value,
// quoted or not, never reaches this package's own parsing at all --
// d2compiler.Compile itself refuses it first. "ref:" (no sigil) is the
// only shape confirmed to survive D2's own lexer intact as a plain
// string.
//
// This exists because a Required attribute is sometimes only knowable
// AFTER a sibling resource is actually created -- a provider-auto-
// generated IAM role name, confirmed live (UBI-92/93's own founder
// repro): a literal value in that case is either impossible to know in
// advance, or worse, silently WRONG on every subsequent re-plan once the
// referenced resource is re-created with a brand new generated identity,
// since a diagram's own re-authoring is otherwise idempotent but a
// hardcoded guess at another resource's generated name never is.
//
// Deliberately narrow, matching ubx_required's own scope discipline
// exactly: still only legal for an attribute the real schema marks
// Required (the same requiredNames check above applies whether the value
// is a literal or a ref) -- this only changes WHERE the value comes
// from, never WHICH attributes may be set this way. Still never a
// general attribute-authoring mechanism; topology-only stays the rule
// for everything else UBI-47 already established.
const ubxRequiredRefPrefix = "ref:"

// ubxRequiredAttrValue resolves one ubx_required attribute's own raw D2
// label text into the json.RawMessage that belongs in the resource's
// Config -- either an ordinary JSON-encoded string literal (today's
// unchanged behavior), or, when raw carries ubxRequiredRefPrefix, a real
// wire $ref object built from resolveTarget's own lookup of the named
// sibling node.
func ubxRequiredAttrValue(raw, attrName, stack string, resolveTarget func(identifier string) (targetType, targetName string, ok bool)) (json.RawMessage, error) {
	if !strings.HasPrefix(raw, ubxRequiredRefPrefix) {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("ubx_required.%s: %w", attrName, err)
		}
		return b, nil
	}

	rest := strings.TrimPrefix(raw, ubxRequiredRefPrefix)
	dotIdx := strings.Index(rest, ".")
	if dotIdx <= 0 || dotIdx == len(rest)-1 {
		return nil, fmt.Errorf("ubx_required.%s: %q is not a well-formed \"ref:<node>.<attr-path>\" reference", attrName, raw)
	}
	identifier := rest[:dotIdx]
	attrPath := rest[dotIdx+1:]

	targetType, targetName, found := resolveTarget(identifier)
	if !found {
		return nil, fmt.Errorf("ubx_required.%s: %q references node %q, which is not a resource or ubx_blueprint node in this same diagram (a reference must name another topology node's own D2 identifier)", attrName, raw, identifier)
	}

	// UBI-128: a ref: naming a ubx_blueprint node means attrPath is a
	// DECLARED OUTPUT name (blueprint/ubxfile.go's outputs: key), never a
	// real provider attribute -- a PROVISIONAL resolver.
	// BlueprintOutputRefPrefix marker, resolved into a real address later
	// by blueprint.ExpandCalls (blueprint/outputs.go), once this call has
	// actually been invoked and its own outputs are known. Deliberately
	// the SAME ref: sigil and $ref wire shape an ordinary resource
	// reference already uses -- only the "to" value's own shape differs,
	// never a second reference mechanism. This is also deliberately NOT
	// @stack.type.name (UBI-47): an output stays within this same
	// document/proposal, no separate ledger, no staleness-by-neighbor-
	// advance concept at all.
	var to string
	if targetType == blueprintCallRefTargetType {
		to = resolver.BlueprintOutputRefPrefix + targetName + ":" + attrPath
	} else {
		to = stack + "." + targetType + "." + targetName + "." + attrPath
	}
	obj := map[string]interface{}{
		"$ref": map[string]interface{}{"to": to},
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("ubx_required.%s: marshal $ref: %w", attrName, err)
	}
	return b, nil
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// resolveNeighborLedgerDir resolves stack's own ledger_dir: an explicit
// opts.NeighborLedgers override, or the "../<stack>" sibling-directory
// convention, anchored at opts.BaseDir. Confirms the resolved directory
// actually exists on disk (row 7's own required outcome: "which
// directory was checked and wasn't there") -- a real, named v1 scope
// boundary, not the deeper "is this address specifically recorded
// there" check $cross's own resolve-time cross_stack_pin mechanism
// performs (out of reach here in v1, since a topology-only edge to a
// reference node doesn't produce a real $cross marker at all yet -- see
// Parse's own "Cross-stack edges" note).
func resolveNeighborLedgerDir(stack string, opts Options) (string, error) {
	dir, explicit := opts.NeighborLedgers[stack]
	if !explicit {
		dir = filepath.Join(opts.BaseDir, "..", stack)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		source := "the \"../<stack>\" convention"
		if explicit {
			source = "--neighbor-ledger"
		}
		return "", fmt.Errorf("stack %q: no ledger directory found at %s (%s) -- pass --neighbor-ledger %s=<path> if it lives somewhere else", stack, dir, source, stack)
	}
	return dir, nil
}
