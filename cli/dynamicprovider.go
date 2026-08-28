// Real gap this file fixes, found and named explicitly during the
// provider-onboarding-pipeline effort's own first checkpoint (STATE.md):
// `ubx sdk gen` (sdk.go) only ever knew how to generate bindings against a
// real, independently-versioned Terraform-registry provider binary
// (provider.Acquire). The new onboarding pipeline's own six real targets
// (AWS/Azure/GCP/Kubernetes/GitHub/Datadog) are all served by the SAME,
// single, shared ubx-provider-dynamic binary instead, differentiated only
// by which [dynamic_providers.<name>] config table is active -- there is
// no independent per-target binary/version to acquire from a registry at
// all. This file is the missing launch path for that real, different
// shape, reusing the identical [dynamic_providers.<name>] table format
// (see Config.DynamicProviders' own doc comment) and the identical real
// provider.Launch/provider.WithDir mechanism UBI-158 Phase 5's own
// conformance work already proved against ubx-provider-dynamic.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/provider"
)

// dynamicProviderHandshakeTimeout overrides provider.Launch's own 10s
// default specifically for ubx-provider-dynamic -- that default is
// calibrated for ordinary, hand-written Terraform provider binaries
// (which handshake almost instantly), not for a group container that
// loads, parses, and translates every real member's own schema from
// disk before its first RPC response. Confirmed live, not guessed:
// Azure's own real, published 604-member group (the largest real group
// any provider in this org has) took ~54-56s wall time to reach its
// handshake line, both on a cold cache (download + extract + merge) AND
// on a cache hit with network poisoned (merge alone) -- the real cost
// here is dominated by parsing and translating 604 real, individually
// bundled (UBI-193's own external-$ref work necessarily made each one
// larger) OpenAPI documents, not network I/O, confirmed by the cache-hit
// case costing almost exactly the same wall time as the cold-cache one.
// 120s leaves real, honest margin over the measured worst case, not a
// number picked to just barely clear it -- a real, separate, not-yet-
// investigated performance question (85MB/604 members taking almost a
// minute to merge even from local disk) is named here, not silently
// hidden behind a timeout bump.
const dynamicProviderHandshakeTimeout = 120 * time.Second

// sortedDynamicProviderNames mirrors sortedProviderSources (providerpool.go)
// for Config.DynamicProviders -- the same determinism discipline every
// other map-keyed config walk in this codebase already applies.
func sortedDynamicProviderNames(m map[string]map[string]any) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// dynamicProviderBinaryCache memoizes one real, on-demand `go build` of
// ubx-provider-dynamic per process run -- every declared
// [dynamic_providers.<name>] entry shares the identical binary (they
// differ only in which config table is active at launch time, never in
// which binary), so building it once and reusing the path for every
// entry is both correct and the only sane cost profile for a provider
// with dozens of declared dynamic-provider entries.
var (
	dynamicProviderBinaryOnce sync.Once
	dynamicProviderBinaryPath string
	dynamicProviderBinaryErr  error
)

// resolveDynamicProviderBinary returns a real, usable ubx-provider-dynamic
// binary path -- explicitPath verbatim if given (a caller-supplied,
// already-built binary, the real, simple, no-magic path for local
// dev/CI once a real, pinned release process for ubx-provider-dynamic
// itself exists), otherwise built on demand from a local checkout at
// repoPath (UBX_PROVIDER_DYNAMIC_REPO, mirroring
// conformance/dynamic_provider_live_test.go's own identical, already-
// proven real build-on-demand pattern -- ubx-provider-dynamic is a
// separate Go module, so this cannot be a plain relative-package build
// the way provider/internal/fakeprovider's own single-module fixture is).
//
// Real, honest, deliberate scope gap, named not hidden: there is no real,
// pinned-version resolution here yet (no equivalent of provider.Acquire's
// own real version pin against a registry) -- ubx-provider-dynamic has no
// real release/tag process of its own today. Building from whatever the
// local checkout's own current HEAD is is the honest, current state of
// the world, not a placeholder pretending otherwise; real version pinning
// is real, separate, future work once ubx-provider-dynamic itself adopts
// one (the central provider-config checkpoint this session's own STATE.md
// entry names as still ahead).
func resolveDynamicProviderBinary(explicitPath, repoPath string) (string, error) {
	if explicitPath != "" {
		if _, err := os.Stat(explicitPath); err != nil {
			return "", fmt.Errorf("--dynamic-provider-bin %q: %w", explicitPath, err)
		}
		return explicitPath, nil
	}

	dynamicProviderBinaryOnce.Do(func() {
		if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err != nil {
			dynamicProviderBinaryErr = fmt.Errorf("no ubx-provider-dynamic checkout found at %q (set --dynamic-provider-bin to an already-built binary, or UBX_PROVIDER_DYNAMIC_REPO to a real local checkout): %w", repoPath, err)
			return
		}
		dir, err := os.MkdirTemp("", "ubx-sdk-gen-dynamic-provider")
		if err != nil {
			dynamicProviderBinaryErr = err
			return
		}
		bin := filepath.Join(dir, "ubx-provider-dynamic")
		build := exec.Command("go", "build", "-o", bin, "./cmd/ubx-provider-dynamic")
		build.Dir = repoPath
		var stderr bytes.Buffer
		build.Stderr = &stderr
		if err := build.Run(); err != nil {
			dynamicProviderBinaryErr = fmt.Errorf("building ubx-provider-dynamic from %s: %w\n%s", repoPath, err, stderr.String())
			return
		}
		dynamicProviderBinaryPath = bin
	})
	return dynamicProviderBinaryPath, dynamicProviderBinaryErr
}

