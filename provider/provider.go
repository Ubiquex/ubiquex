package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/ubiquex/ubiquex/provider/tfplugin5"
	"github.com/ubiquex/ubiquex/provider/tfplugin6"
)

// ubxTerraformVersion is what ubx reports as its "terraform_version" in
// Configure/ConfigureProvider calls. ubx is not Terraform, but providers use
// this purely for their own logging/telemetry, and some gate behavior on a
// minimum version — report a recent Terraform release rather than an empty
// or clearly-fictitious string.
const ubxTerraformVersion = "1.9.0"

// Provider is ubx's protocol-agnostic view of a launched provider binary. A
// Provider always speaks exactly one wire protocol (5 or 6); which one was
// negotiated during the handshake (see handshake.go) determines which
// concrete implementation backs it. Callers never need to branch on
// protocol version themselves.
type Provider interface {
	// ProtocolVersion reports which tfplugin wire protocol this provider
	// negotiated: 5 or 6.
	ProtocolVersion() int

	// Schema fetches the provider's full schema dump.
	Schema(ctx context.Context) (*Schemas, error)

	// Configure performs the provider's one-time initialization.
	// providerSchema is Schemas.Provider from a prior Schema call — its
	// attribute types are what config gets encoded against. config is a
	// JSON object shaped per those attributes; any attribute it omits is
	// sent as null.
	Configure(ctx context.Context, providerSchema *Schema, config json.RawMessage) error

	// ReadResource fetches the live state of one resource instance.
	// resourceSchema is Schemas.Resources[typeName] from a prior Schema
	// call. currentState is a JSON object shaped per that schema's
	// attributes (at minimum, whatever the provider needs to identify the
	// resource, e.g. "bucket" for aws_s3_bucket); omitted attributes are
	// sent as null. Returns the provider's view of the resource's current
	// state, as JSON shaped the same way.
	ReadResource(ctx context.Context, resourceSchema *Schema, typeName string, currentState json.RawMessage) (json.RawMessage, error)

	// ApplyResourceChange asks the provider to move a resource from
	// priorState to plannedState (UBI-26, docs/executor.md). Both are JSON
	// objects shaped per resourceSchema's attributes, the same convention
	// ReadResource's currentState uses. config is sent identically to
	// plannedState (see docs/executor.md — "Constructing PlannedState
	// without planning": v1 has no separate desired-config distinct from
	// the value being restored to). plannedPrivate is empty for every kind
	// this codebase shipped before UBI-30's own correction (create/modify's
	// "no separate plan phase" shortcut needs none) — a destroy is the one
	// exception: it MUST carry the real bytes a matching PlanResourceChange
	// call returned, verified empirically (UBI-30, docs/executor.md's own
	// correction) against a real, complex SDKv2 AWS provider: without them,
	// the provider's own internal shim doesn't recognize this as a genuine
	// destroy at all and silently no-ops, returning success without
	// deleting anything. Returns the provider's post-apply state. An error
	// satisfying errors.As(err, *DiagnosticError) means the provider itself
	// returned a real, structured ERROR diagnostic — never a transport/
	// RPC-level failure — which core/executor's caller (see
	// cli/stateadapter.go) uses to classify "terminal" (docs/executor.md's
	// error taxonomy) rather than blindly retrying.
	ApplyResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, plannedState, config json.RawMessage, plannedPrivate []byte) (json.RawMessage, error)

	// PlanResourceChange asks the provider to compute a real plan moving a
	// resource from priorState toward proposedNewState -- UBI-30's own
	// correction to docs/executor.md's original "no separate plan phase"
	// design: found empirically that a real destroy against a real,
	// complex SDKv2 provider needs the PlannedPrivate bytes only a genuine
	// PlanResourceChange call produces (a directly-constructed
	// ApplyResourceChange call, PlannedPrivate left empty, silently
	// no-ops -- the provider's own shim never invokes the resource's real
	// Delete function at all). config is sent identically to
	// proposedNewState, the same convention ApplyResourceChange's own
	// config parameter already uses. priorPrivate is always empty here --
	// this codebase never tracks a resource's own private bytes across
	// separate `ubx` invocations, so every PlanResourceChange call is, from
	// the provider's perspective, the first plan it has ever seen for this
	// resource. plannedState is returned so a caller can sanity-check it
	// against what it expected (null, for a destroy); plannedPrivate must
	// be threaded, opaque and unmodified, into the matching
	// ApplyResourceChange call.
	PlanResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, proposedNewState json.RawMessage) (plannedState json.RawMessage, plannedPrivate []byte, err error)
}

