package blueprint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sdk: mode, the "import the published SDK instead of emitting a copy of
// its definitions" path.
//
// The load-bearing part is not emitting an import, it is knowing which
// package. A published SDK is laid out per service, and the service is
// not derivable from the wire type: measured against the real
// ubiquex/aws 4.0.0 snapshot, 966 of 1728 AWS types land in the wrong
// package under a mechanical split, because the split takes one token
// where the real namespace is a whole word. These fix the two shapes
// that proves.

func withSnapshot(t *testing.T, cfnTypes ...string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "members"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := map[string]any{}
	for _, ct := range cfnTypes {
		spec[ct] = map[string]any{"properties": map[string]any{}}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	member, err := json.Marshal(map[string]any{"raw_spec": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "members", "aws.json"), member, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := snapshotDir
	snapshotDir = func(_, _, _ string) (string, error) { return dir, nil }
	t.Cleanup(func() { snapshotDir = orig })
}

// The whole reason resolution reads a snapshot rather than splitting the
// wire name. AWS::EC2::Instance is aws_instance in package ec2; a
// mechanical split would say package "instance", which does not exist.
func TestResolveSDKTargets_ServiceIsNotDerivableFromTheWireType(t *testing.T) {
	withSnapshot(t, "AWS::EC2::Instance", "AWS::SQS::Queue", "AWS::ARCZonalShift::ZonalAutoshiftConfiguration")
	spec := &SDKSpec{Provider: "ubiquex/aws@4.0.0", Go: "example.com/sdk/v3@v3.0.1"}

	got, err := resolveSDKTargets(spec, []string{"aws_instance", "aws_sqs_queue", "aws_zonal_autoshift_configuration"})
	if err != nil {
		t.Fatalf("resolveSDKTargets: %v", err)
	}
	for _, tc := range []struct{ wire, service, typeName string }{
		{"aws_instance", "ec2", "Instance"},
		{"aws_sqs_queue", "sqs", "Queue"},
		{"aws_zonal_autoshift_configuration", "arczonalshift", "ZonalAutoshiftConfiguration"},
	} {
		g := got[tc.wire]
		if g.Service != tc.service || g.TypeName != tc.typeName {
			t.Errorf("%s -> %+v, want service %q type %q", tc.wire, g, tc.service, tc.typeName)
		}
	}
	// The one that would silently produce a nonexistent import path.
	if got["aws_instance"].Service == "instance" {
		t.Fatal("resolved the package by splitting the wire name, which is wrong for more than half of AWS")
	}
}

// A type the snapshot does not carry is refused rather than guessed. A
// guessed import path fails as a compile error inside a caller's own
// stack, which is the wrong place to learn about it.
func TestResolveSDKTargets_UnknownTypeIsRefused(t *testing.T) {
	withSnapshot(t, "AWS::SQS::Queue")
	spec := &SDKSpec{Provider: "ubiquex/aws@4.0.0", Go: "example.com/sdk/v3@v3.0.1"}
	if _, err := resolveSDKTargets(spec, []string{"aws_sqs_queue", "aws_not_a_real_type"}); err == nil {
		t.Fatal("expected an unknown wire type to be refused")
	}
}

// An absent snapshot names the remedy rather than falling back to a
// split, and says the emitting path is still available.
func TestResolveSDKTargets_AbsentSnapshotIsAClearError(t *testing.T) {
	orig := snapshotDir
	snapshotDir = func(_, _, _ string) (string, error) { return filepath.Join(t.TempDir(), "nope"), nil }
	t.Cleanup(func() { snapshotDir = orig })

	_, err := resolveSDKTargets(&SDKSpec{Provider: "ubiquex/aws@4.0.0"}, []string{"aws_sqs_queue"})
	if err == nil {
		t.Fatal("expected an absent snapshot to be an error")
	}
	if got := err.Error(); !contains(got, "no snapshot at") || !contains(got, "drop the sdk: block") {
		t.Fatalf("error should name the remedy and the fallback, got: %s", got)
	}
}

// The go: spec must carry a real version. A v0.0.0 placeholder is
// exactly the bug UBI-237 found live, where every blueprint built that
// way failed against the real published module.
func TestGoModulePathAndVersion_RequiresAVersion(t *testing.T) {
	if _, _, err := goModulePathAndVersion("github.com/ubiquex/ubx-sdk-aws/sdk/go/v3"); err == nil {
		t.Fatal("expected a version-less go: spec to be refused")
	}
	path, version, err := goModulePathAndVersion("github.com/ubiquex/ubx-sdk-aws/sdk/go/v3@v3.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "github.com/ubiquex/ubx-sdk-aws/sdk/go/v3" || version != "v3.0.1" {
		t.Fatalf("got %q %q", path, version)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
