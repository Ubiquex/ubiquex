package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/provider"
)

// withFakeDynamicLaunch points the pool's dynamic-provider launch at
// fakeprovider for the calling test.
//
// The real one acquires a pinned snapshot and launches a real
// ubx-provider-dynamic, which is neither hermetic nor something this
// suite should do. fakeprovider answers the same gRPC surface, so
// everything above the launch (inference over the [providers] table,
// pool routing, the read itself) is exercised for real.
func withFakeDynamicLaunch(t *testing.T) {
	t.Helper()
	orig := newDynamicLaunch
	t.Cleanup(func() { newDynamicLaunch = orig })
	newDynamicLaunch = func(salt []byte, dynamic map[string]map[string]any) launchFunc {
		return func(ctx context.Context, key, version string) (executor.Applier, io.Closer, error) {
			client, err := provider.Launch(ctx, fakeProviderBinary)
			if err != nil {
				return nil, nil, err
			}
			return newApplier(client.Provider, salt, key), client, nil
		}
	}
}

// TestScan_SingleResource_DynamicProvidersTable is the structural half of
// the [providers] gap.
//
// `ubx ship`'s half was one condition: ship already built a pool and
// already passed cfg.Providers into it, and only the gate above refused
// to let a dynamic-only stack through. Single-resource scan had no pool
// at all. UBI-49 finding #4 taught it to read a provider table, but it
// read [thirdparty_providers] by launching each declared source and
// asking its schema, and a dynamic provider is not acquired from a
// registry, so that mechanism could never have found one however its
// inputs were fixed.
//
// It now uses declaredProvidersForInference plus resolver.InferProvider
// over the pool, the same pair the five commands swept by d2d235ac
// already use, so both kinds of table entry route through pool.Get.
func TestScan_SingleResource_DynamicProvidersTable(t *testing.T) {
	withFakeDynamicLaunch(t)

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers.fake]
source = "ubiquex/fake"
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "scan",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1"}`,
		"--ledger-dir", dir,
	)

	// The regression: a [providers]-only stack used to fall straight past
	// the table into the legacy branch and demand a flag for a provider it
	// had already declared.
	all := out + errText(err)
	if strings.Contains(all, "either --provider or --source") {
		t.Fatalf("scan refused for want of a provider flag despite a [providers] table: %s", all)
	}
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "New resource found") {
		t.Fatalf("expected a 'new' classification resolved entirely from [providers], got: %s", out)
	}
}

// TestScan_SingleResource_UnknownTypeNamesBothTables proves the inference
// failure now reports across the whole resolved set rather than the
// registry half of it, so a stack whose only provider is dynamic gets an
// answer that names it.
func TestScan_SingleResource_UnknownTypeNamesBothTables(t *testing.T) {
	withFakeDynamicLaunch(t)

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers.fake]
source = "ubiquex/fake"
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "scan",
		"--type", "nonexistent_thing",
		"--name", "whatever",
		"--lookup", `{"name":"whatever"}`,
		"--ledger-dir", dir,
	)
	if err == nil {
		t.Fatalf("expected an unknown-type refusal, got success: %s", out)
	}
	all := out + errText(err)
	if !strings.Contains(all, "nonexistent_thing") {
		t.Fatalf("expected the unknown type named in the error, got: %s", all)
	}
	if !strings.Contains(all, "fake") {
		t.Fatalf("expected the dynamic provider to appear among those checked, got: %s", all)
	}
}
