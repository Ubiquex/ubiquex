//go:build cloudblob

package ledgerstore

import "testing"

// liveGCSBucketURI is the real GCS bucket UBI-56 provisioned specifically
// for this conformance suite (gs://ubx-states, project ubiquex-502722,
// location us-east1 -- confirmed via a real `gcloud storage buckets
// describe` before relying on it, not assumed from the creation command's
// own exit code). No region query param: unlike S3, gcsblob's own
// URLOpener has no such parameter -- GCS bucket location is fixed at
// creation, never selectable per-request (confirmed directly against
// gcsblob's own source before relying on it).
//
// Credentials: Application Default Credentials (gcloud's own cached
// `gcloud auth application-default login` session) -- gcsblob's default
// URL opener uses these automatically, no env var wiring needed in this
// file at all (unlike Azure's own shared-key requirement, see
// store_live_azure_test.go).
const liveGCSBucketURI = "gs://ubx-states"

// The same four real-conformance scenarios S3 already proves (UBI-56:
// "the same real crash-injected orphan scenario the harness already
// exercises for S3"), against real GCS -- literally the same shared test
// bodies store_live_test.go defines, not a re-derived copy.

func TestLive_GCS_BasicRoundTrip(t *testing.T) { testLiveBasicRoundTrip(t, liveGCSBucketURI) }

func TestLive_GCS_CASRace_RealConcurrentProcesses(t *testing.T) {
	testLiveCASRace(t, liveGCSBucketURI)
}

func TestLive_GCS_LockContention_RealConcurrentProcesses(t *testing.T) {
	testLiveLockContention(t, liveGCSBucketURI)
}

func TestLive_GCS_LockTTLExpiry_RealReclaim(t *testing.T) {
	testLiveLockTTLExpiry(t, liveGCSBucketURI)
}
