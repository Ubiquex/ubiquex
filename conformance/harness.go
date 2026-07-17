package conformance

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// stateReaderAdapter adapts a provider.Provider to core.StateReader —
// core deliberately doesn't import package provider (see core/scan.go),
// so every consumer that needs both gets its own small copy of this; cli
// has an equivalent (cli/stateadapter.go). Not worth a shared package for
// ~20 lines.
//
// salt (UBI-23, docs/architecture.md -- Secrets) redacts Sensitive
// attributes before core ever sees observed state; source (UBI-24,
// docs/architecture.md -- Sensitive overrides) is the provider's
// registry source, consulted alongside salt for the (source, type)-keyed
// override table -- see cli/stateadapter.go's equivalent fields for the
// full reasoning.
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
	ps, _ := providerSchema.(*provider.Schema)
	return a.p.Configure(ctx, ps, config)
}

func (a stateReaderAdapter) ReadResource(ctx context.Context, resourceSchema any, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	rs, _ := resourceSchema.(*provider.Schema)
	observed, err := a.p.ReadResource(ctx, rs, typeName, currentState)
	if err != nil {
		return nil, err
	}
	if len(observed) == 0 {
		return observed, nil
	}
	return provider.Redact(a.source, typeName, rs.Block, a.salt, observed)
}

// AdoptMutateScanDiffConfig describes one conformance run.
type AdoptMutateScanDiffConfig struct {
	ProviderPath   string          // provider binary to Launch (real, or a fakeprovider fixture)
	Source         string          // provider registry source (e.g. "hashicorp/helm"), for the (source, type)-keyed override table (UBI-24); optional, empty is fine for types with no overrides registered
	Stack          string          // stack name for the generated proposals
	Address        core.Address    // resource address (stack is redundant with Stack above but kept on Address for GenerateProposal's use)
	Lookup         json.RawMessage // the lookup JSON passed to ReadResource, unchanged across all three scans
	ProviderConfig json.RawMessage // passed to Provider.Configure; nil means {}
	Timeout        time.Duration   // per-provider-launch timeout; zero means 60s

	// ProviderEnv adds extra "KEY=VALUE" entries to every launched provider
	// process (provider.WithEnv) — unused by RealSafe types, but how
	// FakeOnly types tell the fakeprovider fixture which resource type/
	// attributes to serve (FAKEPROVIDER_RESOURCE_TYPE/FAKEPROVIDER_ATTRS).
	// Static for the whole run; Mutate is responsible for anything that
	// must differ between the second scan's launch and the first/third
	// (see FAKEPROVIDER_MUTATE_ATTR/FAKEPROVIDER_MUTATE_VALUE, set via
	// os.Setenv from within Mutate itself — each scan launches a fresh
	// subprocess that reads its env at call time, same pattern
	// FAKEPROVIDER_EXTRA_TAG already established).
	ProviderEnv []string

	// Mutate performs an out-of-band change to the real (or fake) resource
	// between the first and second scan — e.g. `aws ec2 create-tags` for a
	// real type, or setting a fakeprovider env var for a FakeOnly one.
	// Must actually change what the next ReadResource call observes, or
	// the harness's "expect ScanDrifted" assertion will (correctly) fail.
	Mutate func(t *testing.T)
}

// RunAdoptMutateScanDiff is the reusable per-type conformance pattern:
// scan (expect new) → accept the adoption → Mutate() → scan again (expect
// drifted) → accept the drift_adopt → scan a third time (expect
// unchanged). This is exactly the manual sequence UBI-7/UBI-8 ran by hand
// against aws_s3_bucket, generalized so "does ubx handle resource type X"
// is a table-driven test instead of a transcript.
func RunAdoptMutateScanDiff(t *testing.T, cfg AdoptMutateScanDiffConfig) {
	t.Helper()

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	providerConfig := cfg.ProviderConfig
	if providerConfig == nil {
		providerConfig = json.RawMessage(`{}`)
	}

	ledger := core.Open(t.TempDir())
	salt, err := ledger.Salt()
	if err != nil {
		t.Fatalf("ledger salt: %v", err)
	}

	scan := func(step string) *core.ScanResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := provider.Launch(ctx, cfg.ProviderPath, provider.WithEnv(cfg.ProviderEnv...))
		if err != nil {
			t.Fatalf("%s: launch provider: %v", step, err)
		}
		defer client.Close()

		res, err := core.RunScan(ctx, stateReaderAdapter{p: client.Provider, salt: salt, source: cfg.Source}, ledger, core.ScanRequest{
			Address:        cfg.Address,
			ProviderConfig: providerConfig,
			CurrentState:   cfg.Lookup,
		})
		if err != nil {
			t.Fatalf("%s: RunScan: %v", step, err)
		}
		return res
	}

	acceptOutcome := func(step string, res *core.ScanResult) *core.Proposal {
		t.Helper()
		proposal, err := core.GenerateProposal(ledger, cfg.Stack, res)
		if err != nil {
			t.Fatalf("%s: GenerateProposal: %v", step, err)
		}
		accepted, err := core.Accept(ledger, proposal)
		if err != nil {
			t.Fatalf("%s: Accept: %v", step, err)
		}
		return accepted
	}

	res1 := scan("scan 1 (adopt)")
	if res1.Outcome != core.ScanNew {
		t.Fatalf("scan 1: Outcome = %v, want ScanNew (is %s already tracked from a prior run?)", res1.Outcome, cfg.Address)
	}
	acceptOutcome("scan 1 (adopt)", res1)

	if cfg.Mutate != nil {
		cfg.Mutate(t)
	}

	res2 := scan("scan 2 (drift)")
	if res2.Outcome != core.ScanDrifted {
		t.Fatalf("scan 2: Outcome = %v, want ScanDrifted — Mutate() didn't produce an observable change for %s", res2.Outcome, cfg.Address)
	}
	drifted := acceptOutcome("scan 2 (drift)", res2)
	if drifted.Kind != core.KindDriftAdopt {
		t.Fatalf("scan 2: Kind = %q, want %q", drifted.Kind, core.KindDriftAdopt)
	}

	res3 := scan("scan 3 (settled)")
	if res3.Outcome != core.ScanUnchanged {
		t.Fatalf("scan 3: Outcome = %v, want ScanUnchanged", res3.Outcome)
	}
}

// RequireLive skips t unless UBX_CONFORMANCE_LIVE=1 is set — RealSafe
// conformance tests touch a real backend (an AWS or GCP account, or a
// real/local Kubernetes cluster, UBI-22 — a real, if cheap/safe, network
// round trip and/or a real mutation) and must never run as part of a
// default `go test ./...`, which stays hermetic and credential-free
// everywhere else in this project. Explicit opt-in only.
func RequireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run conformance tests against a real backend (AWS/GCP account or Kubernetes cluster)")
	}
}
