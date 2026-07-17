// Package resolver implements docs/resolver.md's contract: a hand-written,
// machine-shaped ubx:intent/v1 file, live ledger state, and a provider
// schema resolve into a typed, hashed kind:"change" proposal
// (docs/schema.md's own amendment) -- creates and modifies, no destroys
// (v1 scope). This package stays provider-import-free the same way
// core/executor does: SchemaInspector stands in for a concrete
// *provider.Schema, with the real adapter living in cli, not here.
package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// IntentFileKind is the only legal IntentFile.Kind value for v1.
const IntentFileKind = "ubx:intent/v1"

// SchemaInspector is core/resolver's own minimal view of "something that
// knows a provider's schema" -- exactly the three questions resolving a
// change proposal ever needs answered, never the concrete *provider.Schema
// itself (docs/resolver.md — "Schema boundary"). A concrete adapter lives
// in cli, alongside stateReaderAdapter; hermetic tests here use a small
// fake.
type SchemaInspector interface {
	HasType(typeName string) bool
	IsComputed(typeName, attrPath string) bool
	IsSensitive(typeName, attrPath string) bool
}

// IntentFile is the ubx:intent/v1 wire format (docs/schema.md's
// "Amendment: intent files and resolved change proposals", UBI-27) --
// deliberately machine-shaped, not for hand-typing in production.
type IntentFile struct {
	SchemaVersion int64            `json:"schema_version"`
	Kind          string           `json:"kind"`
	Stack         string           `json:"stack"`
	Intent        core.Intent      `json:"intent"`
	Resources     []ResourceIntent `json:"resources"`
}

// ResourceIntent is one entry of IntentFile.Resources. Op is always
// explicit ("create" | "modify"), never inferred from ledger presence --
// see docs/resolver.md's own "op: explicit, not inferred" section for why.
// Config is the resource's full desired end-state (never a hand-computed
// before/after diff), whose values may be plain JSON or one of $ref/
// $cross/$secret/$ephemeral (docs/schema.md's amendment).
type ResourceIntent struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Op     string          `json:"op"`
	Config json.RawMessage `json:"config"`
}

const (
	OpCreate = "create"
	OpModify = "modify"
)

var (
	// ErrUnknownIntentKind means IntentFile.Kind isn't "ubx:intent/v1".
	ErrUnknownIntentKind = errors.New("resolve: unrecognized intent file kind")

	// ErrInvalidOp means a resource intent's Op isn't "create" or "modify".
	ErrInvalidOp = errors.New("resolve: resource op must be \"create\" or \"modify\"")

	// ErrDuplicateResource means two resource intents in the same file
	// name the same (stack, type, name) address.
	ErrDuplicateResource = errors.New("resolve: duplicate resource address in intent file")

	// ErrUnknownType means the provider schema has no such resource type
	// at all (docs/resolver-adversarial.md row 8).
	ErrUnknownType = errors.New("resolve: provider schema has no such type")

	// ErrCreateTargetExists means op "create" names an address the ledger
	// already has (docs/resolver.md's "op: explicit, not inferred").
	ErrCreateTargetExists = errors.New("resolve: op \"create\" names an address the ledger already has -- use op \"modify\"")

	// ErrModifyTargetMissing means op "modify" names an address the
	// ledger has never recorded (docs/resolver-adversarial.md row 9).
	ErrModifyTargetMissing = errors.New("resolve: op \"modify\" names an address the ledger has never recorded -- did you mean \"create\"?")

	// ErrRefNotFound means a $ref/$cross doesn't resolve to any known
	// resource or attribute path (docs/resolver-adversarial.md row 3).
	ErrRefNotFound = errors.New("resolve: reference does not resolve to any known resource or attribute")

	// ErrCycleDetected means the intra-stack dependency graph has a cycle
	// (docs/resolver-adversarial.md row 2) -- genuinely new code; v1 XCL's
	// own single-stack graph never detected this at all.
	ErrCycleDetected = errors.New("resolve: circular reference between resources")

	// ErrComputedWhereConcreteRequired means a $computed value was used
	// somewhere docs/resolver.md's own rules require a concrete value
	// (docs/resolver-adversarial.md row 6) -- the direct generalization of
	// v1's own "Pending used where Resolved required" rule.
	ErrComputedWhereConcreteRequired = errors.New("resolve: a $computed value was used where a concrete value is required")

	// ErrSecretNotSensitive means a $secret marker sits at a config path
	// the real provider schema does not flag Sensitive
	// (docs/resolver-adversarial.md row 7) -- a real check v1 never had.
	ErrSecretNotSensitive = errors.New("resolve: $secret placed at an attribute the provider schema does not flag Sensitive")

	// ErrNeighborLedgerMissing means a $cross's ledger_dir has never been
	// initialized at all (docs/resolver-adversarial.md row 4, the "no
	// ledger here at all" half).
	ErrNeighborLedgerMissing = errors.New("resolve: cross-stack ledger_dir has never been initialized")

	// ErrCrossStackAddressNotFound means the neighbor ledger exists but
	// has never recorded the referenced address (docs/resolver-adversarial.md
	// row 4, the "ledger exists, but this wasn't recorded" half --
	// distinguished from ErrNeighborLedgerMissing on purpose).
	ErrCrossStackAddressNotFound = errors.New("resolve: cross-stack address not recorded in the neighbor ledger")

	// ErrCrossStackPinStale means a cross_stack_pin's neighbor ledger has
	// advanced since resolve time (docs/resolver-adversarial.md row 5) --
	// VerifyPins' own sentinel.
	ErrCrossStackPinStale = errors.New("resolve: cross-stack pin is stale -- the neighbor ledger has advanced since this proposal was resolved")
)

