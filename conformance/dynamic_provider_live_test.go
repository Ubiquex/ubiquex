// UBI-158 Phase 5: the conformance gate applied to a Dynamic Provider
// (ubx-provider-dynamic, a separate repo, github.com/Ubiquex/ubx-provider-dynamic)
// -- wiring this project's own existing, real adversarial conformance
// harness (UBI-50/UBI-58) against a schema-derived, generic provider
// binary instead of a hand-written one, so trust for it is earned by
// passing the identical real, falsifiable probe suite every real
// HashiCorp provider is held to, not assumed because a schema parsed
// cleanly.
//
// Real, deliberate design choice: no harness code changed to accommodate
// this (destroy_probe.go/harness.go/live_probe.go's own real
// AdoptMutateScanDiffConfig/DestroyProbeConfig/LiveReadProbeConfig already
// took nothing but a plain binary path string -- see this session's own
// research). The one real, additive gap filled was provider.WithDir
// (provider/client.go): ubx-provider-dynamic reads its own config from
// ".ubx/config" relative to its own process cwd at startup (its own
// internal/config doc comment explains why this can't wait for
// ConfigureProvider), which no existing conformance caller needed before
// since every prior probe target (a real HashiCorp binary) reads
// ambient env/CLI credentials instead.
//
// Real create/destroy cycles against real external services (a GitHub
// repo, an AWS SQS queue), confirmed explicitly with the founder before
// this file was written -- the identical bar destroy_probe_live_test.go's
// own UBI-58 precedent already set for probe 3 against real AWS/GCP, not
// inferred from this ticket's own text alone.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

var (
	dynamicProviderBinaryOnce sync.Once
	dynamicProviderBinaryPath string
	dynamicProviderBinaryErr  error
)

// dynamicProviderRepo locates a local ubx-provider-dynamic checkout --
// UBX_PROVIDER_DYNAMIC_REPO if set, "../../ubx-provider-dynamic" (this
// monorepo's own real sibling checkout location -- go test's own cwd for
// this package is conformance/ itself, and ubx-provider-dynamic lives
// beside ubiquex, not inside it: .../Ubiquex/ubiquex/conformance ->
// .../Ubiquex/ubx-provider-dynamic -- confirmed present this session)
// otherwise.
func dynamicProviderRepo() string {
	if v := os.Getenv("UBX_PROVIDER_DYNAMIC_REPO"); v != "" {
		return v
	}
	return "../../ubx-provider-dynamic"
}

// dynamicProviderBinary builds ubx-provider-dynamic once (real `go build`
// against its own, separate module -- it is a different Go module from
// this one, so this cannot be a plain relative-package build the way
// fake_test.go's own fakeProviderBinary is) and reuses the compiled
// binary for every real conformance case in this file, mirroring
// fake_test.go's own TestMain-built fakeProviderBinary pattern as closely
// as a cross-module build allows (a sync.Once here rather than a second
// TestMain, since this package already has one).
func dynamicProviderBinary(t *testing.T) string {
	t.Helper()
	dynamicProviderBinaryOnce.Do(func() {
		repo := dynamicProviderRepo()
		if _, err := os.Stat(filepath.Join(repo, "go.mod")); err != nil {
			dynamicProviderBinaryErr = fmt.Errorf("ubx-provider-dynamic checkout not found at %q (set UBX_PROVIDER_DYNAMIC_REPO): %w", repo, err)
			return
		}
		dir, err := os.MkdirTemp("", "ubx-conformance-dynamic-provider")
		if err != nil {
			dynamicProviderBinaryErr = err
			return
		}
		bin := filepath.Join(dir, "ubx-provider-dynamic")
		build := exec.Command("go", "build", "-o", bin, "./cmd/ubx-provider-dynamic")
		build.Dir = repo
		out, err := build.CombinedOutput()
		if err != nil {
			dynamicProviderBinaryErr = fmt.Errorf("building ubx-provider-dynamic from %s: %w\n%s", repo, err, out)
			return
		}
		dynamicProviderBinaryPath = bin
	})
	if dynamicProviderBinaryErr != nil {
		t.Fatalf("dynamicProviderBinary: %v", dynamicProviderBinaryErr)
	}
	return dynamicProviderBinaryPath
}

