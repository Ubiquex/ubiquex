package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/discovery"
	"github.com/ubiquex/ubiquex/provider"
)

// newDiscoveryTaggingAPI is a package-level seam (the same convention
// openRemoteLedgerStore already establishes, cli/ledgeropen.go) so
// hermetic tests can point runScanDiscover at a fake discovery.TaggingAPI
// without touching real AWS — production always calls
// discovery.NewTaggingAPI unchanged.
var newDiscoveryTaggingAPI = discovery.NewTaggingAPI

// newDiscoveryStateReader is a second package-level seam, the same
// convention, for the per-resource ReadResource half of `--discover`:
// production resolves and launches a real provider binary exactly like
// every other single-provider command already does; a hermetic test
// overrides this to return a hand-written fake core.StateReader instead
// (never fakeprovider's own shared fake_widget fixture, whose schema
// doesn't include any of discovery's own real, AWS-specific tier-table
// types) -- full, precise control over ReadResource's own behavior per
// adversarial scenario (a clean read, a permission-style error, a
// not-found/deleted-since-list null read), with zero risk to the wide
// existing fakeprovider-based test suite elsewhere in this project.
var newDiscoveryStateReader = func(ctx context.Context, providerPath, source, providerVersion string, salt []byte) (reader core.StateReader, closeReader func() error, checksum string, err error) {
	path, checksum, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
	if err != nil {
		return nil, nil, "", err
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, nil, "", err
	}
	return newStateReader(client.Provider, salt, source), client.Close, checksum, nil
}

// scanDiscoverOptions is `ubx scan --discover`'s own flag surface
// (UBI-45, docs/discovery.md) -- enumerates real resources via the AWS
// Resource Groups Tagging API (the mechanism decision's own chosen
// primary, made empirically) and bridges each returned ARN into a
// provider lookup shape (discovery.BuildLookup's own three-tier
// translation), then reuses the exact same core.RunScan/core.
// GenerateProposal pipeline runScanAll already does -- a new identity
// source, never a new proposal kind or apply path.
type scanDiscoverOptions struct {
	Tags            map[string][]string
	Types           []string
	Region          string
	Limit           int
	Yes             bool
	Stack           string
	OutDir          string
	LedgerDir       string
	Config          *Config
	ProviderPath    string
	Source          string
	ProviderVersion string
	ProviderConfig  string
	Providers       map[string]string
	ProviderConfigs map[string]map[string]any
	Timeout         time.Duration
	NoAttribution   bool
}