// defaultDynamicProviderRepo mirrors conformance/dynamic_provider_live_test.go's
// own identical real default -- go test's/ubx's own cwd for this
// invocation is the ubiquex repo root, and ubx-provider-dynamic lives
// beside it, not inside it.
const defaultDynamicProviderRepo = "../ubx-provider-dynamic"

// dynamicProviderProvenance is UBI-197's own real fix: resolveDynamicProviderBinary's
// own doc comment already named the gap ("there is no real, pinned-version
// resolution here yet... building from whatever the local checkout's own
// current HEAD is is the honest, current state of the world") but never
// recorded what that HEAD actually was anywhere a later session could
// check. Confirmed live this session: a real, unmerged WIP branch
// (collection-envelope unwrapping, openapi data sources only) was live in
// the local checkout for less than an hour, produced the entire docs
// corpus's own data-source naming during that window, and left no trace
// once its own commits were superseded -- every subsequent regeneration
// (including this session's) silently disagreed with the published corpus
// and there was no record anywhere explaining why. Same discipline UBI-194
// already established for the real, pinned-release path (a resolvable
// version + checksum) applied one layer up, to the local-checkout-build
// path this session's own investigation showed is what every real
// generation this session (and, going by file mtimes, the docs corpus's
// own past batch runs) actually used.
type dynamicProviderProvenance struct {
	// Source is "local-checkout" (built on demand from a real git checkout,
	// RepoPath/Commit/Dirty/Unpushed all meaningful) or "explicit-binary"
	// (--dynamic-provider-bin given directly -- no checkout to inspect, so
	// provenance is honestly unknown beyond the path itself).
	Source   string `json:"source"`
	RepoPath string `json:"repo_path,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Dirty    bool   `json:"dirty"`
	Unpushed bool   `json:"unpushed"`

	// UBI-199: schema-source provenance, genuinely per-provider within a
	// single `ubx sdk gen` invocation (unlike Source/RepoPath/Commit/
	// Dirty/Unpushed above, which describe the shared ubx-provider-dynamic
	// TOOL checkout, resolved once and cached for the whole process). A
	// caller with a specific provider's own real params in scope
	// (generateOneDynamicProvider, generateDynamicProviderGroup) sets
	// these on ITS OWN local copy of the process-wide value before
	// marshaling -- never on the cached singleton itself, which stays
	// tool-only. SchemaPinned=false with SchemaURL set means a real,
	// unpinned schema_url fetch (a legitimate mode -- see
	// sdk/providers/.ubx/config's own top-of-file comment on onboarding a
	// new provider); SchemaPinned=true means source/version, resolved via
	// provider.AcquireSchema against a real published ubx-schema-<name>
	// snapshot, zero network on a cache hit. A record with neither set at
	// all (the shape every PROVENANCE.json written before this fix has)
	// must read as unknown, not as implicitly pinned -- see
	// ubiquex-docs' own provenance_check.py, which now refuses on that
	// basis exactly like it already refuses on a missing file.
	SchemaPinned  bool   `json:"schema_pinned"`
	SchemaSource  string `json:"schema_source,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	SchemaURL     string `json:"schema_url,omitempty"`
	// SchemaNote carries a real, human-readable explanation when
	// SchemaPinned's own single bool can't tell the whole story -- today
	// that's exactly one real case: a [dynamic_provider_groups.<name>]
	// group whose members don't uniformly agree on one pinned
	// source/version (or mix pinned and live members), which must not
	// read as cleanly pinned just because SOME members are.
	SchemaNote string `json:"schema_note,omitempty"`
}

