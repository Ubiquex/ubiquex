package ledgerstore

import (
	"context"
	neturl "net/url"
	"strings"
	"testing"

	_ "gocloud.dev/blob/memblob"
)

// TestWithStack_AppendsAsPathSegment confirms docs/architecture.md's own
// addressing rule (<base store>/<stack>/, derived, never separately
// configured) at the string level, independent of any real bucket.
func TestWithStack_AppendsAsPathSegment(t *testing.T) {
	cases := []struct{ base, stack, want string }{
		{"s3://acme-ledger", "payments", "s3://acme-ledger/payments/"},
		{"s3://acme-ledger/acme/prod", "payments", "s3://acme-ledger/acme/prod/payments/"},
		{"s3://acme-ledger/acme/prod/", "payments", "s3://acme-ledger/acme/prod/payments/"},
	}
	for _, tc := range cases {
		got, err := WithStack(tc.base, tc.stack)
		if err != nil {
			t.Fatalf("WithStack(%q, %q): %v", tc.base, tc.stack, err)
		}
		if got != tc.want {
			t.Errorf("WithStack(%q, %q) = %q, want %q", tc.base, tc.stack, got, tc.want)
		}
	}
}

// TestTransformStoreURI_PathStyleBecomesQueryParam confirms the real
// translation this package's own Open performs, as a pure string
// transform -- checked directly against gocloud.dev/blob's source before
// relying on it: no driver's URLOpener understands a path segment as a
// prefix; only the generic ?prefix= query parameter does. (Not testable
// by round-tripping through two real Open calls: gocloud.dev/blob's own
// memblob URL opener hands back a fresh, unshared bucket per call
// regardless of host name, so nothing would actually prove the prefix
// landed correctly that way.)
func TestTransformStoreURI_PathStyleBecomesQueryParam(t *testing.T) {
	cases := []struct{ in, want string }{
		{"s3://acme-ledger/acme/prod/payments/", "s3://acme-ledger?prefix=acme%2Fprod%2Fpayments%2F"},
		{"s3://acme-ledger", "s3://acme-ledger"},
		{"s3://acme-ledger?region=us-east-1", "s3://acme-ledger?region=us-east-1"},
		{"s3://acme-ledger/payments/?region=us-east-1", "s3://acme-ledger?prefix=payments%2F&region=us-east-1"},
	}
	for _, tc := range cases {
		got, err := transformStoreURI(tc.in)
		if err != nil {
			t.Fatalf("transformStoreURI(%q): %v", tc.in, err)
		}
		// Query parameter order isn't guaranteed by url.Values.Encode, so
		// parse both sides back rather than comparing raw strings for the
		// multi-param case.
		if !sameURL(t, got, tc.want) {
			t.Errorf("transformStoreURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func sameURL(t *testing.T, a, b string) bool {
	t.Helper()
	ua, errA := neturl.Parse(a)
	ub, errB := neturl.Parse(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host && ua.Path == ub.Path && ua.Query().Encode() == ub.Query().Encode()
}

// TestOpen_RealBucket confirms Open itself (not just the URL transform)
// produces a usable Store against a real, if in-memory, bucket.
func TestOpen_RealBucket(t *testing.T) {
	ctx := context.Background()
	store, closeFn, err := Open(ctx, "mem://acme-ledger/acme/prod/payments/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closeFn()

	if err := store.WriteSaltIfAbsent(ctx, []byte("0123456789012345678901234567890a")); err != nil {
		t.Fatalf("WriteSaltIfAbsent: %v", err)
	}
	b, found, err := store.ReadSalt(ctx)
	if err != nil || !found {
		t.Fatalf("ReadSalt: found=%v err=%v", found, err)
	}
	if string(b) != "0123456789012345678901234567890a" {
		t.Fatalf("got %q, want the written salt back", b)
	}
}

// TestOpen_UnrelatedUnregisteredScheme_NoMisleadingHint confirms the hint
// is scoped to gs/azblob specifically -- a genuine typo or unsupported
// scheme must get gocloud.dev/blob's own plain error, not an irrelevant
// "-tags cloudblob" suggestion that wouldn't actually fix anything.
func TestOpen_UnrelatedUnregisteredScheme_NoMisleadingHint(t *testing.T) {
	_, _, err := Open(context.Background(), "totallyfake://some-bucket/prefix/")
	if err == nil {
		t.Fatal("Open(totallyfake://...): expected an error, got nil")
	}
	if strings.Contains(err.Error(), "cloudblob") {
		t.Errorf("Open(totallyfake://...) error = %q, must not mention cloudblob for an unrelated scheme", err)
	}
}
