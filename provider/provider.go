package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/ubiquex/ubiquex-cli/provider/tfplugin5"
	"github.com/ubiquex/ubiquex-cli/provider/tfplugin6"
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

// diagnosticError renders a set of provider diagnostics as a single error,
// or nil if none of them are error-severity. Warnings are ignored here —
// ubx has no channel to surface them to yet.
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
	return fmt.Errorf("provider returned %d diagnostic error(s): %s", len(msgs), strings.Join(msgs, "; "))
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

func diagnosticErrorV5(diags []*tfplugin5.Diagnostic) error {
	return diagnosticError(diags,
		func(d *tfplugin5.Diagnostic) int32 { return int32(d.Severity) },
		int32(tfplugin5.Diagnostic_ERROR),
		func(d *tfplugin5.Diagnostic) string { return d.Summary + ": " + d.Detail },
	)
}