// runScanDiscover mirrors runScanAll's own structure closely (cli/
// scanall.go): enumerate once (here, via the tagging API instead of a
// tfstate file), then per resource run the identical, unmodified
// core.RunScan/core.GenerateProposal pipeline. A discovered resource
// discovery couldn't bridge to a lookup shape is skipped with its own
// distinct, honest reason -- "discovered, not yet adoptable" -- never
// silently dropped, matching every other skip category's own visibility.
func runScanDiscover(ctx context.Context, out io.Writer, opts scanDiscoverOptions) error {
	if len(opts.Tags) == 0 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover requires at least one --tag -- whole-account enumeration is a firehose, not a supported mode (docs/discovery.md)")}
	}

	api, err := newDiscoveryTaggingAPI(ctx, opts.Region)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}

	discovered, err := discovery.Discover(ctx, api, discovery.Options{Tags: opts.Tags, Types: opts.Types})
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}

	adoptableCount := 0
	for _, d := range discovered {
		if d.Lookup != nil {
			adoptableCount++
		}
	}
	fmt.Fprintf(out, "%d resource(s) matched, %d adoptable\n", len(discovered), adoptableCount)
	if err := discovery.CheckLimit(adoptableCount, opts.Limit, opts.Yes); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}

	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
		}
	}

	ledger, closeLedger, err := openLedgerForStack(ctx, opts.LedgerDir, opts.Stack, opts.Config)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}
	defer closeLedger()

	salt, err := ledger.Salt()
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}

	// Same multi-provider-vs-single-provider branch runScanAll already
	// established (docs/architecture.md's UBI-43 amendment) -- discovery
	// adds a new identity source only, this provider-selection machinery
	// is completely unchanged.
	var (
		stateReader core.StateReader
		pool        *providerPool
		declared    []resolver.DeclaredProvider
		checksum    string
	)
	// A provider is only ever needed if at least one discovered resource
	// actually bridged to a lookup shape -- a --tag scope that turns up
	// nothing adoptable at all (every resource "discovered, not yet
	// adoptable") never launches a provider or requires --provider/
	// --source, matching this command's own "identity only, no read
	// unless there's something to read" posture.
	if adoptableCount > 0 {
		if len(opts.Providers) > 0 || len(opts.Config.Providers) > 0 {
			pool, err = newProviderPool(salt, opts.Providers, opts.Config.Providers, opts.ProviderConfigs)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
			}
			defer pool.Close()
			declared, err = declaredProvidersForInference(ctx, pool, resolvedProviderVersions(opts.Config))
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
			}
		} else {
			var closeReader func() error
			stateReader, closeReader, checksum, err = newDiscoveryStateReader(ctx, opts.ProviderPath, opts.Source, opts.ProviderVersion, salt)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
			}
			defer closeReader()
		}
	}

	// Same nextParent chaining runScanAll already established (docs/
	// architecture.md's own "Bulk onboarding" section): nothing gets
	// accepted during this walk, only proposal files get written, so
	// each generated proposal's own Parent is tracked forward by hand.
	nextParent, err := ledger.Head()
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
	}

	skipped := map[string]int{}
	adopted := 0
	redactedAttrs := 0

	for _, d := range discovered {
		name := discoveryResourceName(d.ARN)
		if d.Lookup == nil {
			skipped["discovered, not yet adoptable"]++
			fmt.Fprintf(out, "discovered: %s (%s) -- not yet adoptable: %s\n", d.ARN, orUnknownType(d.TerraformType), d.NotAdoptableReason)
			continue
		}
		addr := core.Address{Stack: opts.Stack, Type: d.TerraformType, Name: name}

		app := stateReader
		providerConfig := json.RawMessage(opts.ProviderConfig)
		providerSource := opts.Source
		if pool != nil {
			winner, ierr := resolver.InferProvider(declared, d.TerraformType, nil)
			if ierr != nil {
				skipped["provider read failed"]++
				fmt.Fprintf(out, "skipped: %s -- %v\n", addr, ierr)
				continue
			}
			var cerr error
			app, providerConfig, cerr = pool.Get(ctx, winner.Source, winner.Version)
			if cerr != nil {
				skipped["provider read failed"]++
				fmt.Fprintf(out, "skipped: %s -- provider unavailable: %v\n", addr, cerr)
				continue
			}
			providerSource = winner.Source
		}

		res, err := core.RunScan(ctx, app, ledger, core.ScanRequest{
			Address:          addr,
			ProviderConfig:   providerConfig,
			CurrentState:     d.Lookup,
			ProviderChecksum: checksum,
			ProviderSource:   providerSource,
		})
		if err != nil {
			// Covers both adversarial row 3 (a specific resource's own
			// type-specific read permission denied -- the tagging API's
			// own broad enumeration already succeeded) and row 4 (the
			// resource was deleted between the tagging API's own list
			// and this read) -- both surface through core.RunScan's own
			// wrapped error, the identical "provider read failed" skip
			// category runScanAll already uses for the equivalent
			// tfstate-sourced case, reused verbatim rather than invented
			// twice.
			skipped["provider read failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		if res.Outcome != core.ScanNew {
			// Adversarial row 5: an already-adopted resource
			// rediscovered. core.RunScan's own existing, unmodified
			// address-based ledger lookup already classifies this --
			// zero new idempotency logic needed.
			skipped["already known to the ledger"]++
			fmt.Fprintf(out, "skipped: %s -- already known to the ledger\n", addr)
			continue
		}

		proposal, err := core.GenerateProposal(ledger, opts.Stack, res)
		if err != nil {
			skipped["proposal generation failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		proposal.Parent = nextParent

		// The attribution bonus (UBI-45, docs/discovery.md): who created
		// this resource, best-effort, reusing the exact same
		// newAttributionBackend registry cli/attribution.go's own drift
		// path already uses, unchanged -- see attributeGenesis's own doc
		// comment for the two real differences from drift attribution.
		if !opts.NoAttribution {
			attributeGenesis(ctx, addr, res.Observed, proposal, providerConfig, providerSource, opts.Config.K8sAudit, d.CreationVerbs)
		}

		hash, err := core.Hash(proposal)
		if err != nil {
			skipped["proposal generation failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		nextParent = hash

		b, err := json.MarshalIndent(proposal, "", "  ")
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: marshal proposal for %s: %w", addr, err)}
		}

		adopted++
		redactedAttrs += core.CountRedacted(res.Observed)
		if opts.OutDir == "" {
			fmt.Fprintf(out, "adopted: %s (from %s)\n%s\n", addr, d.ARN, string(b))
			continue
		}
		outPath := filepath.Join(opts.OutDir, addressFilename(addr)+".json")
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: write %s: %w", outPath, err)}
		}
		fmt.Fprintf(out, "adopted: %s (from %s) -> %s\n", addr, d.ARN, outPath)
	}

	totalSkipped := 0
	for _, n := range skipped {
		totalSkipped += n
	}
	fmt.Fprintf(out, "%d adopted, %d skipped, %d attribute(s) redacted\n", adopted, totalSkipped, redactedAttrs)
	printSkipSummary(out, skipped)
	if totalSkipped > 0 {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan --discover: %d resource(s) skipped (see above)", totalSkipped)}
	}
	return nil
}

// runScanSuggestStacks is `ubx scan --discover --suggest-stacks`'s own
// read-only preview path (docs/discovery.md's "stack-grouping inference
// -- human-reviewable, never auto-assigned"): enumerates exactly like
// runScanDiscover, groups the result, and prints a report -- no ledger,
// no provider, no proposal ever written. The operator reads the report,
// then re-invokes `ubx scan --discover` per suggested group with an
// explicit --stack.
func runScanSuggestStacks(ctx context.Context, out io.Writer, opts scanDiscoverOptions, stackTag string) error {
	if len(opts.Tags) == 0 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover --suggest-stacks requires at least one --tag")}
	}
	api, err := newDiscoveryTaggingAPI(ctx, opts.Region)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover --suggest-stacks: %w", err)}
	}
	discovered, err := discovery.Discover(ctx, api, discovery.Options{Tags: opts.Tags, Types: opts.Types})
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover --suggest-stacks: %w", err)}
	}

	groups := discovery.SuggestStacks(discovered, stackTag)
	for _, g := range groups {
		label := g.Suggestion
		if label == "" {
			label = "(ungrouped)"
		}
		fmt.Fprintf(out, "%s: %d resource(s)\n", label, len(g.Resources))
		names := make([]string, 0, len(g.Resources))
		for _, r := range g.Resources {
			names = append(names, r.ARN)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(out, "  %s\n", n)
		}
	}
	fmt.Fprintf(out, "%d group(s), %d resource(s) total -- nothing written; re-run with --stack <name> per group to adopt\n", len(groups), len(discovered))
	return nil
}

// discoveryResourceName derives a stable, filesystem/address-safe
// resource name from a discovered ARN -- the trailing resource-id
// segment, matching addressFilename's own "safe filename" character
// rule for anything that would otherwise be unusable in a
// core.Address.Name.
func discoveryResourceName(arnString string) string {
	a, err := discovery.ParseARN(arnString)
	if err != nil {
		return arnString
	}
	name := a.ResourceID
	if name == "" {
		name = a.Resource
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func orUnknownType(t string) string {
	if t == "" {
		return "unknown type"
	}
	return t
}

// parseTagFlags parses repeated "key=value" --tag flags into
// discovery.Options.Tags' own map[string][]string shape -- multiple
// flags sharing one key OR-combine (all folded into that key's own
// Values); different keys AND-combine, matching the tagging API's own
// real, documented semantics (discovery/discover.go's own
// tagFiltersFor).
func parseTagFlags(flags []string) (map[string][]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	tags := map[string][]string{}
	for _, f := range flags {
		key, value, ok := strings.Cut(f, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--tag %q: want \"key=value\"", f)
		}
		tags[key] = append(tags[key], value)
	}
	return tags, nil
}
