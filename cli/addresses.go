package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/provider"
)

// newAddressesCmd is UBI-48, the fourth and final ticket of the read-only
// projection quartet: a flat, copy-pasteable inventory of every address
// this ledger has recorded, each with the $cross form an md doc or intent
// file actually needs -- born from the real UX gap of authoring a $cross
// reference today requiring manual archaeology through `ubx status`/`ubx
// why` output to find a neighbor's exact address.
//
// Mechanically two halves: core.Ledger.Addresses (the ledger-level fold --
// which addresses exist, active or, with --all, tombstoned) and this
// command's own provider-schema fetch (which attributes are referenceable
// on each one, at the version it was ACTUALLY built against, never
// whatever [providers] happens to pin today -- see resolveAddressProviders'
// own doc comment for why providerPool is the wrong tool here). Never
// itself an exit-1 "finding" the way `ubx verify`/`ubx status --drift`
// produce one -- 0 on any successful inventory, including an empty one, 2
// on a genuine error (unknown stack, provider launch failure).
func newAddressesCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		all       bool
		jsonOut   bool
		timeout   time.Duration
	)

	cmd := &cobra.Command{
		Use:           "addresses",
		Short:         "Referenceable-address inventory -- copy-paste-ready $cross forms for every resource in a stack",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			ledger, closeLedger, filterStack, err := resolveAddressesLedger(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return err
			}
			defer closeLedger()

			entries, err := ledger.Addresses(filterStack, all)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: %w", err)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			listing, err := buildAddressListing(ctx, entries)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: %w", err)}
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				if err := writeJSON(out, addressesToJSON(listing)); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				return nil
			}
			renderAddressesHuman(out, listing)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to inventory -- this repo's own stack by default; a different name resolves via the same base store / [ledger.external] mechanism $cross uses")
	cmd.Flags().BoolVar(&all, "all", false, "include tombstoned (destroyed) addresses, annotated -- excluded by default")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for launching each distinct provider@version and fetching its schema (measured once per distinct pair, not once for the whole command)")
	return cmd
}

// resolveAddressesLedger opens the ledger `ubx addresses --stack <name>`
// should read from. A name that's either omitted or matches this
// workspace's own declared stack (cfg.Stack) is always local: opened
// straight from ledgerDir, exactly like every other quartet command
// (openLedgerForStack), and filtered to that name only if this ledger
// might hold more than one stack's chain (docs/architecture.md's own
// "single ledger directory can legitimately hold an interleaved chain
// spanning multiple stacks"). A remote [ledger] store is handled
// identically regardless of the name -- openLedgerForStack's own
// <store>/<stack>/ addressing is already the correct, and only, way to
// reach ANY stack (including this workspace's own) under a remote store,
// so there's nothing extra to resolve.
//
// A name that names neither -- a git-local workspace asking for some
// OTHER stack by name -- is the one case openLedgerForStack structurally
// can't reach at all (it ignores its own stack argument entirely for a
// git-local store, see its own doc comment): resolved here via the exact
// same base-store/[ledger.external] mechanism core/resolver/refs.go's own
// resolveCross uses for $cross's "stack" form (deriveStackAddress's own
// plain <base>/<stack>/ join, then core.OpenRef) -- so the command
// answers "what can I reference in that neighbor" from anywhere in the
// workspace, never requiring a cd into the neighbor's own directory. A
// git-local workspace with no matching [ledger.external] entry has no
// base to resolve against at all and is refused with a teaching error,
// exactly like $cross's own identical refusal (core/resolver/refs.go,
// proven by TestResolve_CrossStack_ByStackName_NoBaseIsGitLocal_Refused)
// -- --ledger-dir pointed directly at the neighbor is the honest
// alternative in that case, not a silent guess.
func resolveAddressesLedger(ctx context.Context, ledgerDir, stack string, cfg *Config) (ledger *core.Ledger, closeFn func() error, filterStack string, err error) {
	isRemoteBase := cfg.Ledger.Store != "" && cfg.Ledger.Store != "git"
	if isRemoteBase || stack == "" || stack == cfg.Stack {
		l, closeFn, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
		if err != nil {
			return nil, nil, "", &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: %w", err)}
		}
		return l, closeFn, stack, nil
	}

	base, ok := cfg.Ledger.External[stack]
	if !ok {
		return nil, nil, "", knownStacksError(stack, cfg)
	}
	ref := strings.TrimSuffix(base, "/") + "/" + stack + "/"
	neighbor, closeFn, err := core.OpenRef(ctx, ref)
	if err != nil {
		return nil, nil, "", &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: open stack %q via [ledger.external]: %w", stack, err)}
	}
	if !neighbor.Exists() {
		closeFn()
		return nil, nil, "", knownStacksError(stack, cfg)
	}
	return neighbor, closeFn, stack, nil
}

