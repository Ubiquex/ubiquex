package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/provider"
)

// stateReaderAdapter adapts a provider.Provider to core.StateReader. core
// deliberately doesn't import package provider (see core/scan.go); this is
// the small translation layer that lives at the one place that needs both.
//
// salt (UBI-23, docs/architecture.md -- Secrets) is this ledger
// directory's redaction salt (core.Ledger.Salt) -- ReadResource redacts
// every Sensitive-flagged attribute before core ever sees the observed
// state, since this adapter is the one place that still holds the
// concrete *provider.Schema (hence its Sensitive flags) before it's
// type-erased to core.StateReader's opaque `any`. source (UBI-24,
// docs/architecture.md -- Sensitive overrides) is the provider's
// Terraform-source identity (e.g. "hashicorp/helm") -- the same string
// already threaded through as ScanRequest.ProviderSource (UBI-21) -- so
// provider.Redact can also consult the (source, type) -keyed override
// table alongside the schema's own Sensitive flags.
type stateReaderAdapter struct {
	p      provider.Provider
	salt   []byte
	source string
}

func (a stateReaderAdapter) Schema(ctx context.Context) (any, map[string]any, error) {
	schemas, err := a.p.Schema(ctx)
	if err != nil {
		return nil, nil, err
	}
	resources := make(map[string]any, len(schemas.Resources))
	for name, s := range schemas.Resources {
		resources[name] = s
	}
	return schemas.Provider, resources, nil
}

func (a stateReaderAdapter) Configure(ctx context.Context, providerSchema any, config json.RawMessage) error {
	ps, ok := providerSchema.(*provider.Schema)
	if !ok {
		return fmt.Errorf("stateReaderAdapter: unexpected provider schema type %T", providerSchema)
	}
	return a.p.Configure(ctx, ps, config)
}

func (a stateReaderAdapter) ReadResource(ctx context.Context, resourceSchema any, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	rs, ok := resourceSchema.(*provider.Schema)
	if !ok {
		return nil, fmt.Errorf("stateReaderAdapter: unexpected resource schema type %T", resourceSchema)
	}
	observed, err := a.p.ReadResource(ctx, rs, typeName, currentState)
	if err != nil {
		return nil, err
	}
	if len(observed) == 0 {
		return observed, nil
	}
	return provider.Redact(a.source, typeName, rs.Block, a.salt, observed)
}

// ApplyResourceChange satisfies executor.Applier (UBI-26, docs/executor.md)
// on top of the same stateReaderAdapter ReadResource already uses --
// stateReaderAdapter now implements both core.StateReader and
// executor.Applier, so newStateReader/newApplier are two views of the same
// concrete adapter, not two implementations. Redaction applies here in
// exactly the same way as ReadResource (docs/schema.md's apply-record
// amendment: "provider_result... MUST go through provider.Redact... before
// ever being written into an apply record" -- a live secret is exactly as
// reachable through an apply's returned attributes as through a read's).
//
// A provider.DiagnosticError -- a real, structured ERROR-severity
// diagnostic, never a transport/RPC-level failure -- is wrapped as
// executor.TerminalError, the signal core/executor's own state machine
// uses to classify "terminal" (never retried within one ship invocation)
// rather than "retryable" (docs/executor.md's error taxonomy). Any other
// error (a dropped connection, a context deadline, ...) is returned as-is,
// since core/executor already treats "anything that isn't a TerminalError"
// as retryable.
func (a stateReaderAdapter) ApplyResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, plannedState json.RawMessage, plannedPrivate []byte) (json.RawMessage, json.RawMessage, error) {
	rs, ok := resourceSchema.(*provider.Schema)
	if !ok {
		return nil, nil, fmt.Errorf("stateReaderAdapter: unexpected resource schema type %T", resourceSchema)
	}
	// docs/executor.md -- "Constructing PlannedState without planning":
	// config is set identically to plannedState, since a revert has no
	// separate "desired config" distinct from the value being restored to.
	// plannedPrivate is threaded through unmodified -- required for a
	// destroy (UBI-30), always nil for every other kind.
	result, err := a.p.ApplyResourceChange(ctx, rs, typeName, priorState, plannedState, plannedState, plannedPrivate)
	if err != nil {
		var diag *provider.DiagnosticError
		if errors.As(err, &diag) {
			return nil, nil, &executor.TerminalError{Err: err}
		}
		return nil, nil, err
	}
	if len(result) == 0 {
		return result, nil, nil
	}
	redacted, err := provider.Redact(a.source, typeName, rs.Block, a.salt, result)
	if err != nil {
		return nil, nil, err
	}
	// UBI-63 session 2: this is the one place a concrete schema is
	// actually in scope for a fresh create's own ApplyResourceChange
	// call (executor.Applier's own resourceSchema parameter stays
	// opaque, core/executor's provider-import-free boundary) -- so the
	// lookup key is computed here, using the real schema's own Required
	// attribute names, rather than left to core.DeriveLookupFromResult's
	// own id-only default (core/apply.go's own doc comment: "id" alone
	// doesn't round-trip back into a working re-read for every real
	// resource type, aws_iam_role_policy_attachment confirmed live).
	var requiredAttrs []string
	for _, attr := range rs.Block.Attributes {
		if attr.Required {
			requiredAttrs = append(requiredAttrs, attr.Name)
		}
	}
	lookup := core.DeriveLookupFromResult(redacted, requiredAttrs)
	return redacted, lookup, nil
}

// PlanResourceChange satisfies executor.Applier (UBI-30, docs/executor.md's
// own correction) -- a real plan call is unconditionally required before a
// destroy's own ApplyResourceChange (see that method's own doc comment for
// why). Diagnostic classification mirrors ApplyResourceChange's own
// exactly: a real, structured ERROR-severity diagnostic becomes an
// executor.TerminalError; anything else (a dropped connection, a context
// deadline) is returned as-is and treated as retryable.
func (a stateReaderAdapter) PlanResourceChange(ctx context.Context, resourceSchema any, typeName string, priorState, proposedNewState json.RawMessage) (json.RawMessage, []byte, error) {
	rs, ok := resourceSchema.(*provider.Schema)
	if !ok {
		return nil, nil, fmt.Errorf("stateReaderAdapter: unexpected resource schema type %T", resourceSchema)
	}
	plannedState, plannedPrivate, err := a.p.PlanResourceChange(ctx, rs, typeName, priorState, proposedNewState)
	if err != nil {
		var diag *provider.DiagnosticError
		if errors.As(err, &diag) {
			return nil, nil, &executor.TerminalError{Err: err}
		}
		return nil, nil, err
	}
	return plannedState, plannedPrivate, nil
}

// newStateReader wraps a provider.Provider as a core.StateReader. salt is
// the ledger directory's redaction salt (core.Ledger.Salt); source is the
// provider's registry source (e.g. "hashicorp/aws"), empty for a raw
// --provider path -- see stateReaderAdapter's own doc comment.
func newStateReader(p provider.Provider, salt []byte, source string) core.StateReader {
	return stateReaderAdapter{p: p, salt: salt, source: source}
}

// newApplier wraps a provider.Provider as an executor.Applier (UBI-26) --
// the same stateReaderAdapter newStateReader builds, since it now
// implements ApplyResourceChange too. Returned as executor.Applier so
// ship.go's own call site never needs to know the concrete adapter type.
func newApplier(p provider.Provider, salt []byte, source string) executor.Applier {
	return stateReaderAdapter{p: p, salt: salt, source: source}
}
