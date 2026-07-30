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

		if _, err := resolver.InferProvider(providers, typeClass, nil); err != nil {
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
		intent.Resources = append(intent.Resources, resolver.ResourceIntent{
			Type:   typeClass,
			Name:   name,
			Op:     resolver.OpCreate,
			Config: json.RawMessage("{}"),
		})
	}

	// Second pass: edges -> DependsOn (docs/schema.md's UBI-47
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
)

// sortedLeaves returns every non-container object in g, sorted by its
// own absolute ID path -- determinism is this package's own
// responsibility to supply (d2graph.Graph.Objects is populated in parse
// order, not guaranteed stable across independently-constructed graphs
// with the same real content in a different declaration order).
func sortedLeaves(g *d2graph.Graph) []*d2graph.Object {
	var leaves []*d2graph.Object
	for _, obj := range g.Objects {
		if len(obj.ChildrenArray) == 0 {
			leaves = append(leaves, obj)
		}
	}
	sort.SliceStable(leaves, func(i, j int) bool {
		return absID(leaves[i]) < absID(leaves[j])
	})
	return leaves
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