// newProvider builds the protocol-appropriate Provider for a negotiated
// wire version over an already-dialed connection.
func newProvider(version int, conn *grpc.ClientConn) (Provider, error) {
	switch version {
	case 5:
		return &v5Provider{client: tfplugin5.NewProviderClient(conn)}, nil
	case 6:
		return &v6Provider{client: tfplugin6.NewProviderClient(conn)}, nil
	default:
		return nil, fmt.Errorf("%w: negotiated protocol version %d", ErrProtocolMismatch, version)
	}
}

// DiagnosticError wraps one or more ERROR-severity diagnostics a provider
// RPC returned -- as opposed to a transport/RPC-level failure (a dropped
// connection, a context deadline, ...), which surfaces as a plain,
// undecorated error instead. This distinction matters beyond ApplyResourceChange
// (UBI-26): it's what lets a caller (cli/stateadapter.go) classify a real,
// structured provider answer as "terminal" rather than something merely
// ambiguous or transient -- docs/executor.md's error taxonomy is built on
// exactly this signal.
type DiagnosticError struct {
	Messages []string
}

func (e *DiagnosticError) Error() string {
	return fmt.Sprintf("provider returned %d diagnostic error(s): %s", len(e.Messages), strings.Join(e.Messages, "; "))
}

// diagnosticError renders a set of provider diagnostics as a single
// *DiagnosticError, or nil if none of them are error-severity. Warnings are
// ignored here — ubx has no channel to surface them to yet.
func diagnosticError[D any](diags []D, severity func(D) int32, errorSeverity int32, text func(D) string) error {
	var msgs []string
	for _, d := range diags {
		if severity(d) == errorSeverity {
			msgs = append(msgs, text(d))
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return &DiagnosticError{Messages: msgs}
}

type v6Provider struct {
	client tfplugin6.ProviderClient
}

func (p *v6Provider) ProtocolVersion() int { return 6 }

func (p *v6Provider) Schema(ctx context.Context) (*Schemas, error) {
	resp, err := p.client.GetProviderSchema(ctx, &tfplugin6.GetProviderSchema_Request{})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV6(resp.Diagnostics); err != nil {
		return nil, err
	}
	return &Schemas{
		Provider:    schemaFromV6(resp.Provider),
		Resources:   schemaMapFromV6(resp.ResourceSchemas),
		DataSources: schemaMapFromV6(resp.DataSourceSchemas),
	}, nil
}

func (p *v6Provider) Configure(ctx context.Context, providerSchema *Schema, config json.RawMessage) error {
	payload, err := encodeDynamicValue(providerSchema.Block, config)
	if err != nil {
		return fmt.Errorf("encode provider config: %w", err)
	}
	resp, err := p.client.ConfigureProvider(ctx, &tfplugin6.ConfigureProvider_Request{
		TerraformVersion: ubxTerraformVersion,
		Config:           &tfplugin6.DynamicValue{Msgpack: payload},
	})
	if err != nil {
		return err
	}
	return diagnosticErrorV6(resp.Diagnostics)
}

func (p *v6Provider) ReadResource(ctx context.Context, resourceSchema *Schema, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	payload, err := encodeDynamicValue(resourceSchema.Block, currentState)
	if err != nil {
		return nil, fmt.Errorf("encode current state: %w", err)
	}
	resp, err := p.client.ReadResource(ctx, &tfplugin6.ReadResource_Request{
		TypeName:     typeName,
		CurrentState: &tfplugin6.DynamicValue{Msgpack: payload},
	})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV6(resp.Diagnostics); err != nil {
		return nil, err
	}
	return decodeDynamicValue(resourceSchema.Block, resp.NewState.GetMsgpack(), resp.NewState.GetJson())
}

func (p *v6Provider) ApplyResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, plannedState, config json.RawMessage, plannedPrivate []byte) (json.RawMessage, error) {
	// priorState keeps the plain encoder: it either represents an
	// already-observed, fully concrete resource (drift_revert; every
	// attribute present) or a genuine from-scratch create, encoded as
	// literal JSON "null" by the caller (core/executor) -- ctyjson's own
	// null-token handling already produces the correct top-level
	// cty.NullVal(ty) for that case, no unknown-awareness needed here.
	// plannedState/config use the unknown-aware encoder (ctyvalue.go's
	// UBI-27 fix): a $computed marker, or a schema-Computed attribute the
	// resolved config never set at all, both need to reach the wire as a
	// real cty.UnknownVal, not Null.
	priorPayload, err := encodeDynamicValue(resourceSchema.Block, priorState)
	if err != nil {
		return nil, fmt.Errorf("encode prior state: %w", err)
	}
	plannedPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, plannedState)
	if err != nil {
		return nil, fmt.Errorf("encode planned state: %w", err)
	}
	configPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, config)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	resp, err := p.client.ApplyResourceChange(ctx, &tfplugin6.ApplyResourceChange_Request{
		TypeName:       typeName,
		PriorState:     &tfplugin6.DynamicValue{Msgpack: priorPayload},
		PlannedState:   &tfplugin6.DynamicValue{Msgpack: plannedPayload},
		Config:         &tfplugin6.DynamicValue{Msgpack: configPayload},
		PlannedPrivate: plannedPrivate,
	})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV6(resp.Diagnostics); err != nil {
		return nil, err
	}
	return decodeDynamicValue(resourceSchema.Block, resp.NewState.GetMsgpack(), resp.NewState.GetJson())
}

