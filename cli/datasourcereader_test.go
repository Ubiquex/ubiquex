package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// fakeDataSourceSchema is a bare-minimum resolver.SchemaInspector -- this
// file's own tests only ever need HasType (InferProvider's own real
// dependency), the rest are never consulted by attachDataSourceReadersFromPool
// itself.
type fakeDataSourceSchema struct {
	types map[string]bool
}

func (f fakeDataSourceSchema) HasType(t string) bool           { return f.types[t] }
func (f fakeDataSourceSchema) IsComputed(string, string) bool  { return false }
func (f fakeDataSourceSchema) IsSensitive(string, string) bool { return false }
func (f fakeDataSourceSchema) UnknownConfigKeys(string, map[string]interface{}) []resolver.ConfigKeyIssue {
	return nil
}
func (f fakeDataSourceSchema) MissingRequiredKeys(string, map[string]interface{}) []resolver.RequiredAttributeIssue {
	return nil
}

func TestAttachDataSourceReaders_NoDataSources_ReturnsNilImmediately(t *testing.T) {
	// Deliberately nil cfg/ledger/providers -- the real, load-bearing
	// proof that a stack with no data_sources[] never touches any of
	// them: attachDataSourceReaders' own gate must return before ever
	// calling ledger.Salt() (which would panic on a nil *core.Ledger).
	closer, err := attachDataSourceReaders(context.Background(), nil, nil, nil, nil, "", "", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if closer != nil {
		t.Fatalf("expected a nil closer when no data sources are declared, got %v", closer)
	}
}

func TestAttachDataSourceReadersFromPool_RoutesAndAttaches(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"acme/aws": "1.0.0"}, nil)

	providers := []resolver.DeclaredProvider{
		{Source: "acme/aws", Version: "1.0.0", Schema: fakeDataSourceSchema{types: map[string]bool{"data_aws_ec2_instance": true}}},
	}
	dataSources := []resolver.DataSourceIntent{
		{Type: "data_aws_ec2_instance", Name: "primary", Lookup: json.RawMessage(`{"id":"i-123"}`)},
	}

	if err := attachDataSourceReadersFromPool(context.Background(), pool, providers, dataSources); err != nil {
		t.Fatalf("attachDataSourceReadersFromPool: %v", err)
	}
	if len(*launches) != 1 || (*launches)[0] != "acme/aws@1.0.0" {
		t.Fatalf("expected exactly one real launch of acme/aws@1.0.0, got %v", *launches)
	}
	if providers[0].Reader == nil {
		t.Fatal("expected providers[0].Reader to be set to the real, live connection")
	}
}

// TestAttachDataSourceReadersFromPool_DedupesSameProvider is the real
// "one live connection per distinct owning provider, never one per data
// source" proof -- two data sources owned by the same declared provider
// must launch it exactly once.
func TestAttachDataSourceReadersFromPool_DedupesSameProvider(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"acme/aws": "1.0.0"}, nil)

	providers := []resolver.DeclaredProvider{
		{Source: "acme/aws", Version: "1.0.0", Schema: fakeDataSourceSchema{types: map[string]bool{
			"data_aws_ec2_instance": true,
			"data_aws_s3_bucket":    true,
		}}},
	}
	dataSources := []resolver.DataSourceIntent{
		{Type: "data_aws_ec2_instance", Name: "a", Lookup: json.RawMessage(`{}`)},
		{Type: "data_aws_s3_bucket", Name: "b", Lookup: json.RawMessage(`{}`)},
	}

	if err := attachDataSourceReadersFromPool(context.Background(), pool, providers, dataSources); err != nil {
		t.Fatalf("attachDataSourceReadersFromPool: %v", err)
	}
	if len(*launches) != 1 {
		t.Fatalf("expected exactly one real launch (both data sources share the same owning provider), got %v", *launches)
	}
}

func TestAttachDataSourceReadersFromPool_UnknownType_RealError(t *testing.T) {
	pool, _ := newTestPool(t, map[string]string{"acme/aws": "1.0.0"}, nil)

	providers := []resolver.DeclaredProvider{
		{Source: "acme/aws", Version: "1.0.0", Schema: fakeDataSourceSchema{types: map[string]bool{}}},
	}
	dataSources := []resolver.DataSourceIntent{
		{Type: "data_aws_ec2_instance", Name: "primary", Lookup: json.RawMessage(`{}`)},
	}

	err := attachDataSourceReadersFromPool(context.Background(), pool, providers, dataSources)
	if err == nil || !errors.Is(err, resolver.ErrUnknownType) {
		t.Fatalf("expected resolver.ErrUnknownType, got %v", err)
	}
}

// TestAttachDataSourceReadersFromPool_MultipleProviders_EachOwnDataSource
// confirms real multi-provider routing: two distinct declared providers,
// each owning a different referenced type, each gets its own real live
// connection.
func TestAttachDataSourceReadersFromPool_MultipleProviders_EachOwnDataSource(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"acme/aws": "1.0.0", "acme/gcp": "2.0.0"}, nil)

	providers := []resolver.DeclaredProvider{
		{Source: "acme/aws", Version: "1.0.0", Schema: fakeDataSourceSchema{types: map[string]bool{"data_aws_ec2_instance": true}}},
		{Source: "acme/gcp", Version: "2.0.0", Schema: fakeDataSourceSchema{types: map[string]bool{"data_google_compute_instance": true}}},
	}
	dataSources := []resolver.DataSourceIntent{
		{Type: "data_aws_ec2_instance", Name: "a", Lookup: json.RawMessage(`{}`)},
		{Type: "data_google_compute_instance", Name: "b", Lookup: json.RawMessage(`{}`)},
	}

	if err := attachDataSourceReadersFromPool(context.Background(), pool, providers, dataSources); err != nil {
		t.Fatalf("attachDataSourceReadersFromPool: %v", err)
	}
	if len(*launches) != 2 {
		t.Fatalf("expected two real launches, one per distinct owning provider, got %v", *launches)
	}
	if providers[0].Reader == nil || providers[1].Reader == nil {
		t.Fatalf("expected both providers to have a real Reader attached, got %+v", providers)
	}
}

var _ executor.Applier = fakeApplierStub{}
