package cli

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ubiquex/ubiquex/core/executor"
)

// schemaStubApplier is a fakeApplierStub with a real, scriptable Schema()
// -- providerpool_test.go's own fakeApplierStub never needs one, since its
// tests only ever compare Applier identity, never call a method on it.
// declaredProvidersForInference's own tests need Schema() to return real
// type names, so this is its own dedicated stand-in rather than a second
// copy of fakeApplierStub itself.
type schemaStubApplier struct {
	executor.Applier
	types []string
}

func (s schemaStubApplier) Schema(ctx context.Context) (any, map[string]any, error) {
	resources := make(map[string]any, len(s.types))
	for _, t := range s.types {
		resources[t] = struct{}{}
	}
	return struct{}{}, resources, nil
}

func TestResourceTypeSchemaInspector_HasType(t *testing.T) {
	insp := resourceTypeSchemaInspector{resourceSchemas: map[string]any{"aws_db_instance": struct{}{}}}
	if !insp.HasType("aws_db_instance") {
		t.Fatal("HasType(aws_db_instance) = false, want true")
	}
	if insp.HasType("helm_release") {
		t.Fatal("HasType(helm_release) = true, want false -- not in the map")
	}
	// IsComputed/IsSensitive are documented always-false stubs -- resolver.
	// InferProvider (this adapter's only caller) never calls either, but
	// they must still satisfy resolver.SchemaInspector harmlessly.
	if insp.IsComputed("aws_db_instance", "id") || insp.IsSensitive("aws_db_instance", "id") {
		t.Fatal("IsComputed/IsSensitive must always be false stubs")
	}
}

// TestDeclaredProvidersForInference_BuildsFromPool proves the set
// declaredProvidersForInference hands to resolver.InferProvider correctly
// reflects each declared provider's own real Schema() (via
// resourceTypeSchemaInspector), routed through the SAME providerPool a
// status/scanall walk already launched -- never a second, independent
// launch (UBI-43 session 5).
func TestDeclaredProvidersForInference_BuildsFromPool(t *testing.T) {
	versions := map[string]string{"hashicorp/aws": "6.60.0", "hashicorp/helm": "3.0.2"}
	pool, err := newProviderPool(nil, versions, nil)
	if err != nil {
		t.Fatalf("newProviderPool: %v", err)
	}
	var launches []string
	pool.launch = func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error) {
		launches = append(launches, source+"@"+version)
		switch source {
		case "hashicorp/aws":
			return schemaStubApplier{types: []string{"aws_db_instance", "aws_s3_bucket"}}, io.NopCloser(nil), nil
		case "hashicorp/helm":
			return schemaStubApplier{types: []string{"helm_release"}}, io.NopCloser(nil), nil
		default:
			t.Fatalf("unexpected source %q", source)
			return nil, nil, nil
		}
	}

	declared, err := declaredProvidersForInference(context.Background(), pool, versions)
	if err != nil {
		t.Fatalf("declaredProvidersForInference: %v", err)
	}
	if len(declared) != 2 {
		t.Fatalf("got %d declared providers, want 2", len(declared))
	}
	// sortedProviderSources keeps this reproducible -- hashicorp/aws before
	// hashicorp/helm.
	if declared[0].Source != "hashicorp/aws" || declared[0].Version != "6.60.0" {
		t.Fatalf("declared[0] = %+v, want hashicorp/aws@6.60.0", declared[0])
	}
	if declared[1].Source != "hashicorp/helm" || declared[1].Version != "3.0.2" {
		t.Fatalf("declared[1] = %+v, want hashicorp/helm@3.0.2", declared[1])
	}
	if !declared[0].Schema.HasType("aws_db_instance") || declared[0].Schema.HasType("helm_release") {
		t.Fatalf("declared[0]'s schema doesn't correctly reflect the aws provider's own real types")
	}
	if !declared[1].Schema.HasType("helm_release") || declared[1].Schema.HasType("aws_db_instance") {
		t.Fatalf("declared[1]'s schema doesn't correctly reflect the helm provider's own real types")
	}

	// A second call must reuse the pool's own cached launches, never
	// re-launch either provider -- the same "no double-launching a
	// provider just for inference" property status.go/scanall.go rely on.
	if _, err := declaredProvidersForInference(context.Background(), pool, versions); err != nil {
		t.Fatalf("declaredProvidersForInference (second call): %v", err)
	}
	if len(launches) != 2 {
		t.Fatalf("launches = %v, want exactly 2 (one per provider, never repeated)", launches)
	}
}

// TestDeclaredProvidersForInference_LaunchFailure_Propagates proves a real
// launch failure (bad credentials, unreachable registry -- the same
// condition unreadableProviderUnavailable reports) surfaces as an error
// from declaredProvidersForInference rather than a silently-incomplete
// provider set that would make InferProvider report a false
// ErrUnknownType.
func TestDeclaredProvidersForInference_LaunchFailure_Propagates(t *testing.T) {
	versions := map[string]string{"hashicorp/aws": "6.60.0"}
	pool, err := newProviderPool(nil, versions, nil)
	if err != nil {
		t.Fatalf("newProviderPool: %v", err)
	}
	wantErr := errors.New("dial tcp: connection refused")
	pool.launch = func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error) {
		return nil, nil, wantErr
	}

	if _, err := declaredProvidersForInference(context.Background(), pool, versions); err == nil {
		t.Fatal("expected the launch failure to propagate")
	}
}
