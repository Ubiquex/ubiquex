package core

import (
	"context"
	"fmt"
	"strings"
)

// remoteLedgerOpener opens a non-git-local ledger address (an s3://,
// gs://, or azblob:// store URI) -- installed once, at process startup,
// by RegisterRemoteLedgerOpener. nil until then, matching every other
// "core stays dependency-free" inversion in this codebase
// (StateReader/EventLookup): the concrete gocloud.dev/blob-backed
// implementation lives in the sibling ledgerstore package, which core
// never imports.
var remoteLedgerOpener func(ctx context.Context, storeURI string) (LedgerStore, func() error, error)

// RegisterRemoteLedgerOpener installs the function OpenRef uses to open
// a store-URI-shaped ref. Called once, by the cli package's own startup
// wiring (cli.NewRootCmd), since core itself stays free of
// gocloud.dev/blob and its own driver packages -- the same inversion
// cloudtrail/gcpaudit/k8saudit already establish for their own heavy SDK
// dependencies, expressed as a registry (gocloud.dev/blob's own URLMux is
// the direct precedent) rather than a parameter thread, since OpenRef's
// own callers are scattered across core/resolver, not centralized behind
// one entry point the way a StateReader is.
func RegisterRemoteLedgerOpener(fn func(ctx context.Context, storeURI string) (LedgerStore, func() error, error)) {
	remoteLedgerOpener = fn
}

// OpenRef opens a Ledger from ref: a plain git-directory path (the
// legacy, forever-supported shape -- docs/architecture.md's own
// "git-local flat legacy" posture) or a store URI (s3://, gs://,
// azblob://) recognized by whichever opener RegisterRemoteLedgerOpener
// installed. This is what $cross's own neighbor-ledger resolution
// (core/resolver/refs.go) and a destroy's own known_dependents check use
// instead of a bare Open, so either shape works uniformly -- an operator
// can name a remote store's own full address anywhere a git directory
// path was accepted before, with no separate syntax.
//
// The returned close func must be called once the caller is done with
// the Ledger -- a no-op for the git-directory case, a real bucket close
// for a remote one (matching ledgerstore.Open's own contract).
func OpenRef(ctx context.Context, ref string) (*Ledger, func() error, error) {
	if !looksLikeStoreURI(ref) {
		return Open(ref), func() error { return nil }, nil
	}
	if remoteLedgerOpener == nil {
		return nil, nil, fmt.Errorf("open %s: no remote ledger store support registered in this build", ref)
	}
	store, closeFn, err := remoteLedgerOpener(ctx, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", ref, err)
	}
	return OpenStore(store), closeFn, nil
}

// looksLikeStoreURI reports whether ref names a store URI (s3://...)
// rather than a plain filesystem path -- a "://" is never valid inside
// an ordinary directory path on any platform this project supports, so
// its presence is sufficient, unambiguous signal.
func looksLikeStoreURI(ref string) bool {
	return strings.Contains(ref, "://")
}
