package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ubiquex/ubiquex/approval"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// The two change tools (UBI-295): plan proposes, apply ships what a human
// already approved. Neither creates an acceptance, and no tool does. The
// agent's only route to a shipped change runs through approval.Channel,
// where a human approves on a page the agent cannot drive. Absence is the
// gate: there is no approve tool to misconfigure, guard or leak a token
// for. apply refuses until the ledger holds an acceptance for the exact
// hash plan returned, and its refusal is written for the agent to relay
// to a human.

// noChannelError is what plan and apply say on a server that serves no
// stacks, and so started no approval page.
var noChannelError = errors.New("this server serves no stacks, so nothing can be planned or applied. It has to be started with `ubx mcp --serve <stack-root>`, once per stack; ask the person who configured it to add one")

// pendingLookup is the approval page's view of the served stacks: a
// pending proposal is a plan file in the stack's own .ubx/plans/, keyed by
// its full hash, and an approved one is the ledger's own entry. Every
// check `ubx ship` runs on a plan before its signing moment runs here
// before a human is asked to approve, so a page can never offer
// something that could not then ship.
func pendingLookup(served *servedStacks) approval.Lookup {
	return func(ctx context.Context, stack, hash string) (*approval.Pending, error) {
		st, ok := served.byName[stack]
		if !ok {
			return nil, fmt.Errorf("stack %s is not served here: %w", stack, approval.ErrNotPending)
		}
		ledger, closeLedger, err := st.openLedger(ctx)
		if err != nil {
			return nil, err
		}
		if p, err := ledger.Read(hash); err == nil {
			return &approval.Pending{Ledger: ledger, Close: closeLedger, Accepted: p}, nil
		} else if !errors.Is(err, core.ErrProposalNotFound) {
			closeLedger()
			return nil, err
		}
		draft, err := readPlanFile(st.Root, hash)
		if os.IsNotExist(err) {
			closeLedger()
			return nil, fmt.Errorf("stack %s, plan %s: %w", stack, hash, approval.ErrNotPending)
		}
		if err != nil {
			closeLedger()
			return nil, err
		}
		if err := checkPendingPlan(draft, stack, hash); err != nil {
			closeLedger()
			return nil, err
		}
		return &approval.Pending{Ledger: ledger, Close: closeLedger, Draft: draft}, nil
	}
}

// checkPendingPlan is what a plan file must satisfy before anyone is asked
// to approve it: it is the stack's, it hashes to the name it is filed
// under, its cross-stack pins are still fresh, and its delta forms a
// dependency graph. The same checks, in the same order, as `ubx ship`'s
// own resolveAndValidatePlan and ValidateChangeGraph.
func checkPendingPlan(draft *core.Proposal, stack, hash string) error {
	if draft.Stack != stack {
		return fmt.Errorf("plan %s is for stack %q, not %q", hash, draft.Stack, stack)
	}
	computed, err := core.Hash(draft)
	if err != nil {
		return err
	}
	if computed != hash {
		return fmt.Errorf("the plan filed as %s hashes to %s: it was edited or corrupted after it was made. Ask the agent to plan the change again", hash, computed)
	}
	if err := resolver.VerifyPins(draft); err != nil {
		return err
	}
	return executor.ValidateChangeGraph(draft)
}

// --- plan ---

type planToolInput struct {
	Stack  string         `json:"stack" jsonschema:"a stack name, as the stacks tool lists it"`
	Intent map[string]any `json:"intent" jsonschema:"the change, as an ubx:intent/v1 document: {\"intent\": {\"summary\": \"...\"}, \"resources\": [{\"type\": \"<type>\", \"name\": \"<name>\", \"op\": \"create\"|\"modify\", \"config\": {...}}], \"destroys\": [\"<stack>.<type>.<name>\"]}. stack, kind and schema_version are filled in. Use the schema tool for what config a type takes, and state for a modify's current values"`
}

type planJSON struct {
	Format       int              `json:"format"`
	Stack        string           `json:"stack"`
	ProposalHash string           `json:"proposal_hash"`
	ApprovalURL  string           `json:"approval_url"`
	BlastRadius  core.BlastRadius `json:"blast_radius"`
	Next         string           `json:"next"`
	Render       string           `json:"render"`
}