func (p *v6Provider) PlanResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, proposedNewState json.RawMessage) (json.RawMessage, []byte, error) {
	// Same encoder split as ApplyResourceChange: priorState plain,
	// proposedNewState/config unknown-aware (a destroy's own null is a
	// present, concrete null either way -- the unknown-aware encoder
	// already treats a present null as null, unchanged from the plain
	// encoder's own behavior for that case).
	priorPayload, err := encodeDynamicValue(resourceSchema.Block, priorState)
	if err != nil {
		return nil, nil, fmt.Errorf("encode prior state: %w", err)
	}
	proposedPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, proposedNewState)
	if err != nil {
		return nil, nil, fmt.Errorf("encode proposed new state: %w", err)
	}
	resp, err := p.client.PlanResourceChange(ctx, &tfplugin6.PlanResourceChange_Request{
		TypeName:         typeName,
		PriorState:       &tfplugin6.DynamicValue{Msgpack: priorPayload},
		ProposedNewState: &tfplugin6.DynamicValue{Msgpack: proposedPayload},
		Config:           &tfplugin6.DynamicValue{Msgpack: proposedPayload},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := diagnosticErrorV6(resp.Diagnostics); err != nil {
		return nil, nil, err
	}
	plannedState, err := decodeDynamicValue(resourceSchema.Block, resp.PlannedState.GetMsgpack(), resp.PlannedState.GetJson())
	if err != nil {
		return nil, nil, err
	}
	return plannedState, resp.PlannedPrivate, nil
}

func diagnosticErrorV6(diags []*tfplugin6.Diagnostic) error {
	return diagnosticError(diags,
		func(d *tfplugin6.Diagnostic) int32 { return int32(d.Severity) },
		int32(tfplugin6.Diagnostic_ERROR),
		func(d *tfplugin6.Diagnostic) string { return d.Summary + ": " + d.Detail },
	)
}

type v5Provider struct {
	client tfplugin5.ProviderClient
}

func (p *v5Provider) ProtocolVersion() int { return 5 }

func (p *v5Provider) Schema(ctx context.Context) (*Schemas, error) {
	resp, err := p.client.GetSchema(ctx, &tfplugin5.GetProviderSchema_Request{})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV5(resp.Diagnostics); err != nil {
		return nil, err
	}
	return &Schemas{
		Provider:    schemaFromV5(resp.Provider),
		Resources:   schemaMapFromV5(resp.ResourceSchemas),
		DataSources: schemaMapFromV5(resp.DataSourceSchemas),
	}, nil
}

func (p *v5Provider) Configure(ctx context.Context, providerSchema *Schema, config json.RawMessage) error {
	payload, err := encodeDynamicValue(providerSchema.Block, config)
	if err != nil {
		return fmt.Errorf("encode provider config: %w", err)
	}
	resp, err := p.client.Configure(ctx, &tfplugin5.Configure_Request{
		TerraformVersion: ubxTerraformVersion,
		Config:           &tfplugin5.DynamicValue{Msgpack: payload},
	})
	if err != nil {
		return err
	}
	return diagnosticErrorV5(resp.Diagnostics)
}

func (p *v5Provider) ReadResource(ctx context.Context, resourceSchema *Schema, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	payload, err := encodeDynamicValue(resourceSchema.Block, currentState)
	if err != nil {
		return nil, fmt.Errorf("encode current state: %w", err)
	}
	resp, err := p.client.ReadResource(ctx, &tfplugin5.ReadResource_Request{
		TypeName:     typeName,
		CurrentState: &tfplugin5.DynamicValue{Msgpack: payload},
	})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV5(resp.Diagnostics); err != nil {
		return nil, err
	}
	return decodeDynamicValue(resourceSchema.Block, resp.NewState.GetMsgpack(), resp.NewState.GetJson())
}

func (p *v5Provider) ApplyResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, plannedState, config json.RawMessage, plannedPrivate []byte) (json.RawMessage, error) {
	// See v6Provider.ApplyResourceChange's own comment: priorState stays
	// on the plain encoder, plannedState/config use the unknown-aware one.
	priorPayload, err := encodeDynamicValue(resourceSchema.Block, priorState)
	if err != nil {
		return nil, fmt.Errorf("encode prior state: %w", err)
	}
	plannedPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, plannedState)
	if err != nil {
		return nil, fmt.Errorf("encode planned state: %w", err)
	}
	configPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, config)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	resp, err := p.client.ApplyResourceChange(ctx, &tfplugin5.ApplyResourceChange_Request{
		TypeName:       typeName,
		PriorState:     &tfplugin5.DynamicValue{Msgpack: priorPayload},
		PlannedState:   &tfplugin5.DynamicValue{Msgpack: plannedPayload},
		Config:         &tfplugin5.DynamicValue{Msgpack: configPayload},
		PlannedPrivate: plannedPrivate,
	})
	if err != nil {
		return nil, err
	}
	if err := diagnosticErrorV5(resp.Diagnostics); err != nil {
		return nil, err
	}
	return decodeDynamicValue(resourceSchema.Block, resp.NewState.GetMsgpack(), resp.NewState.GetJson())
}

