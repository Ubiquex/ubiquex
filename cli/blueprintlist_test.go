package cli

import (
	"strings"
	"testing"
)

// TestBlueprintList_EmptyCache: a cache with nothing in it says so.
//
// The listing's whole job is explaining a directory of bare content
// hashes, and printing nothing at all for an empty one would leave a
// reader unable to tell "no blueprints" from "this command did not
// work".
//
// The populated cases live in blueprint/contentstore_test.go, where a
// git fixture can drive a real pull into the store. A local path is
// deliberately never cached (an author may be editing it), so it cannot
// exercise the content store from here.
func TestBlueprintList_EmptyCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := runUbx(t, nil, "blueprint", "list")
	if err != nil {
		t.Fatalf("blueprint list on an empty cache: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no blueprints cached") {
		t.Fatalf("expected an empty-cache message, got: %s", out)
	}
	// And it names how one gets populated, rather than leaving that to
	// be looked up.
	if !strings.Contains(out, "ubx plan") {
		t.Fatalf("empty-cache message should say what fills the cache: %s", out)
	}
}
