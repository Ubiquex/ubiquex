package ledgerstore

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"gocloud.dev/blob"

	// Driver registration only -- the Store/conformance code above is
	// itself already generic over any *blob.Bucket, s3/gs/azblob alike;
	// only s3 is wired here this session. gs (gcsblob) and azblob
	// (azureblob) were tried and deliberately backed out, not just
	// skipped: their own driver packages pull in the full Azure SDK and
	// GCP's own monitoring/tracing/OpenTelemetry exporter stacks --
	// dozens of new transitive dependencies apiece, checked directly via
	// `go mod tidy` before deciding, not assumed cheap from "it's the
	// same interface." That's real evidence the harness does NOT make
	// them cheap in the way UBI-32 Arc B's own scope conditioned adding
	// them on -- filed as a follow-up (see STATE.md), not forced through.
	_ "gocloud.dev/blob/s3blob"
)

// Open opens the blob bucket storeURI names and returns a Store backed by
// it, plus a close function the caller must call when done with the
// Ledger it's used to construct.
//
// storeURI's own path component is ubx's own path-style prefix
// convention (docs/architecture.md's own sketch, `s3://bucket/acme/prod/`)
// -- confirmed directly against gocloud.dev/blob's source before relying
// on it: no driver's URLOpener interprets a URL path segment as a bucket
// prefix at all; the portable mechanism is a `?prefix=` query parameter,
// applied uniformly by blob.OpenBucket itself regardless of scheme. Open
// is the translation layer: storeURI's path becomes `?prefix=<path>`
// before the bucket is ever opened, so `ubx`'s own config keeps the
// nicer, doc-matching syntax without every cloud's own driver needing to
// understand it.
func Open(ctx context.Context, storeURI string) (*Store, func() error, error) {
	bucket, err := openBucket(ctx, storeURI)
	if err != nil {
		return nil, nil, err
	}
	return New(bucket), bucket.Close, nil
}

func openBucket(ctx context.Context, storeURI string) (*blob.Bucket, error) {
	transformed, err := transformStoreURI(storeURI)
	if err != nil {
		return nil, err
	}
	return blob.OpenBucket(ctx, transformed)
}

// transformStoreURI is openBucket's own path-style-to-query-param
// translation, pulled out as a pure string transform so it's directly
// testable without opening any real bucket (gocloud.dev/blob's own
// memblob URL opener creates a fresh, unshared bucket per call regardless
// of host name, so round-tripping through two separate Open calls can't
// itself prove this translation -- testing the string transform directly
// can).
func transformStoreURI(storeURI string) (string, error) {
	u, err := url.Parse(storeURI)
	if err != nil {
		return "", fmt.Errorf("ledgerstore: parse store URI: %w", err)
	}
	prefix := strings.TrimPrefix(u.Path, "/")

	opened := *u
	opened.Path = ""
	opened.RawPath = ""
	if prefix != "" {
		q := opened.Query()
		if existing := q.Get("prefix"); existing != "" {
			// A caller-supplied ?prefix= combines under the path-style
			// one, path-style outermost -- so WithStack's own appended
			// stack segment (itself expressed via the path, see below)
			// always lands innermost, matching <base store>/<stack>/.
			prefix = joinPrefix(prefix, existing)
		}
		q.Set("prefix", ensureTrailingSlash(prefix))
		opened.RawQuery = q.Encode()
	}
	return opened.String(), nil
}

// WithStack appends stack as a further path segment onto baseStoreURI,
// matching docs/architecture.md's own addressing rule (`<base store>/
// <stack>/`, derived, never separately configured). baseStoreURI is
// whatever a stack's own `.ubx/config` `[ledger]` `store` key names.
func WithStack(baseStoreURI, stack string) (string, error) {
	u, err := url.Parse(baseStoreURI)
	if err != nil {
		return "", fmt.Errorf("ledgerstore: parse store URI: %w", err)
	}
	u.Path = "/" + joinPrefix(strings.TrimPrefix(u.Path, "/"), stack)
	return u.String(), nil
}

func joinPrefix(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimPrefix(strings.TrimSuffix(b, "/"), "/")
	switch {
	case a == "":
		return ensureTrailingSlash(b)
	case b == "":
		return ensureTrailingSlash(a)
	default:
		return ensureTrailingSlash(a + "/" + b)
	}
}

func ensureTrailingSlash(s string) string {
	if s == "" || strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}