// writeDynamicConfig writes a real .ubx/config into dir -- ubx-provider-dynamic
// reads this from its own process cwd at startup (provider.WithDir's own
// doc comment explains why), never from an env var or CLI flag.
func writeDynamicConfig(t *testing.T, dir, toml string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "config"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- GitHub: all four probes against a real, disposable private repo ---

const githubDynamicConfig = `
[dynamic_providers.github]
schema_source = "openapi"
schema_url = "https://raw.githubusercontent.com/github/rest-api-description/main/descriptions/api.github.com/api.github.com.json"
base_url = "https://api.github.com"

[dynamic_providers.github.auth]
type = "api_key_header"
[[dynamic_providers.github.auth.params.headers]]
name = "Authorization"
value_env = "GITHUB_TOKEN"
value_prefix = "Bearer "
`

// TestConformance_DynamicProvider_GitHub_AllProbes runs all four real
// conformance probes against github_full_repository -- the Dynamic
// Provider's own schema-derived resource type for a real GitHub
// repository, served by ubx-provider-dynamic's OpenAPI-sourced path
// (UBI-158 Phase 1), never hashicorp/github (a real, different, hand-
// written provider this project has no dependency on).
func TestConformance_DynamicProvider_GitHub_AllProbes(t *testing.T) {
	RequireLive(t)
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set")
	}
	binPath := dynamicProviderBinary(t)
	workDir := t.TempDir()
	writeDynamicConfig(t, workDir, githubDynamicConfig)

	owner := ghLogin(t)
	repoName := uniqueName("ubx-conformance-dynprov-github")
	createGitHubRepo(t, repoName)
	t.Cleanup(func() { deleteGitHubRepo(t, owner, repoName) })

	baseCfg := LiveReadProbeConfig{
		ProviderPath:   binPath,
		ProviderDir:    workDir,
		Source:         "ubx-provider-dynamic/github",
		Version:        "dynamic",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "github_full_repository", Name: repoName},
		ProviderConfig: json.RawMessage(`{}`),
		Timeout:        30 * time.Second,
		ProviderEnv:    []string{"UBX_DYNAMIC_PROVIDER_NAME=github", "GITHUB_TOKEN=" + token},
	}
	lookup := MustMarshal(map[string]string{"owner_path": owner, "repo": repoName})

	// Probe 1 (identity-shape, live): github_full_repository's own real
	// lookup key (owner_path+repo) is not decomposable into a smaller
	// "minimal" subset that still resolves anything -- both are strictly
	// required to build the request at all -- so lookupFull ==
	// lookupMinimal here, the documented "nothing to compare" case
	// (ProbeIdentityShapeLive's own doc comment), an honest null result
	// rather than a fabricated comparison. A real Finding here IS a real
	// bug (a silent incomplete read), unlike probe 4 below -- worth
	// failing the test on.
	t.Run("probe1_identity_shape", func(t *testing.T) {
		if finding, err := ProbeIdentityShapeLive(context.Background(), baseCfg, lookup, lookup); err != nil {
			t.Fatalf("ProbeIdentityShapeLive: %v", err)
		} else if finding != nil {
			t.Errorf("real incomplete-read finding against github_full_repository: %+v", finding)
		} else {
			t.Log("no finding")
		}
	})

	// Probe 2 (sensitive echo, live): plant a unique marker in the real,
	// free-form "description" field (via a real PATCH), then check every
	// OTHER attribute for a verbatim echo. A real Finding here is a real,
	// security-relevant bug -- worth failing the test on.
	marker := "ubx-conformance-marker-" + uniqueName("m")
	updateGitHubRepoDescription(t, owner, repoName, marker)
	t.Run("probe2_sensitive_echo", func(t *testing.T) {
		if finding, err := ProbeSensitiveEchoLive(context.Background(), baseCfg, lookup, marker, "description"); err != nil {
			t.Fatalf("ProbeSensitiveEchoLive: %v", err)
		} else if finding != nil {
			t.Errorf("real sensitive-echo finding against github_full_repository: %+v", finding)
		} else {
			t.Log("no finding")
		}
	})

	// Probe 4 (drift-detectability, live): adopt, then rescan with zero
	// mutation -- separate ledger from probe 3's own adopt (RunScan
	// itself refuses to re-adopt an address the ledger already tracks).
	// A real Finding here is NOT necessarily a bug -- docs/conformance-harness.md's
	// own design treats "structurally undriftable" as a legitimate,
	// reportable TYPE CLASSIFICATION (the real hashicorp/time precedent),
	// not a correctness failure -- so this only fails the test on an
	// unexpected ERROR (broken plumbing), always logging whatever real
	// outcome (Finding or none) actually occurred.
	t.Run("probe4_drift_detectability", func(t *testing.T) {
		driftLedger := core.Open(t.TempDir())
		finding, err := ProbeDriftLive(context.Background(), driftLedger, baseCfg, lookup)
		if err != nil {
			t.Fatalf("ProbeDriftLive: %v", err)
		}
		if finding != nil {
			t.Logf("real undriftable finding (not necessarily a bug, see docs/conformance-harness.md): %+v", finding)
		} else {
			t.Log("no finding")
		}
	})

	// Probe 3 (destroy honesty, live): its own, separate ledger (it
	// performs its own real adopt internally) -- a real destroy through
	// core/executor.Ship, independently re-verified against real GitHub
	// afterward. Requires the real "delete_repo" OAuth scope on
	// GITHUB_TOKEN -- most default gh CLI tokens don't carry it (a real,
	// GitHub-specific permission model fact, not a ubx-provider-dynamic
	// concern) -- skipped with a clear reason if absent, rather than
	// misreporting a real 403-caused non-destroy as either a pass or a
	// lie.
	t.Run("probe3_destroy_honesty", func(t *testing.T) {
		if !githubTokenHasScope(t, token, "delete_repo") {
			t.Skip("GITHUB_TOKEN lacks the delete_repo scope -- cannot distinguish a real destroy-honesty result from an ordinary permission failure; run `gh auth refresh -h github.com -s delete_repo` and re-export GITHUB_TOKEN to exercise this probe")
		}
		destroyLedger := core.Open(t.TempDir())
		finding, err := ProbeDestroyHonesty(context.Background(), destroyLedger, DestroyProbeConfig{
			ProviderPath:   binPath,
			ProviderDir:    workDir,
			Source:         "ubx-provider-dynamic/github",
			Version:        "dynamic",
			Stack:          "conformance",
			Address:        core.Address{Stack: "conformance", Type: "github_full_repository", Name: repoName},
			Lookup:         lookup,
			ProviderConfig: json.RawMessage(`{}`),
			Timeout:        60 * time.Second,
			ProviderEnv:    []string{"UBX_DYNAMIC_PROVIDER_NAME=github", "GITHUB_TOKEN=" + token},
		})
		if err != nil {
			t.Fatalf("ProbeDestroyHonesty: %v", err)
		}
		if finding != nil {
			t.Errorf("got a FindingDestroyLie against real GitHub, want nil: %+v", finding)
		} else {
			t.Log("no finding (honest destroy)")
		}

		if !githubRepoAbsent(t, owner, repoName) {
			t.Fatalf("repo %s/%s still exists after a claimed-honest destroy -- NOT actually gone", owner, repoName)
		}
		t.Logf("independent confirmation: repo %s/%s is genuinely gone", owner, repoName)
	})
}

