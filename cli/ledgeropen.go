package cli

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/ledgerstore"
)

// openLedgerForStack opens the Ledger a command should use, per
// .ubx/config's own [ledger] table (UBI-32 Arc B, docs/architecture.md --
// "Ledger stores"): cfg.Ledger.Store empty or "git" is today's exact
// behavior, unchanged -- core.Open(ledgerDir), never touching the
// LedgerStore/remote-store machinery at all. A non-git store opens a
// remote LedgerStore at <cfg.Ledger.Store>/<stack>/
// (docs/architecture.md's own addressing rule) and requires stack to be
// non-empty -- a real, deliberate consequence of that addressing, named
// in docs/ledgerstore-adversarial.md: a remote store's own per-stack
// chain means opening one at all requires knowing which stack first,
// unlike git-local's flat, shared-chain legacy layout.
//
// The returned close func must be called once the caller is done with
// the Ledger -- a no-op for the git-directory case, a real bucket close
// for a remote one. Wired into a first, representative slice of commands
// this session (ubx resolve, ubx accept [local], ubx ship --stack); the
// rest of the CLI surface still opens git-local unconditionally -- see
// STATE.md for exactly which commands still need this.
// openRemoteLedgerStore is a package-level seam (same convention as
// configSearchStartDir/userHomeDir) so hermetic tests can point every
// openLedgerForStack call within one test at ONE shared, real bucket --
// production always calls ledgerstore.Open unchanged. Needed because
// gocloud.dev/blob's own memblob/fileblob URL openers don't give CLI-level
// tests a way to prove cross-invocation persistence otherwise: memblob
// hands back a fresh, unshared bucket per URL open regardless of host name
// (confirmed directly, see ledgerstore/open.go's own transformStoreURI
// comment), and fileblob's real directory-per-path semantics don't
// round-trip through this package's own bucket+prefix URI convention
// (designed for s3/gs/azblob's "host=bucket, path=prefix" shape, never for
// a scheme where the whole location is path-only).
var openRemoteLedgerStore = ledgerstore.Open

func openLedgerForStack(ctx context.Context, ledgerDir, stack string, cfg *Config) (*core.Ledger, func() error, error) {
	if cfg.Ledger.Store == "" || cfg.Ledger.Store == "git" {
		return core.Open(ledgerDir), func() error { return nil }, nil
	}
	if stack == "" {
		return nil, nil, fmt.Errorf("--stack is required to open a remote ledger store (.ubx/config's [ledger] store is %q, not git) -- a remote store's own per-stack chain means opening one at all requires knowing which stack first", cfg.Ledger.Store)
	}
	storeURI, err := ledgerstore.WithStack(cfg.Ledger.Store, stack)
	if err != nil {
		return nil, nil, fmt.Errorf("ledger store: %w", err)
	}
	store, closeFn, err := openRemoteLedgerStore(ctx, storeURI)
	if err != nil {
		return nil, nil, fmt.Errorf("ledger store: open %s: %w", storeURI, err)
	}
	return core.OpenStoreForStack(store, cfg.Ledger.Store, cfg.Ledger.External), closeFn, nil
}

func init() {
	// Registers the concrete gocloud.dev/blob-backed opener core.OpenRef
	// uses for a store-URI-shaped ref (core/resolver's own $cross "stack"
	// resolution, and a destroy's own known_dependents check) -- core
	// itself never imports ledgerstore, matching the "core stays
	// dependency-free" inversion every other heavy-SDK integration in
	// this codebase already uses.
	core.RegisterRemoteLedgerOpener(func(ctx context.Context, storeURI string) (core.LedgerStore, func() error, error) {
		return ledgerstore.Open(ctx, storeURI)
	})
}