// knownStacksError is the ticket's own "teaching error naming known
// stacks" for an unresolvable --stack: everything this workspace's own
// .ubx/config actually knows the name of -- its own declared stack, and
// every [ledger.external] key -- since nothing else is nameable at all
// without either of those (a bare remote [ledger] store has no
// "known stacks" list of its own to enumerate; any name is presumptively
// valid there and openLedgerForStack already handles it).
func knownStacksError(stack string, cfg *Config) error {
	var known []string
	if cfg.Stack != "" {
		known = append(known, cfg.Stack+" (this workspace's own stack)")
	}
	for _, s := range sortedExternalStacks(cfg.Ledger.External) {
		known = append(known, s+" ([ledger.external])")
	}
	if len(known) == 0 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: unknown stack %q -- this workspace's .ubx/config declares no stack of its own and no [ledger.external] entries to resolve a neighbor by name against; point --ledger-dir directly at that stack's own directory instead", stack)}
	}
	return &ExitCodeError{Code: 2, Err: fmt.Errorf("addresses: unknown stack %q -- known stacks: %s", stack, strings.Join(known, ", "))}
}

func sortedExternalStacks(external map[string]string) []string {
	names := make([]string, 0, len(external))
	for s := range external {
		names = append(names, s)
	}
	sort.Strings(names)
	return names
}

// addressListing is buildAddressListing's own output -- one entry per
// core.AddressEntry, decorated with its referenceable attributes.
type addressListing struct {
	Entries []addressListingEntry
}

type addressListingEntry struct {
	core.AddressEntry
	Attributes    []addressAttribute
	SchemaMissing string // non-empty explains why Attributes is empty despite an active address (no recorded provider, or that provider@version failed to launch)
}

type addressAttribute struct {
	Name      string
	Computed  bool
	CrossForm string
}

// buildAddressListing fetches, at most once per distinct (source,
// version) pair actually recorded across entries, that provider's real
// schema (provider.ParseSource/Acquire/Launch/.Schema -- loadDiagramProviders'
// own pattern, cli/propose.go), then lists each entry's own resource
// type's referenceable attributes from it. Deliberately NOT providerPool
// (cli/providerpool.go): providerPool.Get refuses to launch any version
// other than whatever [providers] currently pins, by design ("re-resolve
// against the current config") -- exactly wrong here, since the whole
// point is each resource's own RECORDED version, which may be older (or
// from a source no longer declared at all) than today's config. An entry
// whose own Provider is nil (adopted or drift-only -- never a real
// shipped create) or whose provider fails to launch degrades to an empty
// attribute list with SchemaMissing explaining why, rather than failing
// the whole command over one resource's own gap.
func buildAddressListing(ctx context.Context, entries []core.AddressEntry) (*addressListing, error) {
	cache := map[string]*provider.Schemas{}
	failed := map[string]error{}

	listing := &addressListing{Entries: make([]addressListingEntry, 0, len(entries))}
	for _, e := range entries {
		le := addressListingEntry{AddressEntry: e}
		if e.Provider == nil {
			le.SchemaMissing = "no recorded provider -- adopted or drift-recorded resource, never a shipped create"
			listing.Entries = append(listing.Entries, le)
			continue
		}

		key := e.Provider.Source + "@" + e.Provider.Version
		schemas, ok := cache[key]
		if !ok {
			if ferr, tried := failed[key]; tried {
				le.SchemaMissing = ferr.Error()
				listing.Entries = append(listing.Entries, le)
				continue
			}
			var err error
			schemas, err = fetchProviderSchemas(ctx, e.Provider.Source, e.Provider.Version)
			if err != nil {
				failed[key] = err
				le.SchemaMissing = err.Error()
				listing.Entries = append(listing.Entries, le)
				continue
			}
			cache[key] = schemas
		}

		rs, ok := schemas.Resources[e.Address.Type]
		if !ok {
			le.SchemaMissing = fmt.Sprintf("%s@%s's own schema has no resource type %q", e.Provider.Source, e.Provider.Version, e.Address.Type)
			listing.Entries = append(listing.Entries, le)
			continue
		}
		for _, attr := range rs.Block.Attributes {
			le.Attributes = append(le.Attributes, addressAttribute{
				Name:      attr.Name,
				Computed:  attr.Computed,
				CrossForm: "@" + e.Address.String() + "." + attr.Name,
			})
		}
		listing.Entries = append(listing.Entries, le)
	}
	return listing, nil
}

