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
	store, closeFn, err := ledgerstore.Open(ctx, storeURI)
	if err != nil {
		return nil, nil, fmt.Errorf("ledger store: open %s: %w", storeURI, err)
	}
	return core.OpenStore(store), closeFn, nil
}