// schemaProvenanceFields computes ONE provider entry's own real schema-
// source provenance from its real params -- pinned (source+version,
// the identical presence check pinnedSchemaFields already makes, reused
// rather than duplicated) or live (schema_url, honestly recorded so an
// onboarding provider's real, intentional live-fetch state is visible,
// not hidden).
func schemaProvenanceFields(params map[string]any) (pinned bool, source, version, url string, err error) {
	src, ver, ok, err := pinnedSchemaFields(params)
	if err != nil {
		return false, "", "", "", err
	}
	if ok {
		return true, src, ver, "", nil
	}
	rawURL, _ := params["schema_url"].(string)
	return false, "", "", rawURL, nil
}

// groupSchemaProvenanceFields is schemaProvenanceFields' own real group
// aggregate: a [dynamic_provider_groups.<name>] group's schema
// provenance is only genuinely "pinned" when EVERY real member is
// pinned AND every pinned member names the identical (source, version)
// -- the shape all six real providers converged on this session
// (UBI-182 Stage F's own single-pin collapse). A member disagreeing, or
// any member still live, means the group as a whole is not one coherent,
// reproducible fetch -- recorded as unpinned with SchemaNote explaining
// why, never silently reporting whichever member happened to be checked
// first.
func groupSchemaProvenanceFields(memberNames []string, dynamicProviders map[string]map[string]any) (pinned bool, source, version, note string, err error) {
	allPinned := true
	agree := true
	var firstSource, firstVersion string
	for i, name := range memberNames {
		p, src, ver, _, ferr := schemaProvenanceFields(dynamicProviders[name])
		if ferr != nil {
			return false, "", "", "", fmt.Errorf("member %q: %w", name, ferr)
		}
		if !p {
			allPinned = false
			continue
		}
		if i == 0 {
			firstSource, firstVersion = src, ver
		} else if src != firstSource || ver != firstVersion {
			agree = false
		}
	}
	if allPinned && agree {
		return true, firstSource, firstVersion, "", nil
	}
	return false, "", "", fmt.Sprintf(
		"group has %d member(s), not uniformly pinned to one real source/version (allPinned=%v agree=%v)",
		len(memberNames), allPinned, agree,
	), nil
}

// clean reports whether this provenance record positively confirms the
// binary was built from a real, immutable, fetchable commit -- the one
// condition --require-clean-provenance is willing to accept.
func (p dynamicProviderProvenance) clean() bool {
	return p.Source == "local-checkout" && !p.Dirty && !p.Unpushed
}

var (
	dynamicProviderProvenanceOnce   sync.Once
	dynamicProviderProvenanceResult dynamicProviderProvenance
	dynamicProviderProvenanceErr    error
)

// resolveDynamicProviderProvenance mirrors resolveDynamicProviderBinary's
// own sync.Once caching (one binary, one provenance record, shared by
// every declared entry in a single `ubx sdk gen` invocation) -- explicitPath
// and repoPath are the identical two inputs that decide what binary got
// built, so the same two inputs decide what provenance record describes
// it.
func resolveDynamicProviderProvenance(explicitPath, repoPath string) (dynamicProviderProvenance, error) {
	dynamicProviderProvenanceOnce.Do(func() {
		dynamicProviderProvenanceResult, dynamicProviderProvenanceErr = computeDynamicProviderProvenance(explicitPath, repoPath)
	})
	return dynamicProviderProvenanceResult, dynamicProviderProvenanceErr
}

// computeDynamicProviderProvenance is resolveDynamicProviderProvenance's
// own real logic, factored out uncached so tests can exercise it directly
// against a real, hermetic git repo fixture without fighting the
// package-level sync.Once every real invocation shares (one binary, one
// provenance record, per `ubx sdk gen` process -- see
// resolveDynamicProviderProvenance's own doc comment).
func computeDynamicProviderProvenance(explicitPath, repoPath string) (dynamicProviderProvenance, error) {
	if explicitPath != "" {
		return dynamicProviderProvenance{Source: "explicit-binary"}, nil
	}
	commit, err := runDynamicProviderGit(repoPath, "rev-parse", "HEAD")
	if err != nil {
		return dynamicProviderProvenance{}, fmt.Errorf("resolving ubx-provider-dynamic commit at %q: %w", repoPath, err)
	}
	status, err := runDynamicProviderGit(repoPath, "status", "--porcelain")
	if err != nil {
		return dynamicProviderProvenance{}, fmt.Errorf("checking ubx-provider-dynamic working tree at %q: %w", repoPath, err)
	}
	// Unpushed: no upstream configured at all is treated the same as real
	// commits ahead of one -- a commit nobody else can fetch is exactly
	// the same real risk this record exists to catch, whether or not a
	// tracking branch happens to be set (a detached HEAD, or a local-only
	// branch, both hit this branch).
	unpushed := true
	if _, uerr := runDynamicProviderGit(repoPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); uerr == nil {
		ahead, aerr := runDynamicProviderGit(repoPath, "rev-list", "@{u}..HEAD", "--count")
		if aerr != nil {
			return dynamicProviderProvenance{}, fmt.Errorf("checking ubx-provider-dynamic ahead-count at %q: %w", repoPath, aerr)
		}
		unpushed = strings.TrimSpace(ahead) != "0"
	}
	return dynamicProviderProvenance{
		Source:   "local-checkout",
		RepoPath: repoPath,
		Commit:   commit,
		Dirty:    status != "",
		Unpushed: unpushed,
	}, nil
}

func runDynamicProviderGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// checkDynamicProviderProvenance prints this invocation's real provenance
// once, unconditionally, to stderr -- a clean run's own confirmation is
// worth seeing too, not just a dirty run's warning. When prov is not
// clean(), prints a loud, distinct warning block by default (generation
// proceeds -- the local-checkout-build path exists specifically so someone
// can iterate on ubx-provider-dynamic itself and see the effect
// immediately, which requires a dirty tree by construction; refusing here
// unconditionally would block that real, intended workflow). requireClean
// turns that same condition into a hard refusal instead -- the guarantee a
// real regeneration meant to be committed or published should ask for
// explicitly (CI, a batch docs-corpus regeneration), not the default for
// every interactive invocation.
func checkDynamicProviderProvenance(w io.Writer, prov dynamicProviderProvenance, requireClean bool) error {
	if prov.clean() {
		fmt.Fprintf(w, "sdk gen: ubx-provider-dynamic provenance: local-checkout commit=%s (clean, pushed) repo=%s\n", prov.Commit, prov.RepoPath)
		return nil
	}

	var detail string
	switch {
	case prov.Source == "explicit-binary":
		detail = "an explicit --dynamic-provider-bin path was given -- no checkout to inspect, provenance is honestly unknown beyond that path"
	case prov.Dirty && prov.Unpushed:
		detail = fmt.Sprintf("commit=%s at %s has uncommitted changes AND is not pushed to any upstream", prov.Commit, prov.RepoPath)
	case prov.Dirty:
		detail = fmt.Sprintf("commit=%s at %s has uncommitted changes", prov.Commit, prov.RepoPath)
	default:
		detail = fmt.Sprintf("commit=%s at %s is not pushed to any upstream", prov.Commit, prov.RepoPath)
	}

	if requireClean {
		return fmt.Errorf("ubx-provider-dynamic provenance is not clean: %s -- refusing (--require-clean-provenance); commit and push first, or drop the flag to generate anyway with a loud warning", detail)
	}

	fmt.Fprintf(w, "sdk gen: WARNING -- ubx-provider-dynamic provenance is not clean: %s\n", detail)
	fmt.Fprintf(w, "sdk gen: WARNING -- generated output cannot be traced back to real, fetchable code. Fine for local iteration; do not publish this output as-is.\n")
	return nil
}

// pinnedSchemaFields extracts "source"/"version" from a
// [providers.<name>]/[dynamic_providers.<name>] entry's own params --
// real, mechanical presence-based detection of which of the two real
// entry shapes this is. The pinned shape (source+version,
// provider.AcquireSchema-backed, zero network at schema resolution time)
// and the live shape (schema_source/schema_url,
// writeDynamicProviderConfig-backed, a real fetch on every launch) never
// share a field name, so checking for "source" alone decides,
// unambiguously, which one a given entry is -- never inferred from
// anything else.
//
// version is deliberately required the moment source is present: a
// pinned entry with no version would have to mean "latest," and
// provider.AcquireSchema (like provider.Acquire one level down) never
// does that resolution -- an explicit version is what makes "reconstruct
// the schema a past build used" possible at all, the entire reason this
// mechanism exists (see internal/snapshot's own doc comment,
// ubx-provider-dynamic).
func pinnedSchemaFields(params map[string]any) (source, version string, ok bool, err error) {
	rawSource, hasSource := params["source"]
	if !hasSource {
		return "", "", false, nil
	}
	source, isString := rawSource.(string)
	if !isString {
		return "", "", false, fmt.Errorf("\"source\" must be a string, got %T", rawSource)
	}
	rawVersion, hasVersion := params["version"]
	if !hasVersion {
		return "", "", false, fmt.Errorf("%q declares \"source\" but no \"version\" -- a pinned schema source requires an explicit version, never \"latest\"", source)
	}
	version, isString = rawVersion.(string)
	if !isString {
		return "", "", false, fmt.Errorf("\"version\" must be a string, got %T", rawVersion)
	}
	return source, version, true, nil
}