func registerPlanTool(server *mcp.Server, served *servedStacks, channel approval.Channel) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "plan",
		Description: "Propose a change to a served stack. Resolves the intent against the stack's ledger and provider schema, records it as " +
			"a pending proposal, and returns its proposal_hash and an approval_url. Nothing is changed: a human has to open approval_url " +
			"in a browser on the machine running this server and approve it there. You cannot approve it, and no tool can. " +
			"render is exactly what the human will be shown. After the human says they have approved, call apply with the same proposal_hash.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in planToolInput) (*mcp.CallToolResult, any, error) {
		if channel == nil {
			return nil, nil, noChannelError
		}
		st, err := served.get(in.Stack)
		if err != nil {
			return nil, nil, err
		}
		if in.Intent == nil {
			return nil, nil, errors.New("intent is required: an ubx:intent/v1 document describing the change")
		}
		intent, err := intentFromTool(in.Intent, st.Name)
		if err != nil {
			return nil, nil, err
		}

		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		loaded, err := st.providerSchemas(ctx)
		if err != nil {
			return nil, nil, err
		}
		providers := make([]resolver.DeclaredProvider, 0, len(loaded))
		for _, l := range loaded {
			providers = append(providers, resolver.DeclaredProvider{Source: l.Source, Version: l.Version, Schema: newSchemaInspector(l.Schemas)})
		}

		ledger, closeLedger, err := st.openLedger(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer closeLedger()

		p, err := resolver.Resolve(ledger, providers, intent, knownDependentsFor(st.Cfg, nil))
		if err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		hash, err := core.Hash(p)
		if err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		if err := executor.ValidateChangeGraph(p); err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		data, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("plan: marshal proposal: %w", err)
		}
		if _, err := writePlanFile(st.Root, hash, data); err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		// The same tidy-up `ubx plan` does: a plan the ledger has moved
		// past can never be approved, so it goes. Never fatal.
		_, _ = pruneUnshippablePlans(st.Root, ledger, st.Name, hash)

		render, err := core.RenderProposal(p)
		if err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		handle, err := channel.Request(st.Name, hash)
		if err != nil {
			return nil, nil, fmt.Errorf("plan: %w", err)
		}
		return nil, planJSON{
			Format:       jsonFormatVersion,
			Stack:        st.Name,
			ProposalHash: hash,
			ApprovalURL:  handle.URL,
			BlastRadius:  p.BlastRadius,
			Next: "Nothing has been changed. Ask the human to open " + handle.URL + " in a browser on the machine running ubx, " +
				"read the change, and approve it there if it is right. You cannot approve it yourself. When they say they have, " +
				"call apply with stack " + st.Name + " and this proposal_hash.",
			Render: string(render),
		}, nil
	})
}

