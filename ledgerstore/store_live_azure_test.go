//go:build cloudblob

package ledgerstore

import (
	"os"
	"testing"
)

// liveAzureBucketURI is the real Azure Blob Storage container UBI-56
// provisioned specifically for this conformance suite (container
// ubx-states, storage account ubxstates, resource group ubx-states-rg,
// region eastus). azureblob's own URLOpener treats the URL host as the
// CONTAINER name, not the account (confirmed directly against
// azureblob's own source before relying on it) -- the account itself
// comes from the AZURE_STORAGE_ACCOUNT env var, checked below.
const liveAzureBucketURI = "azblob://ubx-states"

// requireLedgerStoreLiveAzure additionally requires AZURE_STORAGE_ACCOUNT
// and AZURE_STORAGE_KEY, on top of requireLedgerStoreLive's own
// UBX_CONFORMANCE_LIVE gate -- azureblob's own default URL opener falls
// back to azidentity.NewDefaultAzureCredential (Azure AD) when no shared
// key/connection-string/SAS env var is set, but this account's own
// AAD-based data-plane access was deliberately NOT granted this session
// (UBI-56: assigning a new IAM role to run a test is a real permission
// elevation the harness correctly refused to let this session grant
// itself unprompted) -- shared-key auth via these two env vars sidesteps
// that entirely, and is what this suite actually verifies against.
func requireLedgerStoreLiveAzure(t *testing.T) {
	t.Helper()
	requireLedgerStoreLive(t)
	if os.Getenv("AZURE_STORAGE_ACCOUNT") == "" || os.Getenv("AZURE_STORAGE_KEY") == "" {
		t.Skip("skipping: set AZURE_STORAGE_ACCOUNT and AZURE_STORAGE_KEY (in addition to UBX_CONFORMANCE_LIVE=1) to run LedgerStore conformance against real Azure Blob Storage")
	}
}

// The same four real-conformance scenarios S3 already proves (UBI-56:
// "the same real crash-injected orphan scenario the harness already
// exercises for S3"), against real Azure Blob Storage -- literally the
// same shared test bodies store_live_test.go defines, not a re-derived
// copy. Each wrapper checks requireLedgerStoreLiveAzure itself, before
// calling into the shared body (which independently re-checks
// requireLedgerStoreLive too, harmlessly redundant) -- lockprobe's own
// child processes inherit this same process's environment automatically,
// so AZURE_STORAGE_ACCOUNT/AZURE_STORAGE_KEY reach the real subprocess
// CAS/lock tests spawn without any extra plumbing here.

func TestLive_Azure_BasicRoundTrip(t *testing.T) {
	requireLedgerStoreLiveAzure(t)
	testLiveBasicRoundTrip(t, liveAzureBucketURI)
}

func TestLive_Azure_CASRace_RealConcurrentProcesses(t *testing.T) {
	requireLedgerStoreLiveAzure(t)
	testLiveCASRace(t, liveAzureBucketURI)
}

func TestLive_Azure_LockContention_RealConcurrentProcesses(t *testing.T) {
	requireLedgerStoreLiveAzure(t)
	testLiveLockContention(t, liveAzureBucketURI)
}

func TestLive_Azure_LockTTLExpiry_RealReclaim(t *testing.T) {
	requireLedgerStoreLiveAzure(t)
	testLiveLockTTLExpiry(t, liveAzureBucketURI)
}