// acquirePinnedSchemaEnv is the pinned shape's own real work (source+
// version already confirmed present) -- provider.AcquireSchema resolves
// a verified, cached, or freshly-downloaded-and-verified snapshot file
// with zero involvement from a launch workDir at all, since the
// launched process reads UBX_SNAPSHOT_PATH directly (main.go's own
// snapshotPathEnvVar branch, checked before .ubx/config would even be
// loaded). Used by dynamicProviderEnv's own pinned branch
// ([dynamic_providers.<name>], real but with zero real, live usage
// today -- confirmed directly against sdk/providers/.ubx/config, not
// assumed -- so it keeps resolving its own binary the old way, an
// explicit, named, low-risk scope boundary, not a silent gap). See
// acquirePinnedSchemaAndBinary for [providers.<name>]'s own real path,
// which additionally resolves and acquires the real binary the
// snapshot itself requires (UBI-194).
func acquirePinnedSchemaEnv(ctx context.Context, name, src, version string) ([]string, error) {
	result, err := acquireSchemaResult(ctx, name, src, version)
	if err != nil {
		return nil, err
	}
	return []string{"UBX_DYNAMIC_PROVIDER_NAME=" + name, "UBX_SNAPSHOT_PATH=" + result.Path}, nil
}

func acquireSchemaResult(ctx context.Context, name, src, version string) (*provider.AcquireSchemaResult, error) {
	schemaSrc, err := provider.ParseSchemaSource(src)
	if err != nil {
		return nil, fmt.Errorf("%q.source: %w", name, err)
	}
	result, err := provider.AcquireSchema(ctx, schemaSrc, version)
	if err != nil {
		return nil, fmt.Errorf("acquire schema for %q: %w", name, err)
	}
	return result, nil
}

// acquirePinnedSchemaAndBinary is [providers.<name>]'s own real, full
// resolution (UBI-194): acquires the real, verified schema snapshot
// (acquireSchemaResult, the same real work acquirePinnedSchemaEnv
// does), then resolves the real ubx-provider-dynamic binary path. UBI-194:
// this is what removes the UBX_PROVIDER_DYNAMIC_REPO/local-checkout
// dependency from the real, normal [providers.<name>] path -- the
// NORMAL case resolves and acquires the exact real binary the
// snapshot itself requires (provider.ResolveDynamicProviderBinaryVersion
// reads the snapshot's own real, generation-time-stamped
// min_binary_version; provider.AcquireDynamicProviderBinary fetches it,
// mirror-then-cache-then-verify, the identical real discipline
// AcquireSchema/Acquire already use). UBX_PROVIDER_DYNAMIC_REPO stays
// real and useful for local development, exactly as the ticket asks --
// checked FIRST, as a real, explicit override: if set, this builds
// from that checkout instead (resolveAmbientDynamicProviderBinary,
// unchanged), the same real escape hatch every other real dynamic-
// provider call site already has, letting a real, unreleased
// ubx-provider-dynamic change be tested against an already-published
// snapshot before it's ever cut into a real release. Both real
// [providers.<name>] consumers (newDynamicProviderLaunchFunc's own
// providerPool.Get path, and loadDynamicProviderSchema's own ubx
// resolve/ubx plan path) share this one function so binary resolution
// can never drift between them.
func acquirePinnedSchemaAndBinary(ctx context.Context, name string, params map[string]any) (binPath string, env []string, err error) {
	src, version, pinned, err := pinnedSchemaFields(params)
	if err != nil {
		return "", nil, fmt.Errorf("[providers.%s]: %w", name, err)
	}
	if !pinned {
		return "", nil, fmt.Errorf("[providers.%s] must be pinned (\"source\" and \"version\" both required) -- live-fetch config (schema_source/schema_url/...) belongs under [dynamic_providers.%s] instead, never [providers.%s]", name, name, name)
	}

	schemaResult, err := acquireSchemaResult(ctx, name, src, version)
	if err != nil {
		return "", nil, err
	}
	env = []string{"UBX_DYNAMIC_PROVIDER_NAME=" + name, "UBX_SNAPSHOT_PATH=" + schemaResult.Path}

	if os.Getenv("UBX_PROVIDER_DYNAMIC_REPO") != "" {
		binPath, err = resolveAmbientDynamicProviderBinary()
		if err != nil {
			return "", nil, err
		}
		return binPath, env, nil
	}

	binVersion, err := provider.ResolveDynamicProviderBinaryVersion(schemaResult.Path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve ubx-provider-dynamic version for %q: %w", name, err)
	}
	binResult, err := provider.AcquireDynamicProviderBinary(ctx, binVersion)
	if err != nil {
		return "", nil, fmt.Errorf("acquire ubx-provider-dynamic@%s for %q: %w", binVersion, name, err)
	}
	return binResult.Path, env, nil
}