// intentFromTool turns the plan tool's intent object into an
// ubx:intent/v1 document for stack. Unknown fields are refused rather
// than dropped, so a misspelled key is an error the agent sees and not a
// silently different change. Blueprint calls are refused: resolving one
// can fetch a blueprint over the network, and blueprints are outside this
// surface for now.
func intentFromTool(raw map[string]any, stack string) (*resolver.IntentFile, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("intent: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var intent resolver.IntentFile
	if err := dec.Decode(&intent); err != nil {
		return nil, fmt.Errorf("intent is not a valid ubx:intent/v1 document: %w", err)
	}
	if intent.Stack == "" {
		intent.Stack = stack
	}
	if intent.Stack != stack {
		return nil, fmt.Errorf("intent names stack %q but the call is for stack %q", intent.Stack, stack)
	}
	if intent.Kind == "" {
		intent.Kind = "ubx:intent/v1"
	}
	if intent.SchemaVersion == 0 {
		intent.SchemaVersion = 1
	}
	if len(intent.BlueprintCalls) > 0 || len(intent.BlueprintOutputs) > 0 {
		return nil, errors.New("intent calls a blueprint, which plan does not support over MCP. Write the resources out directly")
	}
	return &intent, nil
}

// --- apply ---

type applyToolInput struct {
	Stack        string `json:"stack" jsonschema:"a stack name, as the stacks tool lists it"`
	ProposalHash string `json:"proposal_hash" jsonschema:"the full 64-character proposal_hash plan returned"`
}

type applyJSON struct {
	Format       int                 `json:"format"`
	Stack        string              `json:"stack"`
	ProposalHash string              `json:"proposal_hash"`
	Outcome      string              `json:"outcome"`
	Acceptance   *core.Acceptance    `json:"acceptance"`
	ApplyRecord  *core.ApplyRecord   `json:"apply_record,omitempty"`
	Applies      []*core.ApplyRecord `json:"applies,omitempty"`
	Note         string              `json:"note,omitempty"`
}

func registerApplyTool(server *mcp.Server, served *servedStacks, channel approval.Channel) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "apply",
		Description: "Apply a planned change that a human has already approved. Refuses, and changes nothing, until the stack's ledger holds " +
			"an acceptance for exactly this proposal_hash; the refusal says what the human has to do. Safe to call again: a change already " +
			"applied is reported, not applied twice. Returns the acceptance (who approved, how, and the hash of what they were shown) and " +
			"the apply record.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in applyToolInput) (*mcp.CallToolResult, any, error) {
		if channel == nil {
			return nil, nil, noChannelError
		}
		st, err := served.get(in.Stack)
		if err != nil {
			return nil, nil, err
		}
		if !core.IsHash(in.ProposalHash) {
			return nil, nil, fmt.Errorf("proposal_hash must be the full 64-character hash plan returned, got %q", in.ProposalHash)
		}
		ledger, closeLedger, err := st.openLedger(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer closeLedger()

		p, err := ledger.Read(in.ProposalHash)
		if errors.Is(err, core.ErrProposalNotFound) {
			return nil, nil, notApprovedError(st, channel, in.ProposalHash)
		}
		if err != nil {
			return nil, nil, err
		}
		if p.Acceptance == nil {
			return nil, nil, fmt.Errorf("proposal %s is in the ledger without an acceptance, which should not happen; nothing was applied", p.ID)
		}

		out := applyJSON{Format: jsonFormatVersion, Stack: st.Name, ProposalHash: p.ID, Acceptance: p.Acceptance}
		if isRecordOnlyKind(p.Kind) {
			out.Outcome = "recorded"
			out.Note = fmt.Sprintf("a %s proposal records state and has nothing to execute; approving it was the whole change", p.Kind)
			return nil, out, nil
		}
		if p.Kind != core.KindChange && p.Kind != core.KindDriftRevert {
			return nil, nil, fmt.Errorf("proposal %s is kind %q, which apply does not execute", p.ID, p.Kind)
		}

		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		salt, err := ledger.Salt()
		if err != nil {
			return nil, nil, err
		}
		providerConfig := "{}"
		if len(st.Cfg.ProviderConfig) > 0 {
			if providerConfig, err = providerConfigJSON(st.Cfg.ProviderConfig); err != nil {
				return nil, nil, err
			}
		}
		path, source, version := st.providerSelection()
		pool, closePool, err := newShipPool(ctx, st.Cfg, salt, path, source, version, providerConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("apply: %w", err)
		}
		defer closePool()

		sealed, err := executor.Ship(ctx, ledger, pool, source, p)
		if errors.Is(err, executor.ErrAlreadyApplied) {
			attempts, aerr := ledger.ApplyAttempts(p.ID)
			if aerr != nil {
				return nil, nil, aerr
			}
			out.Outcome = "already_applied"
			out.Applies = attempts
			out.Note = "this change was applied before; nothing was done this time"
			return nil, out, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("apply: %w", err)
		}
		out.Outcome = sealed.Summary.Outcome
		out.ApplyRecord = sealed
		if out.Outcome != "applied" {
			out.Note = "the change did not fully apply; the apply record says which resources failed and why. Calling apply again retries what did not apply"
		}
		return nil, out, nil
	})
}

// notApprovedError is apply's refusal for a hash the ledger has not
// accepted. It is the one error in this surface the agent is expected to
// hit in normal use, so it is written as an instruction to relay, naming
// the place a human approves, rather than as a failure.
func notApprovedError(st *servedStack, channel approval.Channel, hash string) error {
	if _, err := readPlanFile(st.Root, hash); err != nil {
		return fmt.Errorf("stack %s has no plan %s, approved or pending. Plans the stack has moved past are removed. Call plan again to propose the change", st.Name, hash)
	}
	handle, err := channel.Request(st.Name, hash)
	if err != nil {
		return err
	}
	return fmt.Errorf("not applied: nobody has approved this change yet, and nothing was changed. "+
		"Ask the human to open %s in a browser on the machine running ubx, read the change, and approve it there if it is right. "+
		"You cannot approve it yourself. Once they say they have, call apply again with the same proposal_hash", handle.URL)
}

// servedPlanConsumed removes a plan file once its proposal is accepted,
// the same tidy-up `ubx ship` does at its signing moment. The ledger now
// holds the proposal, so the file is redundant. Failure is ignored: a
// leftover file is pruned by the next plan for that stack.
func servedPlanConsumed(served *servedStacks) approval.Accepted {
	return func(stack, hash string) {
		if st, ok := served.byName[stack]; ok {
			_ = os.Remove(planFilePath(st.Root, hash))
		}
	}
}