func fetchProviderSchemas(ctx context.Context, source, version string) (*provider.Schemas, error) {
	parsed, err := provider.ParseSource(source)
	if err != nil {
		return nil, fmt.Errorf("provider %s@%s: %w", source, version, err)
	}
	result, err := provider.Acquire(ctx, parsed, version)
	if err != nil {
		return nil, fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
	}
	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		return nil, fmt.Errorf("launch provider %s@%s: %w", source, version, err)
	}
	defer client.Close()
	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch schema for %s@%s: %w", source, version, err)
	}
	return schemas, nil
}

func renderAddressesHuman(out io.Writer, listing *addressListing) {
	if len(listing.Entries) == 0 {
		fmt.Fprintln(out, "ubx addresses: no addresses recorded")
		return
	}
	for i, e := range listing.Entries {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s\n", e.Address)
		if e.Tombstoned {
			fmt.Fprintf(out, "  DESTROYED by %s\n", displayHash(e.TombstonedBy, false))
		}
		if e.Provider != nil {
			fmt.Fprintf(out, "  provider: %s@%s\n", e.Provider.Source, e.Provider.Version)
		}
		if e.SchemaMissing != "" {
			fmt.Fprintf(out, "  attributes unknown: %s\n", e.SchemaMissing)
			continue
		}
		for _, attr := range e.Attributes {
			marker := "always present"
			if attr.Computed {
				marker = "computed, known after ship"
			}
			fmt.Fprintf(out, "  %s (%s)\n", attr.CrossForm, marker)
		}
	}
}

// addressesJSON is `ubx addresses --json`'s own top-level document
// (UBI-20 workstream 2, UBI-48).
type addressesJSON struct {
	Format  int                  `json:"format"`
	Entries []addressListingJSON `json:"entries"`
}

type addressListingJSON struct {
	Address       addressJSON            `json:"address"`
	Tombstoned    bool                   `json:"tombstoned"`
	TombstonedBy  string                 `json:"tombstoned_by,omitempty"`
	Provider      *providerRefJSON       `json:"provider,omitempty"`
	SchemaMissing string                 `json:"schema_missing,omitempty"`
	Attributes    []addressAttributeJSON `json:"attributes,omitempty"`
}

type providerRefJSON struct {
	Source  string `json:"source"`
	Version string `json:"version"`
}

type addressAttributeJSON struct {
	Name      string `json:"name"`
	Computed  bool   `json:"computed"`
	CrossForm string `json:"cross_form"`
}

func addressesToJSON(listing *addressListing) addressesJSON {
	payload := addressesJSON{Format: jsonFormatVersion, Entries: make([]addressListingJSON, 0, len(listing.Entries))}
	for _, e := range listing.Entries {
		entry := addressListingJSON{
			Address:       addressToJSON(e.Address),
			Tombstoned:    e.Tombstoned,
			TombstonedBy:  e.TombstonedBy,
			SchemaMissing: e.SchemaMissing,
		}
		if e.Provider != nil {
			entry.Provider = &providerRefJSON{Source: e.Provider.Source, Version: e.Provider.Version}
		}
		for _, attr := range e.Attributes {
			entry.Attributes = append(entry.Attributes, addressAttributeJSON{Name: attr.Name, Computed: attr.Computed, CrossForm: attr.CrossForm})
		}
		payload.Entries = append(payload.Entries, entry)
	}
	return payload
}