// dynamicProviderEnv resolves the real env vars a launched
// ubx-provider-dynamic process needs to serve name (a real
// [dynamic_providers.<name>] entry -- see acquirePinnedSchemaAndBinary
// for [providers.<name>]'s own, now-pinned-only, separate real path), and
// prepares workDir to match. Two real, mutually exclusive shapes, chosen
// by pinnedSchemaFields -- both still genuinely real here: a
// [dynamic_providers.<name>] entry drives hash-watch.yml-style live
// regeneration (schema_source/schema_url/...) in the overwhelming real
// case, but a real, pinned one (source+version) is not refused either,
// exactly as before UBI-182's own [providers.<name>] collapse:
//
//   - Pinned (source+version present): acquirePinnedSchemaEnv.
//   - Live (existing shape, schema_source/schema_url/...): unchanged --
//     writeDynamicProviderConfig writes the real, temporary
//     [dynamic_providers.<name>] table the launched process fetches
//     schema_url through, exactly as before this function existed.
func dynamicProviderEnv(ctx context.Context, workDir, name string, params map[string]any) ([]string, error) {
	src, version, pinned, err := pinnedSchemaFields(params)
	if err != nil {
		return nil, fmt.Errorf("[dynamic_providers.%s]: %w", name, err)
	}
	if pinned {
		return acquirePinnedSchemaEnv(ctx, name, src, version)
	}

	if err := writeDynamicProviderConfig(workDir, name, params); err != nil {
		return nil, err
	}
	return []string{"UBX_DYNAMIC_PROVIDER_NAME=" + name}, nil
}

