package conformance

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// gcpProject is the real GCP project these live conformance tests run
// against (billing enabled, owned by the account running this suite).
// Every resource created here is a throwaway, destroyed in the test's own
// cleanup -- same discipline as the AWS live tests' create+destroy
// batch-2 pattern.
const gcpProject = "personal-273114"

// realGCPProviderPath acquires (or reuses the cached) real hashicorp/google
// provider binary via provider.Acquire -- same dogfooding of UBI-8's
// acquisition path realAWSProviderPath already does for hashicorp/aws.
func realGCPProviderPath(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/google")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := provider.Acquire(ctx, src, gcpProviderVersion)
	if err != nil {
		t.Fatalf("acquire real google provider: %v", err)
	}
	return result.Path
}

func runGCloud(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("gcloud", append(args, "--project", gcpProject, "--quiet")...).CombinedOutput()
	if err != nil {
		t.Fatalf("gcloud %v: %v\n%s", args, err, out)
	}
}

func gcpProviderConfig() json.RawMessage {
	return MustMarshal(map[string]string{"project": gcpProject})
}

// TestConformance_GCPStorageBucket creates a throwaway bucket (free at
// this scale), tests it, and deletes it.
//
// Empirically confirmed while writing this test (UBI-21 Stage 2): "id"
// alone (the bucket name) is NOT enough -- unlike aws_s3_bucket's own
// "id" alone succeeding, google_storage_bucket's ReadResource call
// errors outright ("Storage Bucket \"\": googleapi: Error 400") unless
// "name" is also set alongside "id". This is the opposite direction from
// AWS's own s3_bucket lesson (there, "bucket" alone was the trap; here,
// "id" alone is).
func TestConformance_GCPStorageBucket(t *testing.T) {
	RequireLive(t)
	providerPath := realGCPProviderPath(t)
	name := uniqueName("ubx-conformance-gcs")

	runGCloud(t, "storage", "buckets", "create", "gs://"+name, "--location=us-central1")
	t.Cleanup(func() { runGCloud(t, "storage", "rm", "-r", "gs://"+name) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "google_storage_bucket", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": name, "name": name}),
		ProviderConfig: gcpProviderConfig(),
		Mutate: func(t *testing.T) {
			runGCloud(t, "storage", "buckets", "update", "gs://"+name, "--update-labels=ubx-conformance=storage")
		},
	})
}

// TestConformance_GCPPubSubTopic creates a throwaway topic (free), tests
// it, and deletes it.
//
// Empirically confirmed while writing this test: "id" alone (the full
// "projects/<project>/topics/<name>" path) does NOT error, but silently
// reads back an incomplete resource -- its "name" attribute comes back
// empty even though the topic genuinely has one. This is a materially
// different (and more dangerous) failure mode than AWS ever showed:
// AWS's own lookup traps always either errored or returned null, never a
// non-null "success" with a wrong/incomplete value. Adding "name"
// (the topic's own natural-key attribute) alongside "id" fixes it.
func TestConformance_GCPPubSubTopic(t *testing.T) {
	RequireLive(t)
	providerPath := realGCPProviderPath(t)
	name := uniqueName("ubx-conformance-pubsub")

	runGCloud(t, "pubsub", "topics", "create", name)
	t.Cleanup(func() { runGCloud(t, "pubsub", "topics", "delete", name) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath: providerPath,
		Stack:        "conformance",
		Address:      core.Address{Stack: "conformance", Type: "google_pubsub_topic", Name: name},
		Lookup: MustMarshal(map[string]string{
			"id":   "projects/" + gcpProject + "/topics/" + name,
			"name": name,
		}),
		ProviderConfig: gcpProviderConfig(),
		Mutate: func(t *testing.T) {
			runGCloud(t, "pubsub", "topics", "update", name, "--update-labels=env=conformance")
		},
	})
}

