package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/provider"
)

// TestLookupHints_LiveWrongLookup_TeachesTheMissingField is UBI-20
// workstream 3's live verification: a real scan against the real
// "ubx-states" bucket (same fixture TestConformance_AWSS3Bucket uses),
// deliberately given only aws_s3_bucket's own natural-key attribute
// ({"bucket": bucket}, no "id") -- exactly the mistake someone coming from
// Terraform's own attribute naming might make. Confirms the resulting
// core.ErrResourceUnreadable actually teaches "id" as the fix, against a
// real provider round trip, not just the fakeprovider unit coverage in
// core/scan_test.go.
//
// This test caught a real bug during development: the first version of
// core/lookuphints had the hint backwards (naming "bucket" as the missing
// field). A live round trip proved {"id": bucket} alone succeeds and
// {"bucket": bucket} alone fails -- the opposite of what the Notes prose
// alone would suggest without actually calling ReadResource. See
// core/scan.go's lookupHintText doc comment for the corrected direction.
func TestLookupHints_LiveWrongLookup_TeachesTheMissingField(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	const bucket = "ubx-states"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := provider.Launch(ctx, providerPath)
	if err != nil {
		t.Fatalf("launch provider: %v", err)
	}
	defer client.Close()

	ledger := core.Open(t.TempDir())
	salt, err := ledger.Salt()
	if err != nil {
		t.Fatalf("ledger salt: %v", err)
	}
	_, err = core.RunScan(ctx, stateReaderAdapter{p: client.Provider, salt: salt}, ledger, core.ScanRequest{
		Address:        core.Address{Stack: "conformance", Type: "aws_s3_bucket", Name: bucket},
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		CurrentState:   MustMarshal(map[string]string{"bucket": bucket}),
		ProviderSource: "hashicorp/aws",
	})
	if err == nil {
		t.Fatal("expected RunScan to fail against a lookup missing \"id\", got nil error")
	}
	if !strings.Contains(err.Error(), `"id"`) {
		t.Fatalf("expected the error to teach \"id\" as the fix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("expected the error to name \"bucket\" as the natural key that alone isn't enough, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cli/lookup") {
		t.Fatalf("expected the error to link to the lookup docs page, got: %v", err)
	}
}

// TestLookupHints_LiveIDAlone_Succeeds confirms the flip side against the
// same real bucket: "id" alone (ubx's own default --lookup convention
// shown throughout the docs) is genuinely sufficient for this type --
// nothing here should ever regress into requiring "bucket" too, which
// would contradict every scan.mdx example using bare {"id": "..."}.
func TestLookupHints_LiveIDAlone_Succeeds(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	const bucket = "ubx-states"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := provider.Launch(ctx, providerPath)
	if err != nil {
		t.Fatalf("launch provider: %v", err)
	}
	defer client.Close()

	ledger := core.Open(t.TempDir())
	salt, err := ledger.Salt()
	if err != nil {
		t.Fatalf("ledger salt: %v", err)
	}
	res, err := core.RunScan(ctx, stateReaderAdapter{p: client.Provider, salt: salt}, ledger, core.ScanRequest{
		Address:        core.Address{Stack: "conformance", Type: "aws_s3_bucket", Name: bucket},
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		CurrentState:   MustMarshal(map[string]string{"id": bucket}),
	})
	if err != nil {
		t.Fatalf("expected \"id\" alone to succeed, got: %v", err)
	}
	if res.Outcome != core.ScanNew && res.Outcome != core.ScanUnchanged && res.Outcome != core.ScanDrifted {
		t.Fatalf("unexpected outcome: %v", res.Outcome)
	}
}