// dynamicProviderSchema launches ubx-provider-dynamic once against name's
// own real [dynamic_providers.<name>] config entry (params -- live-
// shaped or pinned-shaped, dynamicProviderEnv decides which), and
// returns its real GetProviderSchema dump -- no Configure call, no real
// credentials needed, matching this whole file's own real "schema dump
// only" scope. See loadDynamicProviderSchema for [providers.<name>]'s
// own, separate, pinned-only real entry point -- the two tables keep
// their own real env-resolution (dynamicProviderEnv vs
// acquirePinnedSchemaAndBinary) but share launchAndFetchSchema's own real
// launch/fetch/observability-note mechanics, so that part can never
// silently diverge between them.
func dynamicProviderSchema(ctx context.Context, binPath, name string, params map[string]any) (*provider.Schemas, error) {
	workDir, err := os.MkdirTemp("", "ubx-sdk-gen-"+name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	env, err := dynamicProviderEnv(ctx, workDir, name, params)
	if err != nil {
		return nil, err
	}
	return launchAndFetchSchema(ctx, binPath, name, workDir, env)
}

// launchAndFetchSchema is dynamicProviderSchema's and
// loadDynamicProviderSchema's own real, shared tail: launch the already-
// resolved env against binPath, fetch the real GetProviderSchema dump,
// surface real skip-reason notes, close. workDir is only ever real for
// [dynamic_providers.<name>]'s own live-fetch shape (the launched
// process reads .ubx/config from its own cwd there) -- empty for
// [providers.<name>]'s own pinned-only shape, which never touches a
// workDir at all (UBX_SNAPSHOT_PATH is self-sufficient), so Launch is
// left to inherit the caller's own cwd rather than pointing it at a
// real, empty temp directory for no reason.
func launchAndFetchSchema(ctx context.Context, binPath, name, workDir string, env []string) (*provider.Schemas, error) {
	opts := []provider.Option{
		provider.WithEnv(env...),
		provider.WithHandshakeTimeout(dynamicProviderHandshakeTimeout),
	}
	if workDir != "" {
		opts = append(opts, provider.WithDir(workDir))
	}
	client, err := provider.Launch(ctx, binPath, opts...)
	if err != nil {
		return nil, fmt.Errorf("launch ubx-provider-dynamic for %q: %w", name, err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch schema for dynamic provider %q: %w", name, err)
	}
	// Real observability gap, found while reconciling a real GCP coverage
	// discrepancy (UBI-175): ubx-provider-dynamic's own discoverydoc/
	// cloudformation/resourcemap Build() and Discover() calls already
	// print a real, specific skip-reason Note per excluded candidate
	// resource ("no matching create", "already claimed", "no usable
	// schema", ...) to the launched subprocess's own stderr -- but
	// Client.Stderr() (provider/client.go), which captures exactly that
	// output, was never called by any caller until now, so every one of
	// those notes was silently discarded on every real `ubx sdk gen`/
	// `ubx resolve`/`ubx ship` run. Surfacing them here, once, at the
	// single real chokepoint every dynamic-provider schema fetch already
	// goes through, rather than at each of dynamicProviderSchema's own
	// several call sites separately.
	// client.Stderr() captures the subprocess's ENTIRE stderr, including
	// go-plugin/tf6server's own internal trace-level JSON protocol
	// logging and unrelated warnings -- real noise, not notes. Only the
	// lines carrying main.go's own real "ubx-provider-dynamic: " prefix
	// (every one of its genuine skip-reason Note prints, across
	// discoverydoc/cloudformation/resourcemap/smithy alike) are the
	// actual diagnostic surface this fix exists for.
	for _, line := range strings.Split(client.Stderr(), "\n") {
		if strings.HasPrefix(line, "ubx-provider-dynamic: ") {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	return schemas, nil
}

// resolveAmbientDynamicProviderBinary resolves a real, usable
// ubx-provider-dynamic binary using only the ambient
// UBX_PROVIDER_DYNAMIC_REPO env var (or defaultDynamicProviderRepo) --
// the shared real binPath-resolution logic newDynamicProviderLaunchFunc
// and loadDynamicProviderSchema both need, factored out rather than
// duplicated a third time. See newDynamicProviderLaunchFunc's own doc
// comment for the real, honest scope boundary this implies (no
// --dynamic-provider-bin-equivalent flag for ubx resolve/ubx ship/etc.
// yet).
func resolveAmbientDynamicProviderBinary() (string, error) {
	repoPath := os.Getenv("UBX_PROVIDER_DYNAMIC_REPO")
	if repoPath == "" {
		repoPath = defaultDynamicProviderRepo
	}
	return resolveDynamicProviderBinary("", repoPath)
}

// loadDynamicProviderSchema is `ubx resolve`'s own real entry point for
// fetching a [providers.<name>] entry's real schema (cli/resolve.go's
// own loadResolveProviders) -- resolves BOTH the real snapshot and the
// real ubx-provider-dynamic binary that snapshot itself requires via
// acquirePinnedSchemaAndBinary (UBI-194: no UBX_PROVIDER_DYNAMIC_REPO/
// local-checkout dependency at all on this, the real, normal
// [providers.<name>] path), then shares launchAndFetchSchema's own
// real launch/fetch/observability-note mechanics with
// dynamicProviderSchema (schema-fetch-then-close, the identical real
// shape resolve's own thirdparty branch already has for a real
// Terraform-registry provider).
func loadDynamicProviderSchema(ctx context.Context, name string, params map[string]any) (*provider.Schemas, error) {
	binPath, env, err := acquirePinnedSchemaAndBinary(ctx, name, params)
	if err != nil {
		return nil, err
	}
	return launchAndFetchSchema(ctx, binPath, name, "", env)
}

// dynamicProviderSignals runs the SAME ubx-provider-dynamic binary a
// second time, as a real, plain subprocess -- --dump-signals (see that
// flag's own doc comment in ubx-provider-dynamic's cmd/ubx-provider-dynamic/main.go
// for why this can't ride the same provider.Launch/go-plugin connection
// dynamicProviderSchema above uses: go-plugin's own real handshake
// protocol already owns that process's stdout). Returns a real, honestly
// EMPTY (not nil, not an error) result for a schema_source this binary
// doesn't yet extract signals for (Smithy -- AWS data sources, today),
// matching that binary's own "skip, don't fail" answer.
//
// UBI-182: reuses dynamicProviderEnv, the identical real pinned/live
// branch dynamicProviderSchema already goes through -- a pinned entry
// (source+version) now resolves a real snapshot via provider.AcquireSchema
// and the subprocess reads it via UBX_SNAPSHOT_PATH (main.go's own
// snapshotPathEnvVar branch, wired into --dump-signals as of UBI-182's
// ubx-provider-dynamic Stage B), the same way it already reads
// UBX_SNAPSHOT_PATH for real tfplugin6 serving. No more hard refusal for
// a pinned entry.
func dynamicProviderSignals(ctx context.Context, binPath, name string, params map[string]any) (map[string]map[string]*fieldSignal, error) {
	workDir, err := os.MkdirTemp("", "ubx-sdk-gen-signals-"+name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	env, err := dynamicProviderEnv(ctx, workDir, name, params)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binPath, "--dump-signals")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("dump signals for dynamic provider %q: %w\n%s", name, err, stderr.String())
	}

	var out map[string]map[string]*fieldSignal
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse signal output for dynamic provider %q: %w", name, err)
	}
	return out, nil
}

// dynamicProviderNamespaces is dynamicProviderSignals' own real sibling
// for UBI-98's fix: --dump-namespaces (ubx-provider-dynamic's own doc
// comment on that flag has the full real account) returns a real,
// authoritative per-resource-type service identity -- CloudFormation's
// own namespace field for AWS resources, Smithy's own endpointPrefix
// trait for AWS data sources later -- that sdk/codegen/ir's own
// ServiceAndLocalNameForType uses instead of guessing from a mechanical
// split of the flat wire type name. Empty (real, not an error) for
// every provider that never had this problem: Azure/GCP/Kubernetes/
// GitHub/Datadog build their wire type fresh from real identity
// already, confirmed live against all 1,096 real Azure and 1,543 real
// Google wire types (zero true mismatches).
//
// Identical real launch shape to dynamicProviderSignals -- same
// dynamicProviderEnv pinned/live branch, same subprocess, same
// non-fatal-on-error treatment by this function's own caller
// (generateOneDynamicProvider/generateDynamicProviderGroup already treat
// a dynamicProviderSignals error as "skip, don't fail"; this is the
// identical real posture). UBI-182: no more hard refusal for a pinned
// entry, matching dynamicProviderSignals' own fix.
func dynamicProviderNamespaces(ctx context.Context, binPath, name string, params map[string]any) (map[string]string, error) {
	workDir, err := os.MkdirTemp("", "ubx-sdk-gen-namespaces-"+name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	env, err := dynamicProviderEnv(ctx, workDir, name, params)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binPath, "--dump-namespaces")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("dump namespaces for dynamic provider %q: %w\n%s", name, err, stderr.String())
	}

	var out map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse namespace output for dynamic provider %q: %w", name, err)
	}
	return out, nil
}

