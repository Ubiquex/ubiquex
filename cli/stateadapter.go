package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
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
// type-erased to core.StateReader's opaque `any`.
type stateReaderAdapter struct {
	p    provider.Provider
	salt []byte
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
	return provider.Redact(rs.Block, a.salt, observed)
}

// newStateReader wraps a provider.Provider as a core.StateReader. salt is
// the ledger directory's redaction salt (core.Ledger.Salt) -- see
// stateReaderAdapter's own doc comment.
func newStateReader(p provider.Provider, salt []byte) core.StateReader {
	return stateReaderAdapter{p: p, salt: salt}
}
