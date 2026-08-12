//go:build cloudblob

// Package ledgerstore's gs:// and azblob:// driver registration -- opt-in
// via `go build -tags cloudblob ./cmd/ubx` (or `go install -tags
// cloudblob ./cmd/ubx`), not part of the default `make build`/
// `make install` binary, which stays unchanged (S3 and git-local only).
// UBI-56: tried unconditionally first, real evidence said no -- `go mod
// tidy` with both drivers wired
// in showed the real, measured current cost is +25.6MB/+27% binary size
// (95.4MB -> 121.0MB), even after crediting this binary's own
// pre-existing GCP/Azure SDK usage (audit/gcp's and its Azure
// counterpart's own drift-detection backends already pull in a real
// chunk of both SDKs, which is genuinely why the raw MODULE count delta
// alone -- 9 net new modules -- looked deceptively small; the two
// storage CLIENT libraries themselves, cloud.google.com/go/storage and
// azure-sdk-for-go/sdk/storage/azblob, are real, new, substantial
// additions regardless). s3blob (open.go) stays unconditional: its own
// real, measured cost (10 lines go.mod, 40 lines go.sum, UBI-32 Arc B
// session 1) never approached this bar.
//
// The Store/conformance code (store.go) is itself already fully generic
// over any *blob.Bucket -- this file's only job is driver registration,
// identical in kind to s3blob's own unconditional import in open.go.
package ledgerstore

import (
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/gcsblob"
)