// TestConformance_GCPServiceAccount creates a throwaway service account
// (free), tests it, and deletes it.
//
// Empirically confirmed while writing this test: "id" alone (the full
// "projects/<project>/serviceAccounts/<email>" path) IS sufficient --
// unlike pubsub_topic/storage_bucket above, no companion field is
// needed. Not every GCP type has the "id alone isn't enough" trap, the
// same lesson AWS's own registry already teaches (aws_iam_policy/
// aws_sqs_queue/aws_sns_topic all need only "id" too).
func TestConformance_GCPServiceAccount(t *testing.T) {
	RequireLive(t)
	providerPath := realGCPProviderPath(t)
	// GCP service account IDs must be 6-30 characters -- uniqueName's own
	// nanosecond-timestamp suffix (fine for AWS's far more permissive
	// naming) overflows that limit, empirically confirmed while writing
	// this test. A second-resolution Unix timestamp keeps this well
	// under 30 chars while still being unique enough for a test suite
	// that doesn't run this concurrently with itself.
	name := "ubx-sa-" + time.Now().UTC().Format("20060102150405")
	email := name + "@" + gcpProject + ".iam.gserviceaccount.com"

	runGCloud(t, "iam", "service-accounts", "create", name, "--display-name=ubx conformance")
	t.Cleanup(func() { runGCloud(t, "iam", "service-accounts", "delete", email) })
	// Same IAM propagation lag as the mutate step below, just after
	// create instead of update -- confirmed empirically (an earlier run
	// of this test hit "resource unreadable" on the very first adopt
	// scan, immediately after create returned successfully).
	waitForServiceAccountDisplayName(t, email, "ubx conformance")

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "google_service_account", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": "projects/" + gcpProject + "/serviceAccounts/" + email}),
		ProviderConfig: gcpProviderConfig(),
		Mutate: func(t *testing.T) {
			runGCloud(t, "iam", "service-accounts", "update", email, "--display-name=ubx conformance mutated")
			// IAM's own read-after-write consistency lags the write
			// itself -- empirically confirmed while writing this test:
			// `describe` immediately after `update` returned the OLD
			// displayName even though `update`'s own response already
			// echoed the new one. Google documents IAM propagation as
			// "usually under a minute, occasionally up to several
			// minutes" -- wait for it to actually be visible rather than
			// racing core.RunScan's own next read against it.
			waitForServiceAccountDisplayName(t, email, "ubx conformance mutated")
		},
	})
}

// waitForServiceAccountDisplayName polls `gcloud iam service-accounts
// describe` until it reflects want, or fails the test after 90 seconds --
// GCP IAM's own documented propagation window, not an arbitrary guess.
//
// A fixed extra sleep after gcloud itself confirms the change is a
// deliberate, empirically-motivated addition, not a leftover: a real run
// of this test showed gcloud's own `describe` (one read path) and the
// Terraform provider's `ReadResource` (a different read path, and
// possibly a different backend/cache) becoming consistent at different
// times -- gcloud saw the update before core.RunScan's next read did.
// IAM propagation isn't one consistent moment across every API surface
// that reads it.
func waitForServiceAccountDisplayName(t *testing.T, email, want string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, err := exec.Command("gcloud", "iam", "service-accounts", "describe", email,
			"--project", gcpProject, "--format=value(displayName)").Output()
		if err == nil && strings.TrimSpace(string(out)) == want {
			time.Sleep(15 * time.Second)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("service account %s displayName never became %q within 90s (IAM propagation)", email, want)
		}
		time.Sleep(3 * time.Second)
	}
}

// TestConformance_GCPSecretManagerSecret creates a throwaway secret
// (free at this scale), tests it, and deletes it.
//
// Empirically confirmed while writing this test: same silent-incomplete-
// read trap as pubsub_topic -- "id" alone (the full
// "projects/<project>/secrets/<name>" path) reads back with "name": ""
// and "secret_id": null, no error at all. Adding "secret_id" (the
// secret's own natural-key attribute) alongside "id" fixes it.
func TestConformance_GCPSecretManagerSecret(t *testing.T) {
	RequireLive(t)
	providerPath := realGCPProviderPath(t)
	name := uniqueName("ubx-conformance-secret")

	runGCloud(t, "secrets", "create", name, "--replication-policy=automatic")
	t.Cleanup(func() { runGCloud(t, "secrets", "delete", name) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath: providerPath,
		Stack:        "conformance",
		Address:      core.Address{Stack: "conformance", Type: "google_secret_manager_secret", Name: name},
		Lookup: MustMarshal(map[string]string{
			"id":        "projects/" + gcpProject + "/secrets/" + name,
			"secret_id": name,
		}),
		ProviderConfig: gcpProviderConfig(),
		Mutate: func(t *testing.T) {
			runGCloud(t, "secrets", "update", name, "--update-labels=env=conformance")
		},
	})
}

// TestConformance_GCPProjectIAMCustomRole creates a throwaway custom
// role (free), tests it, and deletes it.
//
// Empirically confirmed while writing this test: "id" alone (the full
// "projects/<project>/roles/<role_id>" path) IS sufficient, same as
// google_service_account above -- no companion field needed.
func TestConformance_GCPProjectIAMCustomRole(t *testing.T) {
	RequireLive(t)
	providerPath := realGCPProviderPath(t)
	roleID := strings.ReplaceAll(uniqueName("ubx_conformance_role"), "-", "_")

	runGCloud(t, "iam", "roles", "create", roleID, "--title=ubx conformance", "--permissions=storage.buckets.get")
	t.Cleanup(func() { runGCloud(t, "iam", "roles", "delete", roleID) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "google_project_iam_custom_role", Name: roleID},
		Lookup:         MustMarshal(map[string]string{"id": "projects/" + gcpProject + "/roles/" + roleID}),
		ProviderConfig: gcpProviderConfig(),
		Mutate: func(t *testing.T) {
			runGCloud(t, "iam", "roles", "update", roleID, "--permissions=storage.buckets.get,storage.buckets.list")
		},
	})
}
