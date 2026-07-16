package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// TestNewAttributionBackend_K8sNotConfigured and
// TestNewAttributionBackend_HelmNotConfigured are UBI-22's core dispatch
// adversarial case: a kubernetes_*/helm_release drift with no
// .ubx/config [k8s_audit] table must produce errK8sAuditNotConfigured --
// never attempt a real AWS client construction, never block. Both direct
// (not CLI-level) since --provider/--source are mutually exclusive at
// the flag layer, and this dispatch is keyed by ProviderSource, which
// only --source ever populates (which would force a real network
// provider acquire) -- see docs/architecture.md's "Kubernetes support"
// §3 for the full reasoning.
func TestNewAttributionBackend_K8sNotConfigured(t *testing.T) {
	_, backend, closeFn, err := newAttributionBackend(context.Background(), "hashicorp/kubernetes", nil, K8sAuditConfig{})
	if !errors.Is(err, errK8sAuditNotConfigured) {
		t.Fatalf("expected errK8sAuditNotConfigured, got %v", err)
	}
	if backend.Name != "k8s_audit_logs" {
		t.Fatalf("expected k8saudit.Backend, got %+v", backend)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn must be a safe no-op even on error, got %v", err)
	}
}

func TestNewAttributionBackend_HelmNotConfigured(t *testing.T) {
	_, backend, _, err := newAttributionBackend(context.Background(), "hashicorp/helm", nil, K8sAuditConfig{})
	if !errors.Is(err, errK8sAuditNotConfigured) {
		t.Fatalf("expected errK8sAuditNotConfigured for helm, got %v", err)
	}
	if backend.Name != "k8s_audit_logs" {
		t.Fatalf("expected k8saudit.Backend, got %+v", backend)
	}
}

// TestNewAttributionBackend_K8sConfiguredDispatchesToK8sAudit confirms
// the opposite path: once [k8s_audit].cluster is set, dispatch
// constructs a real k8saudit.Client (no network call happens at
// construction time -- config.LoadDefaultConfig only resolves config,
// same as cloudtrail.New/gcpaudit.New, confirmed hermetic by
// TestScan_AttributionDegradesGracefully_NoCredentials's own existing
// precedent).
func TestNewAttributionBackend_K8sConfiguredDispatchesToK8sAudit(t *testing.T) {
	lookup, backend, closeFn, err := newAttributionBackend(context.Background(), "hashicorp/kubernetes", nil, K8sAuditConfig{
		Cluster: "test-cluster", Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("expected no error constructing the client, got %v", err)
	}
	if lookup == nil {
		t.Fatal("expected a non-nil core.EventLookup")
	}
	if backend.SuccessKind != "k8s_audit" {
		t.Fatalf("expected k8s_audit success kind, got %+v", backend)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}
}

// TestAttributeDrift_K8sNotConfigured_RecordsNotConfiguredReason confirms
// attributeDrift's own errors.Is dispatch: an unconfigured k8s_audit
// backend must record audit_unattributed/not_configured -- distinct from
// the generic not_logged every other newAttributionBackend failure
// produces -- and must never block (no error returned, no panic; the
// function has no return value at all, its whole contract is "append to
// proposal.Intent.Sources and return").
func TestAttributeDrift_K8sNotConfigured_RecordsNotConfiguredReason(t *testing.T) {
	ledger := core.Open(t.TempDir())
	addr := core.Address{Stack: "s", Type: "kubernetes_deployment_v1", Name: "web"}
	res := &core.ScanResult{Address: addr, Observed: json.RawMessage(`{"id":"default/web"}`), ObservedHash: "deadbeef"}
	proposal := &core.Proposal{Resolution: core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)}}

	attributeDrift(context.Background(), ledger, addr, res, proposal, json.RawMessage(`{}`), "hashicorp/kubernetes", K8sAuditConfig{})

	if len(proposal.Intent.Sources) != 1 {
		t.Fatalf("expected exactly one intent source, got %d: %+v", len(proposal.Intent.Sources), proposal.Intent.Sources)
	}
	src := proposal.Intent.Sources[0]
	if src.Kind != "audit_unattributed" {
		t.Errorf("Kind = %q, want audit_unattributed", src.Kind)
	}
	if src.Reason != core.ReasonNotConfigured {
		t.Errorf("Reason = %q, want %q", src.Reason, core.ReasonNotConfigured)
	}
	if src.Backend != "k8s_audit_logs" {
		t.Errorf("Backend = %q, want k8s_audit_logs", src.Backend)
	}
}

// TestAttributeDrift_HelmNotConfigured_RecordsNotConfiguredReason is the
// same case for helm_release -- both providers dispatch through the same
// switch case in newAttributionBackend, but this is a distinct source
// string, worth its own regression check.
func TestAttributeDrift_HelmNotConfigured_RecordsNotConfiguredReason(t *testing.T) {
	ledger := core.Open(t.TempDir())
	addr := core.Address{Stack: "s", Type: "helm_release", Name: "myrelease"}
	res := &core.ScanResult{Address: addr, Observed: json.RawMessage(`{"id":"myrelease"}`), ObservedHash: "deadbeef"}
	proposal := &core.Proposal{Resolution: core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)}}

	attributeDrift(context.Background(), ledger, addr, res, proposal, json.RawMessage(`{}`), "hashicorp/helm", K8sAuditConfig{})

	if len(proposal.Intent.Sources) != 1 {
		t.Fatalf("expected exactly one intent source, got %d: %+v", len(proposal.Intent.Sources), proposal.Intent.Sources)
	}
	if proposal.Intent.Sources[0].Reason != core.ReasonNotConfigured {
		t.Fatalf("Reason = %q, want %q", proposal.Intent.Sources[0].Reason, core.ReasonNotConfigured)
	}
}

// TestNewAttributionBackend_AWSStillDefaultsToCloudTrail is a regression
// guard: adding the hashicorp/kubernetes/hashicorp/helm cases must not
// perturb the existing AWS-or-unrecognized default path (an EKS
// cluster's own aws_eks_cluster drift, scanned via --source hashicorp/aws,
// must stay exactly as CloudTrail-attributable as before -- see
// docs/architecture.md's "Kubernetes support" §3 on why dispatch is by
// ProviderSource, not by resource type).
func TestNewAttributionBackend_AWSStillDefaultsToCloudTrail(t *testing.T) {
	_, backend, _, _ := newAttributionBackend(context.Background(), "hashicorp/aws", nil, K8sAuditConfig{})
	if backend.SuccessKind != "cloudtrail" {
		t.Fatalf("expected cloudtrail.Backend for hashicorp/aws, got %+v", backend)
	}
}
