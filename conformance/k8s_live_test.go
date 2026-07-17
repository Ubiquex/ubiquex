package conformance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// k8sProviderVersion/helmProviderVersion are pinned, not "latest" (see
// docs/architecture.md -- provider acquisition, UBI-8: explicit version
// pins only).
const (
	k8sProviderVersion  = "2.35.1"
	helmProviderVersion = "2.17.0"
)

func realK8sProviderPath(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/kubernetes")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := provider.Acquire(ctx, src, k8sProviderVersion)
	if err != nil {
		t.Fatalf("acquire real kubernetes provider: %v", err)
	}
	return result.Path
}

func realHelmProviderPath(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/helm")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := provider.Acquire(ctx, src, helmProviderVersion)
	if err != nil {
		t.Fatalf("acquire real helm provider: %v", err)
	}
	return result.Path
}

// requireKubeContext skips (doesn't fail) unless kubectl already resolves
// a current context -- this test needs SOME cluster to exist (a real EKS
// cluster, or the free/local/disposable `kind create cluster` this
// project's own Stage 2 session used), the same "skip on a missing
// account-level precondition, don't fail" posture requireDefaultVPCID
// already establishes for AWS. ubx itself never creates or destroys
// clusters; that's the operator's own tooling, same as every other
// conformance precondition in this file.
func requireKubeContext(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("kubectl", "config", "current-context").Output()
	ctx := strings.TrimSpace(string(out))
	if err != nil || ctx == "" {
		t.Skip("no kubeconfig current-context configured -- set one up (e.g. `kind create cluster`) to run this test")
	}
	return ctx
}

// kubeconfigProviderConfig builds the hashicorp/kubernetes provider's
// config (config_path/config_context) for kubeContext -- the same
// $HOME/.kube/config every kubectl invocation already reads.
func kubeconfigProviderConfig(kubeContext string) json.RawMessage {
	return MustMarshal(map[string]string{
		"config_path":    os.Getenv("HOME") + "/.kube/config",
		"config_context": kubeContext,
	})
}

// helmKubeconfigProviderConfig is the same thing for hashicorp/helm,
// whose "kubernetes" config lives inside a nested (NestingList) block --
// confirmed directly against the real provider schema, not assumed (see
// docs/architecture.md's "Kubernetes support" section).
func helmKubeconfigProviderConfig(kubeContext string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"kubernetes": []map[string]string{{
			"config_path":    os.Getenv("HOME") + "/.kube/config",
			"config_context": kubeContext,
		}},
	})
	return b
}

func runKubectl(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl %v: %v\n%s", args, err, out)
	}
}

// TestConformance_KubernetesConfigMap confirms the real, empirically
// simpler lookup shape Stage 2 found: {"id": "<namespace>/<name>"} alone
// -- never the {"metadata": [{"name":..., "namespace":...}]} shape
// metadata's own NestingList schema modeling would otherwise suggest is
// required (docs/architecture.md's "Kubernetes support" §1).
func TestConformance_KubernetesConfigMap(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realK8sProviderPath(t)
	ns := uniqueName("ubx-conf")

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })
	runKubectl(t, "create", "configmap", "app-config", "--from-literal=env=prod", "-n", ns)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "kubernetes_config_map_v1", Name: "app-config"},
		Lookup:         MustMarshal(map[string]string{"id": ns + "/app-config"}),
		ProviderConfig: kubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			runKubectl(t, "patch", "configmap", "app-config", "-n", ns, "--type", "merge", "-p", `{"data":{"env":"staging"}}`)
		},
	})
}

// TestConformance_KubernetesSecret is UBI-22/UBI-23's core cross-check:
// kubernetes_secret_v1.data/binary_data are confirmed Sensitive in the
// real schema, and this proves the whole adopt->drift->settle cycle
// still works correctly over the resulting $redacted values, against a
// real cluster -- not just the fakeprovider-fixture coverage in
// cli/redact_test.go. (The "grep the generated proposal file for zero
// real secret material" leg of this check was performed by hand this
// session, see STATE.md for the transcript -- RunAdoptMutateScanDiff's
// own ledger is a private t.TempDir(), not exposed for post-hoc file
// inspection from here.)
func TestConformance_KubernetesSecret(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realK8sProviderPath(t)
	ns := uniqueName("ubx-conf")

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })
	runKubectl(t, "create", "secret", "generic", "app-secret", "-n", ns, "--from-literal=password=initial-secret")

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "kubernetes_secret_v1", Name: "app-secret"},
		Lookup:         MustMarshal(map[string]string{"id": ns + "/app-secret"}),
		ProviderConfig: kubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			runKubectl(t, "patch", "secret", "app-secret", "-n", ns, "--type", "merge", "-p", `{"stringData":{"password":"rotated-secret"}}`)
		},
	})
}

