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
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/provider"
)

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

// dynamicProviderEnv resolves the real env vars a launched
// ubx-provider-dynamic process needs to serve name, and prepares workDir
// to match. Two real, mutually exclusive shapes, chosen by
// pinnedSchemaFields:
//
//   - Pinned (source+version present): provider.AcquireSchema resolves a
//     verified, cached, or freshly-downloaded-and-verified snapshot file
//     with zero involvement from workDir at all -- the launched process
//     reads UBX_SNAPSHOT_PATH directly (main.go's own
//     snapshotPathEnvVar branch, checked before .ubx/config would even be
//     loaded), so workDir is left empty, not written to.
//   - Live (existing shape, schema_source/schema_url/...): unchanged --
//     writeDynamicProviderConfig writes the real, temporary
//     [dynamic_providers.<name>] table the launched process fetches
//     schema_url through, exactly as before this function existed.
func dynamicProviderEnv(ctx context.Context, workDir, name string, params map[string]any) ([]string, error) {
	src, version, pinned, err := pinnedSchemaFields(params)
	if err != nil {
		return nil, fmt.Errorf("[providers.%s]: %w", name, err)
	}
	if pinned {
		schemaSrc, err := provider.ParseSchemaSource(src)
		if err != nil {
			return nil, fmt.Errorf("[providers.%s].source: %w", name, err)
		}
		result, err := provider.AcquireSchema(ctx, schemaSrc, version)
		if err != nil {
			return nil, fmt.Errorf("acquire schema for %q: %w", name, err)
		}
		return []string{"UBX_DYNAMIC_PROVIDER_NAME=" + name, "UBX_SNAPSHOT_PATH=" + result.Path}, nil
	}

	if err := writeDynamicProviderConfig(workDir, name, params); err != nil {
		return nil, err
	}
	return []string{"UBX_DYNAMIC_PROVIDER_NAME=" + name}, nil
}

// dynamicProviderSchema launches ubx-provider-dynamic once against name's
// own real config entry (params, the same generic map[string]any
// Config.DynamicProviders[name]/Config.Providers[name] already carries --
// live-shaped or pinned-shaped, dynamicProviderEnv decides which), and
// returns its real GetProviderSchema dump -- no Configure call, no real
// credentials needed, matching this whole file's own real "schema dump
// only" scope.
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

	client, err := provider.Launch(ctx, binPath,
		provider.WithDir(workDir),
		provider.WithEnv(env...),
	)
	if err != nil {
		return nil, fmt.Errorf("launch ubx-provider-dynamic for %q: %w", name, err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch schema for dynamic provider %q: %w", name, err)
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
// own loadResolveProviders) -- resolves the real binary via the same
// ambient convention every other real dynamic-provider call site uses,
// then reuses dynamicProviderSchema unchanged (schema-fetch-then-close,
// the identical real shape resolve's own thirdparty branch already has
// for a real Terraform-registry provider).
func loadDynamicProviderSchema(ctx context.Context, name string, params map[string]any) (*provider.Schemas, error) {
	binPath, err := resolveAmbientDynamicProviderBinary()
	if err != nil {
		return nil, err
	}
	return dynamicProviderSchema(ctx, binPath, name, params)
}

// dynamicProviderSignals runs the SAME ubx-provider-dynamic binary a
// second time, as a real, plain subprocess -- --dump-signals (see that
// flag's own doc comment in ubx-provider-dynamic's cmd/ubx-provider-dynamic/main.go
// for why this can't ride the same provider.Launch/go-plugin connection
// dynamicProviderSchema above uses: go-plugin's own real handshake
// protocol already owns that process's stdout). Reuses the identical
// real, temporary .ubx/config dynamicProviderSchema writes. Returns a
// real, honestly EMPTY (not nil, not an error) result for a
// schema_source this binary doesn't yet extract signals for (Smithy --
// AWS, today), matching that binary's own "skip, don't fail" answer.
//
// Real, honest, deliberate scope boundary, named not hidden: a PINNED
// entry (source+version) returns a real, clear error instead of silently
// degrading -- --dump-signals is a plain subprocess reading a real,
// temporary .ubx/config, and main.go's own snapshotPathEnvVar branch
// (runServeSnapshot) never wires that mode into --dump-signals at all
// (only into real tfplugin6 serving). generateOneDynamicProvider's own
// caller already treats a dynamicProviderSignals error as non-fatal
// ("skip, don't fail" -- sdk.go), so this surfaces as the identical real,
// visible warning a Smithy provider's own missing-signals case already
// produces, not a silent gap.
func dynamicProviderSignals(ctx context.Context, binPath, name string, params map[string]any) (map[string]map[string]*fieldSignal, error) {
	if _, _, pinned, err := pinnedSchemaFields(params); err != nil {
		return nil, fmt.Errorf("[providers.%s]: %w", name, err)
	} else if pinned {
		return nil, fmt.Errorf("[providers.%s]: --dump-signals is not yet wired to a pinned schema source (source+version) -- only the live schema_url shape supports signal extraction today", name)
	}

	workDir, err := os.MkdirTemp("", "ubx-sdk-gen-signals-"+name)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	if err := writeDynamicProviderConfig(workDir, name, params); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binPath, "--dump-signals")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "UBX_DYNAMIC_PROVIDER_NAME="+name)
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
		binPath, err := resolveAmbientDynamicProviderBinary()
		if err != nil {
			return nil, nil, err
		}

		workDir, err := os.MkdirTemp("", "ubx-dynamic-provider-"+key)
		if err != nil {
			return nil, nil, err
		}
		env, err := dynamicProviderEnv(ctx, workDir, key, params)
		if err != nil {
			os.RemoveAll(workDir)
			return nil, nil, err
		}

		client, err := provider.Launch(ctx, binPath,
			provider.WithDir(workDir),
			provider.WithEnv(env...),
		)
		os.RemoveAll(workDir) // the launched process has already read the config by the time Launch returns
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
