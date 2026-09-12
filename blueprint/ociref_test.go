package blueprint

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// ociref_test.go covers UBI-256: an oci:// blueprint source had no
// working spelling from .ubx.hcl. The tag had to live somewhere, and
// both places rejected it for unrelated reasons.

func TestOCIReference_ComposesASeparateVersion(t *testing.T) {
	got, err := ociReference("oci://ghcr.io/ubx-blueprints/ubx-aws-sqs", "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := "oci://ghcr.io/ubx-blueprints/ubx-aws-sqs:v0.1.0"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A digest is not a tag, and joining it with a colon would produce a
// reference no registry resolves.
func TestOCIReference_ADigestJoinsWithAt(t *testing.T) {
	got, err := ociReference("oci://ghcr.io/org/repo", "sha256:abc123")
	if err != nil {
		t.Fatal(err)
	}
	if want := "oci://ghcr.io/org/repo@sha256:abc123"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestOCIReference_NoVersionIsUnchanged(t *testing.T) {
	source := "oci://ghcr.io/org/repo:v1"
	got, err := ociReference(source, "")
	if err != nil || got != source {
		t.Errorf("got %q, %v", got, err)
	}
}

// Two versions can disagree, and silently picking one would pull a
// version the author did not ask for. For something content-addressed
// that is the worst available outcome, so it is refused.
func TestOCIReference_VersionInBothPlacesIsRefused(t *testing.T) {
	_, err := ociReference("oci://ghcr.io/org/repo:v1", "v2")
	if err == nil {
		t.Fatal("want a refusal when a version is given twice")
	}
	if !strings.Contains(err.Error(), "exactly one place") {
		t.Errorf("the refusal has to say what to do, got: %v", err)
	}
	if _, err := ociReference("oci://ghcr.io/org/repo@sha256:abc", "v2"); err == nil {
		t.Error("a digest is a version too")
	}
}

// A registry port is a colon that is part of the HOST, not a tag.
// Treating it as one would refuse a perfectly ordinary local-registry
// reference.
func TestOCIReference_ARegistryPortIsNotATag(t *testing.T) {
	got, err := ociReference("oci://localhost:5000/org/repo", "v1")
	if err != nil {
		t.Fatalf("a port is not a tag: %v", err)
	}
	if want := "oci://localhost:5000/org/repo:v1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The other half of UBI-256: the pull succeeded and the call still
// failed, because the name derived from the source kept the tag and
// every identifier derivation then refused the colon.
func TestBlueprintNameFromCall_StripsAnOCIVersion(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"oci://ghcr.io/ubx-blueprints/ubx-aws-sqs:v0.1.0", "ubx-aws-sqs"},
		{"oci://ghcr.io/ubx-blueprints/ubx-aws-sqs", "ubx-aws-sqs"},
		{"oci://ghcr.io/org/repo@sha256:abc123", "repo"},
		{"oci://localhost:5000/org/repo:v1", "repo"},
	}
	for _, c := range cases {
		got := blueprintNameFromCall(resolver.BlueprintCall{Blueprint: c.source})
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.source, got, c.want)
		}
		if strings.ContainsAny(got, ":@") {
			t.Errorf("%s: derived name %q still carries a version, which every identifier derivation refuses", c.source, got)
		}
	}
}

// A git source's own ref must not be stripped from anything: only an
// oci:// reference carries its version inside the name.
func TestBlueprintNameFromCall_GitIsUnchanged(t *testing.T) {
	got := blueprintNameFromCall(resolver.BlueprintCall{
		Blueprint: "https://github.com/ubx-blueprints/ci-platform.git",
		Ref:       "v0.1.0",
	})
	if got != "ci-platform" {
		t.Errorf("got %q", got)
	}
}

// --path stays refused for OCI, since an artifact is the whole
// blueprint. Only --ref changed meaning.
func TestPull_OCIStillRefusesPath(t *testing.T) {
	_, err := Pull(context.Background(), "oci://ghcr.io/org/repo:v1", filepath.Join(t.TempDir(), "dest"), "", "subdir")
	if err == nil {
		t.Fatal("want a refusal for --path on an oci:// source")
	}
	if !strings.Contains(err.Error(), "--path") || strings.Contains(err.Error(), "--ref/--path") {
		t.Errorf("the refusal should name only --path now, got: %v", err)
	}
}