// VerifyPins re-derives every cross-stack pin recorded in p (every
// resolution.inputs entry with kind "cross_stack_pin") and confirms its
// neighbor ledger's CURRENT head still matches the PinnedHead recorded at
// resolve time -- docs/resolver.md's own "neighbor-advance staleness"
// mechanism, made checkable (docs/resolver-adversarial.md row 5), the same
// "resolved-time truth vs. accept-time reality" shape core.VerifyFreshness
// already enforces for live cloud state, one level up. Returns
// ErrCrossStackPinStale (wrapping which pin and its two head values) on
// the first mismatch found. Wiring this into `ubx accept` is a later
// session's CLI work (docs/plan.md's own "Resolver v1" wedge); this is the
// mechanism itself, tested hermetically.
func VerifyPins(p *core.Proposal) error {
	for _, in := range p.Resolution.Inputs {
		if in.Kind != "cross_stack_pin" {
			continue
		}
		neighbor := core.Open(in.LedgerDir)
		head, err := neighbor.Head()
		if err != nil {
			return fmt.Errorf("verify pin for %s: %w", in.Resource, err)
		}
		if head != in.PinnedHead {
			return fmt.Errorf("%w: %s (ledger %s) pinned at %s, now %s",
				ErrCrossStackPinStale, in.Resource, in.LedgerDir, in.PinnedHead, head)
		}
	}
	return nil
}

// batchEntry is one resource intent's working state during resolution --
// package-private, never exposed.
type batchEntry struct {
	ri             ResourceIntent
	addr           core.Address
	rawEdges       []string // canonical addresses this entry's raw $ref targets, restricted to this batch
	resolvedConfig map[string]interface{}
}

