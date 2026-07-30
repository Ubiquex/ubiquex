package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// registerFakeRemoteOpener installs a core.RegisterRemoteLedgerOpener
// that maps any ref ending in "/<stack>/" onto a PrefixedBucket over the
// SAME shared in-memory bucket, keyed by that trailing stack segment --
// standing in for a real gocloud.dev/blob store URI open (which
// core/resolver itself never performs -- see core/openref.go) while
// still proving two stacks genuinely share one base: gocloud.dev/blob's
// own mem:// URL opener hands back a fresh, unshared bucket per call, so
// opening two "mem://.../payments/" and "mem://.../networking/" URIs
// independently would never actually prove sharing -- this fake does,
// deliberately, the same reasoning ledgerstore's own
// TestTransformStoreURI_PathStyleBecomesQueryParam settled for testing
// its own URI translation.
func registerFakeRemoteOpener(t *testing.T, shared *blob.Bucket) {
	t.Helper()
	core.RegisterRemoteLedgerOpener(func(ctx context.Context, ref string) (core.LedgerStore, func() error, error) {
		trimmed := strings.TrimSuffix(ref, "/")
		parts := strings.Split(trimmed, "/")
		stack := parts[len(parts)-1]
		return ledgerstore.New(blob.PrefixedBucket(shared, stack+"/")), func() error { return nil }, nil
	})
	t.Cleanup(func() { core.RegisterRemoteLedgerOpener(nil) })
}

const testBase = "mem://acme-ledger/acme/prod"

func TestResolve_CrossStack_ByStackName_ResolvesAgainstOwnBase(t *testing.T) {
	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })
	registerFakeRemoteOpener(t, shared)

	currentStore := ledgerstore.New(blob.PrefixedBucket(shared, "payments/"))
	l := core.OpenStoreForStack(currentStore, testBase, nil)

	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`)
	wantHead, err := neighbor.Head()
	if err != nil {
		t.Fatalf("neighbor head: %v", err)
	}

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"networking","to":"networking.aws_vpc.main.id"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	if cfg["vpc_id"] != "vpc-123" {
		t.Fatalf("vpc_id = %v, want vpc-123", cfg["vpc_id"])
	}

	var pin *core.ResolutionInput
	for i := range p.Resolution.Inputs {
		if p.Resolution.Inputs[i].Kind == "cross_stack_pin" {
			pin = &p.Resolution.Inputs[i]
		}
	}
	if pin == nil {
		t.Fatal("expected a cross_stack_pin resolution input")
	}
	wantAddr := testBase + "/networking/"
	if pin.LedgerDir != wantAddr {
		t.Errorf("LedgerDir = %q, want the derived address %q", pin.LedgerDir, wantAddr)
	}
	if pin.PinnedHead != wantHead {
		t.Errorf("PinnedHead = %q, want %q", pin.PinnedHead, wantHead)
	}
}

func TestResolve_CrossStack_ByStackName_ExternalOverride(t *testing.T) {
	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })
	registerFakeRemoteOpener(t, shared)

	currentStore := ledgerstore.New(blob.PrefixedBucket(shared, "payments/"))
	// The current stack's own base deliberately does NOT hold "external-team" --
	// only the [ledger.external] override does, proving the override is
	// actually consulted rather than always falling back to the own base.
	l := core.OpenStoreForStack(currentStore, testBase, map[string]string{
		"external-team": "mem://other-base/net/prod",
	})

	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "external-team/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedger(t, neighbor, core.Address{Stack: "external-team", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-999"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"external-team","to":"external-team.aws_vpc.main.id"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var pin *core.ResolutionInput
	for i := range p.Resolution.Inputs {
		if p.Resolution.Inputs[i].Kind == "cross_stack_pin" {
			pin = &p.Resolution.Inputs[i]
		}
	}
	if pin == nil {
		t.Fatal("expected a cross_stack_pin resolution input")
	}
	wantAddr := "mem://other-base/net/prod/external-team/"
	if pin.LedgerDir != wantAddr {
		t.Errorf("LedgerDir = %q, want the externally-overridden address %q", pin.LedgerDir, wantAddr)
	}
}

func TestResolve_CrossStack_ByStackName_NoBaseIsGitLocal_Refused(t *testing.T) {
	l := core.Open(t.TempDir()) // git-local: no base store at all

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"networking","to":"networking.aws_vpc.main.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
	if !strings.Contains(err.Error(), "networking") || !strings.Contains(err.Error(), "ledger_dir") {
		t.Fatalf("expected the error to name the stack and suggest ledger_dir, got: %v", err)
	}
}

func TestResolve_CrossStack_BothLedgerDirAndStack_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"ledger_dir":"/tmp/x","stack":"networking","to":"networking.aws_vpc.main.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected the error to explain exactly one of ledger_dir/stack is allowed, got: %v", err)
	}
}

// TestVerifyPins_RemoteNeighbor_CatchesStaleness proves VerifyPins works
// identically for a remote-store-recorded pin, not just git-local: a
// neighbor that advances after resolve time is caught via core.OpenRef,
// exactly like docs/resolver-adversarial.md row 5 already proves for
// git-local.
func TestVerifyPins_RemoteNeighbor_CatchesStaleness(t *testing.T) {
	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })
	registerFakeRemoteOpener(t, shared)

	currentStore := ledgerstore.New(blob.PrefixedBucket(shared, "payments/"))
	l := core.OpenStoreForStack(currentStore, testBase, nil)

	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"networking","to":"networking.aws_vpc.main.id"}}}`),
	)
	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if err := VerifyPins(p); err != nil {
		t.Fatalf("VerifyPins (before neighbor advances): %v", err)
	}

	// The neighbor ledger genuinely advances -- someone else accepted a
	// change against it after this proposal was resolved.
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "second"}, `{"id":"vpc-456"}`)

	err = VerifyPins(p)
	if !errors.Is(err, ErrCrossStackPinStale) {
		t.Fatalf("VerifyPins (after neighbor advances) = %v, want ErrCrossStackPinStale", err)
	}
}
