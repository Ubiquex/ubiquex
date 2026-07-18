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

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
	"github.com/ubiquex/ubiquex-cli/provider"
	"github.com/ubiquex/ubiquex-cli/tfstate"
)

// scanAllOptions is ubx scan --all's flag surface, gathered into one
// struct since runScanAll (unlike the single-resource path) has its own
// distinct set of required/optional flags -- see docs/architecture.md's
// "Bulk onboarding" section for the full design this implements.
//
// Providers/ProviderConfigs (UBI-43 session 5) is .ubx/config's own
// [providers]/[provider_configs] tables, non-empty for a real
// multi-provider stack -- when set, every enumerated resource gets its
// provider inferred fresh by type (resolver.InferProvider) against the
// declared set, since a tfstate-imported resource has no ledger history
// of its own to fall back on at all (unlike cli/status.go's mixed
// legacy/explicit Fleet case). ProviderPath/Source/ProviderVersion are
// ignored when Providers is non-empty, mirroring cli/resolve.go's own
// established single-vs-multi branching convention.
type scanAllOptions struct {
	TFStatePath     string
	Stack           string
	Propose         string
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
}

// runScanAll is UBI-18's bulk onboarding: parses a Terraform state file
// once (a border-crossing artifact, never depended on again), then reuses
// the exact same core.RunScan/core.GenerateProposal pipeline a single
// `ubx scan` invocation already runs, once per resource the state file
// names. State provides identity (how to look a resource up), never
// truth -- every proposal's recorded observed state still comes from a
// live ReadResource call.
func runScanAll(ctx context.Context, out io.Writer, opts scanAllOptions) error {
	if opts.Propose != "adopt" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: --propose must be \"adopt\" -- bulk onboarding only ever generates " +
			"adoption proposals (a resource being onboarded for the first time has nothing to revert to)")}
	}

	data, err := os.ReadFile(opts.TFStatePath)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: read state file: %w", err)}
	}
	resources, err := tfstate.Parse(data)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
	}

	stack := opts.Stack
	if stack == "" {
		stack = stackFromStateFilename(opts.TFStatePath)
	}

	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
		}
	}

	ledger, closeLedger, err := openLedgerForStack(ctx, opts.LedgerDir, stack, opts.Config)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
	}
	defer closeLedger()

	salt, err := ledger.Salt()
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
	}

	// docs/architecture.md's own "Bulk onboarding" section, generalized for
	// UBI-43 session 5: a real [providers] table means every enumerated
	// resource gets its provider inferred fresh by type, since a
	// tfstate-imported resource has no ledger history to carry a recorded
	// provider forward from at all (unlike cli/status.go's mixed legacy/
	// explicit Fleet case) -- built eagerly, not lazily, since virtually
	// every resource in a multi-provider walk needs it.
	var (
		stateReader core.StateReader
		pool        *providerPool
		declared    []resolver.DeclaredProvider
		checksum    string
	)
	if len(opts.Providers) > 0 {
		pool, err = newProviderPool(salt, opts.Providers, opts.ProviderConfigs)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
		}
		defer pool.Close()
		declared, err = declaredProvidersForInference(ctx, pool, opts.Providers)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
		}
	} else {
		var path string
		path, checksum, err = resolveProviderBinary(ctx, opts.ProviderPath, opts.Source, opts.ProviderVersion)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
		}
		client, err := provider.Launch(ctx, path)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
		}
		defer client.Close()
		stateReader = newStateReader(client.Provider, salt, opts.Source)
	}

	// core.GenerateProposal sets each proposal's Parent from the ledger's
	// real on-disk head, which never moves during this walk -- nothing
	// gets accepted here, only proposal files get written. Left alone,
	// every one of N generated proposals would carry the exact same
	// (real) parent, valid for only the first of them to ever be
	// accepted. Since a proposal's hash is a pure function of its content
	// (Parent included, Acceptance/ID/Status excluded -- docs/schema.md),
	// nextParent tracks what the head *will* be after accepting every
	// proposal generated so far, in this same order, forming a real,
	// sequentially-acceptable chain the moment the batch is written --
	// not something the operator has to reorder or fix up by hand.
	nextParent, err := ledger.Head()
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: %w", err)}
	}

	skipped := map[string]int{}
	seen := map[core.Address]bool{}
	adopted := 0
	redactedAttrs := 0

	for _, r := range resources {
		name := r.Name
		if r.Module != "" {
			name = r.Module + "." + name
		}
		addr := core.Address{Stack: stack, Type: r.Type, Name: name}

		if seen[addr] {
			skipped["duplicate address"]++
			fmt.Fprintf(out, "skipped: %s -- duplicate address (another state entry already produced this exact address)\n", addr)
			continue
		}
		seen[addr] = true

		lookup, err := tfstate.BuildLookup(r.Type, r.Attributes)
		if err != nil {
			skipped["no identity in state"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}

		app := stateReader
		providerConfig := json.RawMessage(opts.ProviderConfig)
		providerSource := opts.Source
		if pool != nil {
			winner, ierr := resolver.InferProvider(declared, r.Type, nil)
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
			CurrentState:     lookup,
			ProviderChecksum: checksum,
			ProviderSource:   providerSource,
		})
		if err != nil {
			skipped["provider read failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		if res.Outcome != core.ScanNew {
			skipped["already known to the ledger"]++
			fmt.Fprintf(out, "skipped: %s -- already known to the ledger\n", addr)
			continue
		}

		proposal, err := core.GenerateProposal(ledger, stack, res)
		if err != nil {
			skipped["proposal generation failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		if r.Module != "" {
			proposal.Intent.Summary += fmt.Sprintf(" (from module %s)", r.Module)
		}
		proposal.Parent = nextParent

		hash, err := core.Hash(proposal)
		if err != nil {
			skipped["proposal generation failed"]++
			fmt.Fprintf(out, "skipped: %s -- %v\n", addr, err)
			continue
		}
		nextParent = hash

		b, err := json.MarshalIndent(proposal, "", "  ")
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: marshal proposal for %s: %w", addr, err)}
		}

		adopted++
		redactedAttrs += core.CountRedacted(res.Observed)
		if opts.OutDir == "" {
			fmt.Fprintf(out, "adopted: %s\n%s\n", addr, string(b))
			continue
		}
		outPath := filepath.Join(opts.OutDir, addressFilename(addr)+".json")
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: write %s: %w", outPath, err)}
		}
		fmt.Fprintf(out, "adopted: %s -> %s\n", addr, outPath)
	}

	totalSkipped := 0
	for _, n := range skipped {
		totalSkipped += n
	}
	fmt.Fprintf(out, "%d adopted, %d skipped, %d attribute(s) redacted\n", adopted, totalSkipped, redactedAttrs)
	printSkipSummary(out, skipped)
	if totalSkipped > 0 {
		// Skips are a normal, working-as-designed outcome of a bulk walk (a
		// resource with no identity in state, a duplicate address, ...) --
		// an actionable finding for the operator to look at, not a failed
		// walk (UBI-20 exit-code contract).
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan --all: %d resource(s) skipped (see above)", totalSkipped)}
	}
	return nil
}

// printSkipSummary prints one line per skip reason, sorted for
// deterministic output -- docs/architecture.md: "recorded with its
// address and reason in a skipped-summary."
func printSkipSummary(out io.Writer, skipped map[string]int) {
	if len(skipped) == 0 {
		return
	}
	reasons := make([]string, 0, len(skipped))
	for reason := range skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	fmt.Fprintln(out, "--- skipped ---")
	for _, reason := range reasons {
		fmt.Fprintf(out, "%s: %d\n", reason, skipped[reason])
	}
}

// stackFromStateFilename derives a default stack name from the state
// file's own path when --stack isn't given: the basename with its
// extension stripped ("prod.tfstate" -> "prod"), falling back to
// "default" if that's empty or unusable.
func stackFromStateFilename(path string) string {
	base := filepath.Base(path)
	stack := strings.TrimSuffix(base, filepath.Ext(base))
	if stack == "" || stack == "." || stack == string(filepath.Separator) {
		return "default"
	}
	return stack
}

// addressFilename renders addr as a filesystem-safe filename: anything
// that isn't alphanumeric, '.', '_', or '-' becomes '_' -- a for_each
// index key can contain arbitrary characters.
func addressFilename(addr core.Address) string {
	s := addr.String()
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