// TestConformance_KubernetesDeployment confirms a replica-count drift on
// a real workload type -- also exercises spec's own NestingList shape.
func TestConformance_KubernetesDeployment(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realK8sProviderPath(t)
	ns := uniqueName("ubx-conf")

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })
	runKubectl(t, "create", "deployment", "web", "--image=nginx:1.25", "--replicas=2", "-n", ns)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "kubernetes_deployment_v1", Name: "web"},
		Lookup:         MustMarshal(map[string]string{"id": ns + "/web"}),
		ProviderConfig: kubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			runKubectl(t, "scale", "deployment", "web", "-n", ns, "--replicas=3")
		},
	})
}

// TestConformance_KubernetesService confirms a label drift on a
// networking type.
func TestConformance_KubernetesService(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realK8sProviderPath(t)
	ns := uniqueName("ubx-conf")

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })
	runKubectl(t, "create", "deployment", "web", "--image=nginx:1.25", "-n", ns)
	runKubectl(t, "expose", "deployment", "web", "-n", ns, "--port=80", "--target-port=80")

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "kubernetes_service_v1", Name: "web"},
		Lookup:         MustMarshal(map[string]string{"id": ns + "/web"}),
		ProviderConfig: kubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			runKubectl(t, "label", "service", "web", "-n", ns, "env=prod", "--overwrite")
		},
	})
}

// TestConformance_KubernetesNamespace confirms the cluster-scoped id
// shape (bare name, no namespace prefix -- §1's other finding).
func TestConformance_KubernetesNamespace(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realK8sProviderPath(t)
	ns := uniqueName("ubx-conf")

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "kubernetes_namespace_v1", Name: ns},
		Lookup:         MustMarshal(map[string]string{"id": ns}),
		ProviderConfig: kubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			runKubectl(t, "label", "namespace", ns, "team=platform", "--overwrite")
		},
	})
}

// TestConformance_HelmRelease confirms helm_release's own, flatter
// identity shape ({"id", "name", "namespace"} all three together --
// unlike every kubernetes_* type above, "id" alone is NOT sufficient),
// and that a real `helm upgrade --set` values change is detected via
// metadata[0].values -- the field that actually carries a values drift,
// since the top-level values/chart attributes are write-only config the
// provider never backfills from a live read (docs/architecture.md's
// "Kubernetes support" §4).
func TestConformance_HelmRelease(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realHelmProviderPath(t)
	ns := uniqueName("ubx-conf")
	chartDir := t.TempDir()

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })

	if out, err := exec.Command("helm", "create", chartDir+"/testchart").CombinedOutput(); err != nil {
		t.Fatalf("helm create: %v\n%s", err, out)
	}
	install := exec.Command("helm", "install", "myrelease", chartDir+"/testchart", "-n", ns, "--set", "replicaCount=1")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("helm install: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("helm", "uninstall", "myrelease", "-n", ns).Run() })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/helm",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "helm_release", Name: "myrelease"},
		Lookup:         MustMarshal(map[string]string{"id": "myrelease", "name": "myrelease", "namespace": ns}),
		ProviderConfig: helmKubeconfigProviderConfig(kubeContext),
		Mutate: func(t *testing.T) {
			upgrade := exec.Command("helm", "upgrade", "myrelease", chartDir+"/testchart", "-n", ns, "--set", "replicaCount=3")
			if out, err := upgrade.CombinedOutput(); err != nil {
				t.Fatalf("helm upgrade: %v\n%s", err, out)
			}
		},
	})
}

