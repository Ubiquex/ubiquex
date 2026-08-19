package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/provider"
)

// LiveReadProbeConfig describes one live-tier read-only probe run —
// lie-classes 1, 2, and 4's own live halves (docs/conformance-harness.md).
// Deliberately read/rescan-only: nothing in this file ever calls
// ApplyResourceChange or touches core/executor.Ship — creation and
// destruction of the real resource under test stay entirely out of band
// (the `aws`/`gcloud`/`az`/`kubectl` CLI, matching every existing
// RealSafe conformance test's own precedent), the same discipline this
// project has held to for every live conformance session before this
// one. Only probe 3 (destroy honesty) ever reaches the real apply path,
// and only hermetically as of this session — see destroy_probe.go's own
// doc comment.
type LiveReadProbeConfig struct {
	ProviderPath   string
	ProviderDir    string // see AdoptMutateScanDiffConfig.ProviderDir's own doc comment
	Source         string
	Version        string
	Stack          string
	Address        core.Address
	ProviderConfig json.RawMessage
	Timeout        time.Duration
	ProviderEnv    []string
}

// ProbeIdentityShapeLive is lie-class 1's own live confirmation
// (docs/conformance-harness.md — "real create with a full, known
// config; then read back via EACH candidate identity value alone...
// diff every returned attribute against the config that was actually
// set"): reads the real resource once via lookupMinimal (a single
// candidate identity value, e.g. {"id": "..."}) and once via
// lookupFull (every recorded identity value together). Any attribute
// present (non-empty, non-null) under the fuller lookup but missing
// under the minimal one is a live-CONFIRMED incomplete-read finding —
// the exact GCP/UBI-21 silent-incomplete-read class this mechanizes,
// now provable for real rather than found by hand, one type at a time.
// Returns (nil, nil) when lookupFull is empty or byte-identical to
// lookupMinimal (nothing to compare) or when both reads agree.
func ProbeIdentityShapeLive(ctx context.Context, cfg LiveReadProbeConfig, lookupMinimal, lookupFull json.RawMessage) (*Finding, error) {
	if len(lookupFull) == 0 || string(lookupFull) == string(lookupMinimal) {
		return nil, nil
	}
	minimal, err := liveRead(ctx, cfg, lookupMinimal)
	if err != nil {
		return nil, fmt.Errorf("read via minimal lookup: %w", err)
	}
	full, err := liveRead(ctx, cfg, lookupFull)
	if err != nil {
		return nil, fmt.Errorf("read via full lookup: %w", err)
	}

	var minimalMap, fullMap map[string]json.RawMessage
	if err := json.Unmarshal(minimal, &minimalMap); err != nil {
		return nil, fmt.Errorf("decode minimal read: %w", err)
	}
	if err := json.Unmarshal(full, &fullMap); err != nil {
		return nil, fmt.Errorf("decode full read: %w", err)
	}

	var missing []string
	for k, fv := range fullMap {
		if isEmptyOrNullJSON(fv) {
			continue
		}
		mv, ok := minimalMap[k]
		if !ok || isEmptyOrNullJSON(mv) {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) == 0 {
		return nil, nil
	}
	return &Finding{
		Source: cfg.Source, Version: cfg.Version, Type: cfg.Address.Type, Verb: "read",
		Class: FindingIncompleteRead, Tier: TierLive, Confidence: Confirmed,
		Detail: "live-confirmed: " + strings.Join(missing, ",") +
			" read back empty/null via the minimal lookup alone but populated via a fuller lookup -- a genuine silent-incomplete-read, not just a schema-level candidate",
	}, nil
}

// ProbeSensitiveEchoLive is lie-class 2's own live confirmation
// (docs/conformance-harness.md — "plant a unique marker string...
// search EVERY OTHER attribute... for that exact marker string
// appearing verbatim under a DIFFERENT, non-Sensitive-flagged path"):
// reads the real resource (already mutated out of band to carry marker
// somewhere among expectedAttrs, by the caller) and searches every OTHER
// top-level attribute for a verbatim echo. A real, mechanically PROVABLE
// leak — stronger evidence than the hermetic tier's own keyword guess.
//
// expectedAttrs (plural, not a single markerAttr) is deliberate: found
// empirically while live-verifying this probe against a real
// aws_sns_topic (UBI-50 session 3) — planting a marker via a tag call
// populates BOTH "tags" and "tags_all" (AWS's own real, documented
// convention: tags_all is tags merged with any provider-level
// default_tags), a legitimate duplicate by design, not a leak. Excluding
// only the single attribute the marker was nominally planted "in" would
// have produced a false positive on the very first real check this
// probe ever ran against a real resource — corrected here, not left as
// a known false-positive gotcha for every future caller to rediscover.
func ProbeSensitiveEchoLive(ctx context.Context, cfg LiveReadProbeConfig, lookup json.RawMessage, marker string, expectedAttrs ...string) (*Finding, error) {
	observed, err := liveRead(ctx, cfg, lookup)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(observed, &m); err != nil {
		return nil, fmt.Errorf("decode read: %w", err)
	}
	expected := make(map[string]bool, len(expectedAttrs))
	for _, a := range expectedAttrs {
		expected[a] = true
	}
	var leaked []string
	for k, v := range m {
		if expected[k] {
			continue
		}
		if strings.Contains(string(v), marker) {
			leaked = append(leaked, k)
		}
	}
	sort.Strings(leaked)
	if len(leaked) == 0 {
		return nil, nil
	}
	return &Finding{
		Source: cfg.Source, Version: cfg.Version, Type: cfg.Address.Type, Verb: "read",
		Class: FindingSensitiveUnderflag, Tier: TierLive, Confidence: Confirmed,
		Detail: "live-confirmed echo: a marker value planted among " + strings.Join(expectedAttrs, ",") +
			" was found verbatim in " + strings.Join(leaked, ",") + " -- a mechanically proven leak, not a keyword guess",
	}, nil
}

// ProbeDriftLive is lie-class 4's own live confirmation
// (docs/conformance-harness.md — "real create, then a real RESCAN with
// zero real-world mutation in between... if consecutive reads of the
// identical, untouched resource return DIFFERENT values... that
// CONFIRMS undriftable"): adopts addr via a real scan (ScanNew expected)
// then immediately rescans with NO mutation of any kind in between —
// deliberately the opposite of RunAdoptMutateScanDiff's own
// adopt→MUTATE→scan-diff shape. ScanUnchanged on the second scan means
// genuinely stable (not undriftable, however the hermetic tier guessed);
// ScanDrifted CONFIRMS it — the resource is, structurally, incapable of
// reporting a meaningful drift signal (the hashicorp/time class,
// UBI-43's own real, already-live-verified finding).
func ProbeDriftLive(ctx context.Context, l *core.Ledger, cfg LiveReadProbeConfig, lookup json.RawMessage) (*Finding, error) {
	scan := func() (*core.ScanResult, error) {
		return liveScan(ctx, cfg, l, lookup)
	}

	res1, err := scan()
	if err != nil {
		return nil, fmt.Errorf("scan 1 (adopt): %w", err)
	}
	if res1.Outcome != core.ScanNew {
		return nil, fmt.Errorf("scan 1: Outcome = %v, want ScanNew (is %s already tracked from a prior run?)", res1.Outcome, cfg.Address)
	}
	proposal, err := core.GenerateProposal(l, cfg.Stack, res1)
	if err != nil {
		return nil, fmt.Errorf("generate adopt proposal: %w", err)
	}
	if _, err := core.Accept(l, proposal); err != nil {
		return nil, fmt.Errorf("accept adopt: %w", err)
	}

	res2, err := scan()
	if err != nil {
		return nil, fmt.Errorf("scan 2 (no-mutation rescan): %w", err)
	}
	switch res2.Outcome {
	case core.ScanUnchanged:
		return nil, nil
	case core.ScanDrifted:
		return &Finding{
			Source: cfg.Source, Version: cfg.Version, Type: cfg.Address.Type, Verb: "drift",
			Class: FindingUndriftable, Tier: TierLive, Confidence: Confirmed,
			Detail: "live-confirmed: two consecutive reads of the identical, untouched resource (zero mutation between scans) produced different observed state -- genuinely undriftable, the hashicorp/time class",
		}, nil
	default:
		return nil, fmt.Errorf("scan 2: unexpected Outcome %v", res2.Outcome)
	}
}

// liveRead performs one real ReadResource call directly, bypassing
// core.RunScan's own ledger-diff semantics entirely — probes 1 and 2
// are pure "read via the provider, inspect the result" checks with no
// ledger involvement at all, unlike probe 4, which needs RunScan's own
// ScanNew/ScanDrifted/ScanUnchanged classification. Still the real wire
// protocol, still the real Schema/Configure/ReadResource sequence
// core.RunScan itself calls internally — never a shortcut around the
// provider boundary, only around the ledger-diff layer this specific
// check doesn't need.
func liveRead(ctx context.Context, cfg LiveReadProbeConfig, lookup json.RawMessage) (json.RawMessage, error) {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := provider.Launch(readCtx, cfg.ProviderPath, provider.WithEnv(cfg.ProviderEnv...), provider.WithDir(cfg.ProviderDir))
	if err != nil {
		return nil, fmt.Errorf("launch provider: %w", err)
	}
	defer client.Close()

	providerConfig := cfg.ProviderConfig
	if providerConfig == nil {
		providerConfig = json.RawMessage(`{}`)
	}
	schemas, err := client.Provider.Schema(readCtx)
	if err != nil {
		return nil, fmt.Errorf("fetch schema: %w", err)
	}
	if err := client.Provider.Configure(readCtx, schemas.Provider, providerConfig); err != nil {
		return nil, fmt.Errorf("configure: %w", err)
	}
	rs, ok := schemas.Resources[cfg.Address.Type]
	if !ok {
		return nil, fmt.Errorf("type %q not found in schema", cfg.Address.Type)
	}
	return client.Provider.ReadResource(readCtx, rs, cfg.Address.Type, lookup)
}

// liveScan runs one real core.RunScan call — the identical function
// RunAdoptMutateScanDiff's own scan closure already calls, reused
// directly rather than duplicated.
func liveScan(ctx context.Context, cfg LiveReadProbeConfig, l *core.Ledger, lookup json.RawMessage) (*core.ScanResult, error) {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := provider.Launch(scanCtx, cfg.ProviderPath, provider.WithEnv(cfg.ProviderEnv...), provider.WithDir(cfg.ProviderDir))
	if err != nil {
		return nil, fmt.Errorf("launch provider: %w", err)
	}
	defer client.Close()

	salt, err := l.Salt()
	if err != nil {
		return nil, fmt.Errorf("ledger salt: %w", err)
	}
	providerConfig := cfg.ProviderConfig
	if providerConfig == nil {
		providerConfig = json.RawMessage(`{}`)
	}
	return core.RunScan(scanCtx, stateReaderAdapter{p: client.Provider, salt: salt, source: cfg.Source}, l, core.ScanRequest{
		Address:        cfg.Address,
		ProviderConfig: providerConfig,
		CurrentState:   lookup,
	})
}

// isEmptyOrNullJSON reports whether raw decodes to JSON null, an empty
// string, or is itself empty/absent — the shapes a silently-incomplete
// read produces (google_pubsub_topic's own "name": "",
// google_secret_manager_secret's own "secret_id": null).
func isEmptyOrNullJSON(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == ""
	}
	return false
}