func (p *v5Provider) PlanResourceChange(ctx context.Context, resourceSchema *Schema, typeName string, priorState, proposedNewState json.RawMessage) (json.RawMessage, []byte, error) {
	priorPayload, err := encodeDynamicValue(resourceSchema.Block, priorState)
	if err != nil {
		return nil, nil, fmt.Errorf("encode prior state: %w", err)
	}
	proposedPayload, err := encodeUnknownAwareDynamicValue(resourceSchema.Block, proposedNewState)
	if err != nil {
		return nil, nil, fmt.Errorf("encode proposed new state: %w", err)
	}
	resp, err := p.client.PlanResourceChange(ctx, &tfplugin5.PlanResourceChange_Request{
		TypeName:         typeName,
		PriorState:       &tfplugin5.DynamicValue{Msgpack: priorPayload},
		ProposedNewState: &tfplugin5.DynamicValue{Msgpack: proposedPayload},
		Config:           &tfplugin5.DynamicValue{Msgpack: proposedPayload},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := diagnosticErrorV5(resp.Diagnostics); err != nil {
		return nil, nil, err
	}
	plannedState, err := decodeDynamicValue(resourceSchema.Block, resp.PlannedState.GetMsgpack(), resp.PlannedState.GetJson())
	if err != nil {
		return nil, nil, err
	}
	return plannedState, resp.PlannedPrivate, nil
}

func diagnosticErrorV5(diags []*tfplugin5.Diagnostic) error {
	return diagnosticError(diags,
		func(d *tfplugin5.Diagnostic) int32 { return int32(d.Severity) },
		int32(tfplugin5.Diagnostic_ERROR),
		func(d *tfplugin5.Diagnostic) string { return d.Summary + ": " + d.Detail },
	)
}