// TestConformance_HelmReleaseSensitiveOverride is UBI-24's own live
// cross-check: a real Helm release carrying a secret-looking value
// (standing in for a real terraform-provider-helm set_sensitive value --
// what actually lands in the release's own resolved values is identical
// either way, since Terraform's set_sensitive only hides the value from
// Terraform's own plan output, not from what Helm itself stores) must
// adopt with metadata.values/metadata.notes/manifest redacted via
// provider/overrides.go's table -- schema Sensitive flags alone (UBI-23)
// would have missed this entirely, the exact gap UBI-22 found and UBI-24
// closes. Uses its own ledger directory (not RunAdoptMutateScanDiff's
// private t.TempDir()) specifically so the generated proposal file can
// be grepped for zero real material, both on adopt and after a real
// values change (the drift path) -- the literal "grep the proposal file"
// requirement this ticket names explicitly.
func TestConformance_HelmReleaseSensitiveOverride(t *testing.T) {
	RequireLive(t)
	kubeContext := requireKubeContext(t)
	providerPath := realHelmProviderPath(t)
	ns := uniqueName("ubx-conf")
	chartDir := t.TempDir()
	ledgerDir := t.TempDir()
	const secretV1 = "s3ns1t1ve-helm-v4lue"
	const secretV2 = "r0tated-helm-s3cr3t-v2"

	runKubectl(t, "create", "namespace", ns)
	t.Cleanup(func() { runKubectl(t, "delete", "namespace", ns, "--ignore-not-found") })

	if out, err := exec.Command("helm", "create", chartDir+"/testchart").CombinedOutput(); err != nil {
		t.Fatalf("helm create: %v\n%s", err, out)
	}
	install := exec.Command("helm", "install", "myrelease", chartDir+"/testchart", "-n", ns,
		"--set", "replicaCount=1", "--set-string", "dbPassword="+secretV1)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("helm install: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("helm", "uninstall", "myrelease", "-n", ns).Run() })

	providerConfig := helmKubeconfigProviderConfig(kubeContext)
	lookup := MustMarshal(map[string]string{"id": "myrelease", "name": "myrelease", "namespace": ns})
	ledger := core.Open(ledgerDir)

	scan := func(step string) *core.ScanResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		client, err := provider.Launch(ctx, providerPath)
		if err != nil {
			t.Fatalf("%s: launch provider: %v", step, err)
		}
		defer client.Close()
		salt, err := ledger.Salt()
		if err != nil {
			t.Fatalf("%s: ledger salt: %v", step, err)
		}
		res, err := core.RunScan(ctx, stateReaderAdapter{p: client.Provider, salt: salt, source: "hashicorp/helm"}, ledger, core.ScanRequest{
			Address:        core.Address{Stack: "conformance", Type: "helm_release", Name: "myrelease"},
			ProviderConfig: providerConfig,
			CurrentState:   lookup,
		})
		if err != nil {
			t.Fatalf("%s: RunScan: %v", step, err)
		}
		return res
	}
	acceptAndWriteProposal := func(step string, res *core.ScanResult) string {
		t.Helper()
		proposal, err := core.GenerateProposal(ledger, "conformance", res)
		if err != nil {
			t.Fatalf("%s: GenerateProposal: %v", step, err)
		}
		b, err := json.MarshalIndent(proposal, "", "  ")
		if err != nil {
			t.Fatalf("%s: marshal: %v", step, err)
		}
		path := filepath.Join(ledgerDir, step+".json")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("%s: write: %v", step, err)
		}
		if _, err := core.Accept(ledger, proposal); err != nil {
			t.Fatalf("%s: Accept: %v", step, err)
		}
		return path
	}
	requireNoMaterial := func(path string, secrets ...string) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if strings.Contains(string(raw), s) {
				t.Fatalf("proposal file %s leaked real secret material %q:\n%s", path, s, raw)
			}
		}
		if !strings.Contains(string(raw), "$redacted") {
			t.Fatalf("expected proposal file %s to contain a $redacted marker, got:\n%s", path, raw)
		}
	}

	res1 := scan("adopt")
	if res1.Outcome != core.ScanNew {
		t.Fatalf("scan 1: Outcome = %v, want ScanNew", res1.Outcome)
	}
	adoptPath := acceptAndWriteProposal("adopt", res1)
	requireNoMaterial(adoptPath, secretV1, secretV2)

	upgrade := exec.Command("helm", "upgrade", "myrelease", chartDir+"/testchart", "-n", ns,
		"--set", "replicaCount=1", "--set-string", "dbPassword="+secretV2)
	if out, err := upgrade.CombinedOutput(); err != nil {
		t.Fatalf("helm upgrade: %v\n%s", err, out)
	}

	res2 := scan("drift")
	if res2.Outcome != core.ScanDrifted {
		t.Fatalf("scan 2: Outcome = %v, want ScanDrifted", res2.Outcome)
	}
	driftPath := acceptAndWriteProposal("drift", res2)
	requireNoMaterial(driftPath, secretV1, secretV2)
}