// newDynamicProviderLaunchFunc is providerPool's own real launchFunc for
// a key declared under [providers] (ubx's own, dynamic-provider-backed)
// -- this session's own real addition, the first time a dynamic-
// provider-backed source becomes usable for REAL infra through
// ubx resolve/ubx ship, not just `ubx sdk gen`'s schema-only dump
// (dynamicProviderSchema above). Reuses the identical real
// resolveDynamicProviderBinary/writeDynamicProviderConfig/
// provider.Launch machinery that function already established -- the
// one real difference is this keeps the launched client OPEN (wrapped
// into a real executor.Applier via newApplier, exactly like
// newRealLaunchFunc does for a Terraform-registry provider) rather than
// closing it immediately after one schema call.
//
// Real, honest, deliberate scope boundary, named not hidden: binPath
// resolution here always uses the ambient UBX_PROVIDER_DYNAMIC_REPO /
// defaultDynamicProviderRepo convention (no --dynamic-provider-bin flag
// threaded through every real command this touches, `ubx sdk gen`'s own
// only current caller) -- extending that flag to ubx resolve/ubx ship/
// ubx propose/etc. is real, separate, not-yet-done work.
func newDynamicProviderLaunchFunc(salt []byte, dynamic map[string]map[string]any) launchFunc {
	return func(ctx context.Context, key, _ string) (executor.Applier, io.Closer, error) {
		params, ok := dynamic[key]
		if !ok {
			return nil, nil, fmt.Errorf("provider %q is not declared in this stack's [providers] config", key)
		}

		// UBI-194: both the real snapshot and the real
		// ubx-provider-dynamic binary that snapshot itself requires
		// are resolved here -- no UBX_PROVIDER_DYNAMIC_REPO/local-
		// checkout dependency, and no workDir at all (a pinned launch
		// is fully self-sufficient via UBX_SNAPSHOT_PATH, per
		// acquirePinnedSchemaAndBinary's own doc comment).
		binPath, env, err := acquirePinnedSchemaAndBinary(ctx, key, params)
		if err != nil {
			return nil, nil, err
		}

		client, err := provider.Launch(ctx, binPath,
			provider.WithEnv(env...),
			provider.WithHandshakeTimeout(dynamicProviderHandshakeTimeout),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("launch ubx-provider-dynamic for %q: %w", key, err)
		}
		return newApplier(client.Provider, salt, "ubiquex/dynamic/"+key), client, nil
	}
}

// writeDynamicProviderConfig serializes params back into a real
// .ubx/config TOML file under [dynamic_providers.<name>] -- the exact
// real table shape ubx-provider-dynamic's own internal/config package
// parses, round-tripped through TOML rather than hand-built as a string
// so any real value shape (nested auth.params tables included) survives
// correctly.
func writeDynamicProviderConfig(dir, name string, params map[string]any) error {
	doc := map[string]any{
		"dynamic_providers": map[string]any{
			name: params,
		},
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, ".ubx", "config"))
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(doc)
}