// Resolve resolves intent against l's current ledger state and schema
// into a draft kind:"change" proposal -- creates and modifies, dependency
// ordered, never destroys (docs/resolver.md's own contract). The whole
// resolution runs through core.DoubleRun (docs/resolver-adversarial.md row
// 1): called twice, byte-compared, a hard failure on any divergence,
// v1 XCL never had an equivalent check at all.
func Resolve(l *core.Ledger, schema SchemaInspector, intent *IntentFile) (*core.Proposal, error) {
	var resolved *core.Proposal
	_, err := core.DoubleRun(func() ([]byte, error) {
		p, err := resolveOnce(l, schema, intent)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		resolved = p
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func resolveOnce(l *core.Ledger, schema SchemaInspector, intent *IntentFile) (*core.Proposal, error) {
	if intent.Kind != IntentFileKind {
		return nil, fmt.Errorf("%w: got %q", ErrUnknownIntentKind, intent.Kind)
	}

	batch := make(map[string]*batchEntry, len(intent.Resources))
	order := make([]string, 0, len(intent.Resources))

	for _, ri := range intent.Resources {
		if !schema.HasType(ri.Type) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownType, ri.Type)
		}
		if ri.Op != OpCreate && ri.Op != OpModify {
			return nil, fmt.Errorf("%w: %q (resource %s.%s)", ErrInvalidOp, ri.Op, ri.Type, ri.Name)
		}
		addr := core.Address{Stack: intent.Stack, Type: ri.Type, Name: ri.Name}
		key := addr.String()
		if _, dup := batch[key]; dup {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateResource, addr)
		}

		_, found, err := l.FoldState(addr)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", addr, err)
		}
		switch ri.Op {
		case OpCreate:
			if found {
				return nil, fmt.Errorf("%w: %s", ErrCreateTargetExists, addr)
			}
		case OpModify:
			if !found {
				return nil, fmt.Errorf("%w: %s", ErrModifyTargetMissing, addr)
			}
		}

		batch[key] = &batchEntry{ri: ri, addr: addr}
		order = append(order, key)
	}

	for _, key := range order {
		e := batch[key]
		var raw interface{}
		if err := json.Unmarshal(e.ri.Config, &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode config: %w", e.addr, err)
		}
		edges, err := scanRefEdges(raw, batch)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		e.rawEdges = edges
	}

	topoOrder, err := topoSort(order, func(key string) []string { return batch[key].rawEdges })
	if err != nil {
		return nil, err
	}

	var resolutionInputs []core.ResolutionInput
	for _, key := range topoOrder {
		e := batch[key]
		var raw interface{}
		if err := json.Unmarshal(e.ri.Config, &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode config: %w", e.addr, err)
		}
		resolvedVal, inputs, err := resolveValue(raw, "", e.ri.Type, l, schema, batch)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		resolvedMap, ok := resolvedVal.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("resolve %s: config must be a JSON object", e.addr)
		}
		e.resolvedConfig = resolvedMap
		resolutionInputs = append(resolutionInputs, inputs...)
	}

	var creates []json.RawMessage
	var modifies []core.Modification
	for _, key := range topoOrder {
		e := batch[key]
		dependsOn := append([]string(nil), e.rawEdges...)
		sort.Strings(dependsOn)

		switch e.ri.Op {
		case OpCreate:
			node := map[string]interface{}{
				"stack":  e.addr.Stack,
				"type":   e.addr.Type,
				"name":   e.addr.Name,
				"config": e.resolvedConfig,
			}
			if len(dependsOn) > 0 {
				node["depends_on"] = dependsOn
			}
			b, err := json.Marshal(node)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			creates = append(creates, b)

		case OpModify:
			current, _, err := l.FoldState(e.addr)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			resolvedBytes, err := json.Marshal(e.resolvedConfig)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			before, after, err := core.DiffAttributes(current, resolvedBytes)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			observedHash, err := core.ObservedHash(current)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			modifies = append(modifies, core.Modification{
				Target:    e.addr,
				Before:    before,
				After:     after,
				DependsOn: dependsOn,
			})
			resolutionInputs = append(resolutionInputs, core.ResolutionInput{
				Kind:         "live_state",
				Resource:     e.addr.String(),
				ObservedHash: observedHash,
			})
		}
	}

	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	return &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         intent.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        intent.Intent,
		Delta: core.Delta{
			Creates:  creates,
			Modifies: modifies,
		},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs:     resolutionInputs,
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{
			Creates:  int64(len(creates)),
			Modifies: int64(len(modifies)),
		},
		Status: core.StatusDraft,
	}, nil
}
