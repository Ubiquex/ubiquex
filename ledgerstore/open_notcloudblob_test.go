//go:build !cloudblob

package ledgerstore

import (
	"context"
	"strings"
	"testing"
)

// TestOpen_CloudblobSchemeWithoutBuildTag_GetsAnActionableHint runs only
// in this package's own default (untagged) test build, where gs://
// azblob:// are genuinely unregistered -- exactly the real condition
// UBI-56's cloudblobHint targets. No network involved: blob.OpenBucket
// fails immediately once it can't find a matching driver. Excluded from
// a -tags cloudblob test run: there, both schemes' own drivers ARE
// registered, so this test's own premise (driver not found) no longer
// holds -- store_live_gcs_test.go/store_live_azure_test.go cover that
// build instead.
func TestOpen_CloudblobSchemeWithoutBuildTag_GetsAnActionableHint(t *testing.T) {
	for _, uri := range []string{"gs://some-bucket/prefix/", "azblob://some-container/prefix/"} {
		_, _, err := Open(context.Background(), uri)
		if err == nil {
			t.Fatalf("Open(%q): expected an error in a build without -tags cloudblob, got nil", uri)
		}
		if !strings.Contains(err.Error(), "-tags cloudblob") {
			t.Errorf("Open(%q) error = %q, want it to mention the -tags cloudblob fix", uri, err)
		}
	}
}
