package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/ubiquex/ubiquex/core/executor"
)

// fakeCloser counts Close calls -- providerPool_test.go's own stand-in for
// a real *provider.Client, since a hermetic test never launches one.
type fakeCloser struct{ closed int }

func (f *fakeCloser) Close() error { f.closed++; return nil }

// fakeApplier is a bare-minimum executor.Applier -- providerPool's own
// tests never actually call any of its methods, only compare identity, so
// there's nothing to script.
type fakeApplierStub struct{ executor.Applier }

// newTestPool builds a providerPool with a fake launchFunc that records
// every (source, version) it was asked to launch and returns a distinct
// Applier + fakeCloser each time -- lets tests assert exactly how many
// times a real launch would have happened, not just the end state.
func newTestPool(t *testing.T, versions map[string]string, configs map[string]map[string]any) (*providerPool, *[]string) {
	t.Helper()
	pp, err := newProviderPool(nil, versions, configs)
	if err != nil {
		t.Fatalf("newProviderPool: %v", err)
	}
	var launches []string
	pp.launch = func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error) {
		launches = append(launches, source+"@"+version)
		return fakeApplierStub{}, &fakeCloser{}, nil
	}
	return pp, &launches
}

func TestProviderPool_LazyLaunch_CachedOnSecondGet(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"hashicorp/aws": "6.60.0"}, nil)

	app1, _, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	app2, _, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err != nil {
		t.Fatalf("Get (second): %v", err)
	}
	if app1 != app2 {
		t.Fatalf("Get returned two different Appliers for the same source@version -- want the identical cached instance")
	}
	if len(*launches) != 1 {
		t.Fatalf("launch called %d times, want exactly 1 -- lazy, launched once, reused", len(*launches))
	}
}

func TestProviderPool_ReturnsPerSourceConfig(t *testing.T) {
	pool, _ := newTestPool(t,
		map[string]string{"hashicorp/aws": "6.60.0", "hashicorp/helm": "3.0.2"},
		map[string]map[string]any{
			"hashicorp/aws":  {"region": "us-east-1"},
			"hashicorp/helm": {"kubeconfig": "~/.kube/config"},
		},
	)

	_, awsConfig, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err != nil {
		t.Fatalf("Get(aws): %v", err)
	}
	_, helmConfig, err := pool.Get(context.Background(), "hashicorp/helm", "3.0.2")
	if err != nil {
		t.Fatalf("Get(helm): %v", err)
	}
	var awsM, helmM map[string]any
	json.Unmarshal(awsConfig, &awsM)
	json.Unmarshal(helmConfig, &helmM)
	if awsM["region"] != "us-east-1" {
		t.Fatalf("aws config = %s, want region=us-east-1", awsConfig)
	}
	if helmM["kubeconfig"] != "~/.kube/config" {
		t.Fatalf("helm config = %s, want kubeconfig=~/.kube/config", helmConfig)
	}
	// AWS's own config must never leak into helm's, or vice versa -- the
	// exact gap this whole session exists to close.
	if _, ok := awsM["kubeconfig"]; ok {
		t.Fatalf("aws config = %s, contains helm's own kubeconfig key -- cross-contamination", awsConfig)
	}
}

func TestProviderPool_MissingConfigDefaultsToEmptyObject(t *testing.T) {
	pool, _ := newTestPool(t, map[string]string{"hashicorp/aws": "6.60.0"}, nil)
	_, config, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(config) != "{}" {
		t.Fatalf("config = %s, want {} (a declared source with no [provider_configs] entry) -- the same default --provider-config already uses", config)
	}
}

func TestProviderPool_UndeclaredSource_Refused(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"hashicorp/aws": "6.60.0"}, nil)
	_, _, err := pool.Get(context.Background(), "hashicorp/gcp", "5.0.0")
	if err == nil {
		t.Fatal("expected an error for a source this stack never declared")
	}
	if len(*launches) != 0 {
		t.Fatalf("launch called %d times, want 0 -- an undeclared source must never even attempt a launch", len(*launches))
	}
}

// TestProviderPool_VersionMismatch_Refused proves docs/architecture.md's
// own "explicit pins only" rule holds at ship time, not just at resolve
// time: a proposal recorded against a version this stack's config no
// longer pins is refused, never silently launched against whatever IS
// currently pinned -- the proposal was signed against a specific version,
// and executing a different one than what was reviewed is exactly the
// kind of silent drift this project exists to catch.
func TestProviderPool_VersionMismatch_Refused(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"hashicorp/aws": "6.61.0"}, nil)
	_, _, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err == nil {
		t.Fatal("expected an error for a version no longer matching this stack's own current pin")
	}
	if len(*launches) != 0 {
		t.Fatalf("launch called %d times, want 0 -- a version mismatch must never launch anything at all", len(*launches))
	}
}

// TestProviderPool_VersionEmpty_UsesPinned covers Ship's own drift_revert
// dispatch (core/executor: pool.Get(ctx, providerSource, "")) -- a
// version-empty request means "whatever this stack currently pins,"
// never a real request for "any version."
func TestProviderPool_VersionEmpty_UsesPinned(t *testing.T) {
	pool, launches := newTestPool(t, map[string]string{"hashicorp/aws": "6.60.0"}, nil)
	if _, _, err := pool.Get(context.Background(), "hashicorp/aws", ""); err != nil {
		t.Fatalf("Get with empty version: %v", err)
	}
	if len(*launches) != 1 || (*launches)[0] != "hashicorp/aws@6.60.0" {
		t.Fatalf("launches = %v, want exactly [hashicorp/aws@6.60.0]", *launches)
	}
}

func TestProviderPool_LaunchFailure_Propagates(t *testing.T) {
	pool, err := newProviderPool(nil, map[string]string{"hashicorp/aws": "6.60.0"}, nil)
	if err != nil {
		t.Fatalf("newProviderPool: %v", err)
	}
	wantErr := fmt.Errorf("dial tcp: connection refused")
	pool.launch = func(ctx context.Context, source, version string) (executor.Applier, io.Closer, error) {
		return nil, nil, wantErr
	}
	_, _, err = pool.Get(context.Background(), "hashicorp/aws", "6.60.0")
	if err == nil {
		t.Fatal("expected the launch failure to propagate")
	}
}

func TestProviderPool_Close_ClosesOnlyLaunchedClients(t *testing.T) {
	pool, _ := newTestPool(t,
		map[string]string{"hashicorp/aws": "6.60.0", "hashicorp/helm": "3.0.2", "hashicorp/kubernetes": "2.35.1"},
		nil,
	)
	// Only two of the three declared providers are actually used.
	if _, _, err := pool.Get(context.Background(), "hashicorp/aws", "6.60.0"); err != nil {
		t.Fatalf("Get(aws): %v", err)
	}
	if _, _, err := pool.Get(context.Background(), "hashicorp/helm", "3.0.2"); err != nil {
		t.Fatalf("Get(helm): %v", err)
	}

	if len(pool.closers) != 2 {
		t.Fatalf("closers = %d, want 2 -- kubernetes was declared but never used, never launched, never needs closing", len(pool.closers))
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i, c := range pool.closers {
		if c.(*fakeCloser).closed != 1 {
			t.Fatalf("closer %d was closed %d times, want exactly 1", i, c.(*fakeCloser).closed)
		}
	}
}