// githubTokenHasScope reports whether token carries scope, per GitHub's
// own real, authoritative X-OAuth-Scopes response header (confirmed live
// this session to be the real, accurate source -- gh CLI's own local
// `gh auth status` cache proved stale/unreliable against a just-refreshed
// token during this exact investigation).
func githubTokenHasScope(t *testing.T, token, scope string) bool {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "token "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("check token scopes: %v", err)
	}
	defer resp.Body.Close()
	scopes := resp.Header.Get("X-OAuth-Scopes")
	for _, s := range strings.Split(scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

func ghLogin(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		t.Fatalf("gh api user: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func createGitHubRepo(t *testing.T, name string) {
	t.Helper()
	out, err := exec.Command("gh", "repo", "create", name, "--private",
		"--description", "throwaway, ubx conformance probe testing, safe to delete").CombinedOutput()
	if err != nil {
		t.Fatalf("gh repo create %s: %v\n%s", name, err, out)
	}
	t.Logf("created real GitHub repo: %s", strings.TrimSpace(string(out)))
}

func updateGitHubRepoDescription(t *testing.T, owner, name, description string) {
	t.Helper()
	out, err := exec.Command("gh", "api", "-X", "PATCH", "repos/"+owner+"/"+name,
		"-f", "description="+description).CombinedOutput()
	if err != nil {
		t.Fatalf("gh api PATCH repos/%s/%s: %v\n%s", owner, name, err, out)
	}
}

func deleteGitHubRepo(t *testing.T, owner, name string) {
	t.Helper()
	exec.Command("gh", "repo", "delete", owner+"/"+name, "--yes").Run()
}

func githubRepoAbsent(t *testing.T, owner, name string) bool {
	t.Helper()
	out, err := exec.Command("gh", "api", "repos/"+owner+"/"+name).CombinedOutput()
	if err == nil {
		t.Logf("gh api repos/%s/%s unexpectedly succeeded:\n%s", owner, name, out)
		return false
	}
	return strings.Contains(string(out), "404") || strings.Contains(string(out), "Not Found")
}

// --- AWS/SQS: all four probes against a real, disposable queue ---

const awsSQSDynamicConfigTemplate = `
[dynamic_providers.aws]
schema_source = "smithy"
schema_url = "https://raw.githubusercontent.com/aws/api-models-aws/main/models/sqs/service/2012-11-05/sqs-2012-11-05.json"
base_url = "https://sqs.%s.amazonaws.com"
target_prefix = "AmazonSQS"

[dynamic_providers.aws.auth]
type = "aws_sigv4"
[dynamic_providers.aws.auth.params]
region = "%s"
service = "sqs"
credential_source = "profile"
`

// TestConformance_DynamicProvider_AWS_SQS_AllProbes runs all four real
// conformance probes against aws_sqs_queue -- the Dynamic Provider's own
// Smithy-sourced resource type (UBI-158 Phase 4), never hashicorp/aws.
func TestConformance_DynamicProvider_AWS_SQS_AllProbes(t *testing.T) {
	RequireLive(t)
	const region = "us-east-1"
	binPath := dynamicProviderBinary(t)
	workDir := t.TempDir()
	writeDynamicConfig(t, workDir, fmt.Sprintf(awsSQSDynamicConfigTemplate, region, region))

	queueName := uniqueName("ubx-conformance-dynprov-sqs")
	queueURL := runAWSOutput(t, "sqs", "create-queue", "--queue-name", queueName, "--region", region, "--query", "QueueUrl", "--output", "text")
	t.Logf("created real SQS queue: %s", queueURL)
	t.Cleanup(func() { exec.Command("aws", "sqs", "delete-queue", "--queue-url", queueURL, "--region", region).Run() })

	baseCfg := LiveReadProbeConfig{
		ProviderPath:   binPath,
		ProviderDir:    workDir,
		Source:         "ubx-provider-dynamic/aws",
		Version:        "dynamic",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sqs_queue", Name: queueName},
		ProviderConfig: json.RawMessage(`{}`),
		Timeout:        30 * time.Second,
		ProviderEnv:    []string{"UBX_DYNAMIC_PROVIDER_NAME=aws"},
	}
	lookup := MustMarshal(map[string]string{"queue_url": queueURL})

	// Probe 1 (identity-shape, live): aws_sqs_queue's own real lookup key
	// is queue_url alone -- nothing smaller to compare against, the same
	// honest "nothing to compare" case as GitHub's own probe 1 above.
	if finding, err := ProbeIdentityShapeLive(context.Background(), baseCfg, lookup, lookup); err != nil {
		t.Fatalf("ProbeIdentityShapeLive: %v", err)
	} else if finding != nil {
		t.Errorf("unexpected identity-shape finding: %+v", finding)
	} else {
		t.Log("probe 1 (identity-shape): no finding")
	}

	// Probe 2 (sensitive echo, live): a real SQS queue has no free-form,
	// human-authored text field the way a GitHub repo's "description"
	// is -- every real settable attribute (VisibilityTimeout,
	// MessageRetentionPeriod, ...) is a numeric policy knob, not
	// something a marker string could be planted in and read back
	// through this provider's own real schema, other than a queue tag.
	// Real AWS convention (confirmed live during UBI-158 Phase 2/3
	// research against aws_sns_topic): tags round-trip through BOTH
	// "tags" and a provider-computed "tags_all," a legitimate real
	// duplicate, not a leak -- excluded via expectedAttrs exactly as that
	// prior finding already established.
	marker := "ubx-conformance-marker-" + uniqueName("m")
	runAWS(t, "sqs", "tag-queue", "--queue-url", queueURL, "--region", region, "--tags", "conformance="+marker)
	if finding, err := ProbeSensitiveEchoLive(context.Background(), baseCfg, lookup, marker, "tags", "tags_all"); err != nil {
		t.Fatalf("ProbeSensitiveEchoLive: %v", err)
	} else if finding != nil {
		t.Errorf("real sensitive-echo finding against aws_sqs_queue: %+v", finding)
	} else {
		t.Log("probe 2 (sensitive-echo): no finding")
	}

	// Probe 4 (drift-detectability, live). A real Finding here is NOT
	// necessarily a bug -- see the identical reasoning in the GitHub test
	// above -- so this only fails on an unexpected ERROR, always logging
	// whatever real outcome actually occurred.
	driftLedger := core.Open(t.TempDir())
	if finding, err := ProbeDriftLive(context.Background(), driftLedger, baseCfg, lookup); err != nil {
		t.Fatalf("ProbeDriftLive: %v", err)
	} else if finding != nil {
		t.Logf("real undriftable finding (not necessarily a bug, see docs/conformance-harness.md): %+v", finding)
	} else {
		t.Log("probe 4 (drift-detectability): no finding")
	}

	// Probe 3 (destroy honesty, live) -- its own, separate ledger.
	destroyLedger := core.Open(t.TempDir())
	finding, err := ProbeDestroyHonesty(context.Background(), destroyLedger, DestroyProbeConfig{
		ProviderPath:   binPath,
		ProviderDir:    workDir,
		Source:         "ubx-provider-dynamic/aws",
		Version:        "dynamic",
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sqs_queue", Name: queueName},
		Lookup:         lookup,
		ProviderConfig: json.RawMessage(`{}`),
		Timeout:        60 * time.Second,
		ProviderEnv:    []string{"UBX_DYNAMIC_PROVIDER_NAME=aws"},
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}
	if finding != nil {
		t.Errorf("got a FindingDestroyLie against real AWS SQS, want nil: %+v", finding)
	} else {
		t.Log("probe 3 (destroy-honesty): no finding (honest destroy)")
	}

	out, getErr := exec.Command("aws", "sqs", "get-queue-attributes", "--queue-url", queueURL, "--region", region).CombinedOutput()
	t.Logf("independent confirmation, aws sqs get-queue-attributes --queue-url %s:\n%s", queueURL, out)
	if getErr == nil {
		t.Fatalf("aws sqs get-queue-attributes still succeeds after a claimed-honest destroy -- queue %s is NOT actually gone", queueURL)
	}
	if !strings.Contains(string(out), "NonExistentQueue") {
		t.Fatalf("aws sqs get-queue-attributes failed for an unexpected reason (want NonExistentQueue): %s", out)
	}
}
