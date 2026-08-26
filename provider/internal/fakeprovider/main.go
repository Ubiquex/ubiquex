// Command fakeprovider is a test fixture only: it speaks (or deliberately
// misspeaks) just enough of the tfplugin handshake to exercise
// provider.Launch's success and adversarial paths — including both wire
// protocols ubx supports — without depending on a real Terraform provider
// binary.
//
// Mode is selected by the FAKEPROVIDER_MODE environment variable (not argv:
// real provider binaries are launched with no arguments, so tests drive
// this fixture the same way Launch drives a real provider, via
// provider.WithEnv):
//
//	ok-v6               valid v6 handshake, serves a real schema + ReadResource over gRPC
//	ok-v5               valid v5 handshake, serves a real schema + ReadResource over gRPC
//	conformance-v6      valid v6 handshake, serves a schema/ReadResource shaped by the
//	                    FAKEPROVIDER_RESOURCE_TYPE/FAKEPROVIDER_ATTRS env vars below —
//	                    UBI-9's fake-only per-type conformance fixture
//	conformance-v5      same as conformance-v6, over the v5 wire protocol
//	bad-core            handshake line with the wrong core (go-plugin) protocol version
//	unsupported-version handshake line with an app protocol version ubx has no wire impl for
//	bad-protocol        handshake line with a non-grpc wire protocol
//	malformed           an unparseable line
//	hang                never writes anything to stdout
//	crash                exits immediately with no output
//
// FAKEPROVIDER_EXTRA_TAG ("key=value"), if set, merges an extra tag into
// ok-v5/ok-v6's ReadResource response regardless of current_state — see
// echoWidgetState.
//
// FAKEPROVIDER_STATE_DIR (UBI-85 live finale, ok-v5/ok-v6 only), if set,
// makes ApplyResourceChange persist each fake_widget's full applied state
// to "<dir>/<id>.json" and ReadResource load it back by the id in its own
// current_state — opt-in cross-process persistence, since a bare create in
// one `ubx` invocation and a later modify/drift-check's freshness read in a
// SEPARATE invocation each launch a fresh fakeprovider process, and this
// fixture is otherwise fully stateless (see echoWidgetState): without this,
// ReadResource can only echo back whatever current_state it was actually
// given, and ship-time freshness verification deliberately passes just the
// resource's own minimal lookup key (real-provider semantics — a real
// Read only needs an id to fetch full state from the cloud's own
// database), so any attribute absent from that lookup key (e.g. tags)
// reads back as spuriously gone. Unset by every existing test, so every
// existing test keeps today's pure-echo behavior unchanged.
//
// FAKEPROVIDER_FAIL_CREATE_TARGET / FAKEPROVIDER_FAIL_CREATE_COUNT (UBI-92,
// ok-v5/ok-v6 only, paired with FAKEPROVIDER_APPLY_MODE=fail-create-not-found
// below) name which fake_widget's own "name" attribute should fail its
// create and how many times before letting it through.
//
// FAKEPROVIDER_APPLY_MODE (UBI-26, ok-v5/ok-v6 only) selects
// ApplyResourceChange's behavior:
//
//	""/"ok"           echoes planned_state back (same computed-id fill-in as
//	                  ReadResource), simulating a clean apply -- UNLESS
//	                  planned_state is a genuine null (UBI-30: ubx's own
//	                  destroy convention), in which case it's a destroy: the
//	                  id named in prior_state is remembered as destroyed for
//	                  the rest of this process's lifetime, and a subsequent
//	                  ReadResource for that same id genuinely reports
//	                  not-found (no DynamicValue at all), the same shape a
//	                  real provider returns for a resource that's actually
//	                  gone. This fixture has no other persistent state at
//	                  all -- see destroyedIDs' own doc comment for why a
//	                  destroy specifically needs one.
//	"diagnostic-error" returns one ERROR-severity Diagnostic and no new_state --
//	                  the "terminal" half of docs/executor.md's error taxonomy
//	"hang"            never responds -- the caller's own context deadline is
//	                  what must fire; the "retryable" half of the taxonomy
//	"lying-destroy"   (UBI-44) a destroy call reports the identical clean
//	                  success shape (no error, no diagnostics) an honest
//	                  destroy does, but never marks the id destroyed -- the
//	                  next ReadResource still finds it, exactly like the
//	                  real google_pubsub_topic destroy that lied. Exercises
//	                  shipDestroyNode's own universal post-destroy read-back.
//	"slow"            (UBI-83) sleeps fakeProviderSlowApplyDelay (context-
//	                  aware -- a shorter --timeout still cancels cleanly)
//	                  before falling through to the ordinary "ok" success
//	                  path -- a genuinely slow-but-clean apply, real
//	                  elapsed wall-clock time, no real cloud provider
//	                  needed. Exercises the live "shipping" ticker's own
//	                  multi-tick in-place redraw for a create/modify
//	                  specifically (as opposed to "lying-destroy," which
//	                  only ever exercises the read-back-verification
//	                  ticker, since a lying destroy still returns
//	                  immediately itself).
//	"fail-create-not-found" (UBI-92) simulates AWS IAM's own documented
//	                  eventual-consistency lag on the CREATE side (the
//	                  mirror image of "lying-destroy"'s destroy-side
//	                  fault): the fake_widget named by
//	                  FAKEPROVIDER_FAIL_CREATE_TARGET fails its own create
//	                  with a not-found-shaped diagnostic ("NoSuchEntity" /
//	                  "cannot be found") for FAKEPROVIDER_FAIL_CREATE_COUNT
//	                  attempts, then succeeds normally -- the exact shape
//	                  a real AttachRolePolicy hit against a role that had
//	                  itself JUST shipped successfully in the same batch.
//	                  Every other fake_widget's create is unaffected.
//	                  Exercises ship.go's own
//	                  retryCreateOnDependencyNotVisible against the real
//	                  wire protocol, not just core/executor's in-process
//	                  fakeApplier.
//
// conformance-v5/conformance-v6 are driven by:
//
//	FAKEPROVIDER_RESOURCE_TYPE   the resource type name to advertise (e.g. "aws_instance")
//	FAKEPROVIDER_ATTRS           comma-separated attribute names to model — a subset of
//	                             that type's REAL schema (see cmd/schemadump), so the
//	                             identity/mutable fields a conformance test exercises are
//	                             schema-verified, not invented. "tags"/"tags_all" are
//	                             modeled as string maps; everything else as strings.
//	FAKEPROVIDER_MUTATE_ATTR     which attribute name (from FAKEPROVIDER_ATTRS) to change
//	                             on the next ReadResource call — the fake stand-in for a
//	                             real out-of-band mutation.
//	FAKEPROVIDER_MUTATE_VALUE    the value to set it to. If the target attribute is a map
//	                             ("tags"/"tags_all"), this is "key=value" merged into the
//	                             map, same convention as FAKEPROVIDER_EXTRA_TAG; otherwise
//	                             it replaces the attribute's scalar value directly.
//	FAKEPROVIDER_SENSITIVE_ATTRS comma-separated subset of FAKEPROVIDER_ATTRS to advertise
//	                             with Sensitive: true (UBI-23) — lets a test exercise
//	                             provider.Redact/core's $redacted handling end to end
//	                             without a real Sensitive-bearing provider schema.
//	FAKEPROVIDER_ATTR_TYPES      (UBI-63 session 3; "number" added UBI-123) comma-separated
//	                             "name:kind" overrides for FAKEPROVIDER_ATTRS entries whose
//	                             real shape isn't a plain string — kind one of "bool",
//	                             "number" (cty.Number, a real numeric-typed attribute —
//	                             UBI-123's own encode-path repro needs this: a plain
//	                             string-typed attribute never exercises encodePrimitiveValue's
//	                             cty.Number branch at all), "list" (list of string), "map"
//	                             (map of string; "tags"/"tags_all" already default to this
//	                             without needing an entry here). Lets a test model e.g. a
//	                             bool-typed attribute with its own real zero value (false),
//	                             or a numeric attribute like message_retention_seconds, not
//	                             just strings/maps.
//	FAKEPROVIDER_COMPUTED_ATTRS  (UBI-63 session 3) comma-separated FAKEPROVIDER_ATTRS
//	                             names (beyond "id", always Computed) to advertise as
//	                             Computed rather than plain Optional — models a real
//	                             attribute the provider fills in itself (e.g. AWS's own
//	                             per-resource "region"), whose null baseline resolving to
//	                             a real value on a later read is expected materialization,
//	                             not drift.
//
// See conformance/fake_test.go for how the harness drives this.
package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"

	"github.com/ubiquex/ubiquex/provider/tfplugin5"
	"github.com/ubiquex/ubiquex/provider/tfplugin6"
)

func main() {
	mode := os.Getenv("FAKEPROVIDER_MODE")

	switch mode {
	case "ok-v6":
		serveV6()
	case "ok-v5":
		serveV5()
	case "conformance-v6":
		serveConformanceV6()
	case "conformance-v5":
		serveConformanceV5()
	case "bad-core":
		fmt.Println("99|6|unix|/tmp/does-not-matter|grpc")
		block()
	case "unsupported-version":
		fmt.Println("1|4|unix|/tmp/does-not-matter|grpc")
		block()
	case "bad-protocol":
		fmt.Println("1|6|unix|/tmp/does-not-matter|netrpc")
		block()
	case "malformed":
		fmt.Println("not-a-handshake-line")
		block()
	case "hang":
		block()
	case "crash":
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "fakeprovider: unknown mode %q\n", mode)
		os.Exit(2)
	}
}

// block keeps the process alive (as a real plugin's server loop would)
// without writing anything further to stdout, until the test harness kills
// it. A timed sleep, not a bare select{}, so the Go runtime's deadlock
// detector (which fires on a goroutine blocked with no possible wakeup)
// doesn't kill the process itself before the test does.
func block() {
	time.Sleep(24 * time.Hour)
}

func socketPath() string {
	f, err := os.CreateTemp("", "fakeprovider-*.sock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: tempfile: %v\n", err)
		os.Exit(1)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	return path
}

func serveV6() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	tfplugin6.RegisterProviderServer(srv, &fakeProviderServerV6{})

	fmt.Printf("1|6|unix|%s|grpc\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

func serveV5() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	tfplugin5.RegisterProviderServer(srv, &fakeProviderServerV5{})

	fmt.Printf("1|5|unix|%s|grpc\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

// fakeWidgetSchemaJSON etc. are shared, protocol-independent fixture data:
// one resource type (fake_widget) with a handful of scalar attributes,
// enough to exercise a real schema dump and a real ReadResource round trip.

// fakeProviderSlowApplyDelay is FAKEPROVIDER_APPLY_MODE=slow's own real
// elapsed wait (UBI-83) -- long enough to observe several real
// tickInterval-spaced redraws (ubx's own live "shipping" ticker) before
// the call finally returns, short enough not to make a `script -q`
// verification pass tediously slow. The in-place-redraw property this
// mode exists to prove (one line changing, not N appended) is provably
// independent of how many ticks fire -- see cli's own
// TestNewProgressPrinter_TicksUpdateInPlace_NotAppendedPerTick -- so this
// doesn't need to match a real SQS create's own ~25s+ lag to be a
// faithful proof, just to be genuinely real, multi-tick elapsed time.
const fakeProviderSlowApplyDelay = 10 * time.Second

type fakeProviderServerV6 struct {
	tfplugin6.UnimplementedProviderServer
}

func (s *fakeProviderServerV6) GetProviderSchema(context.Context, *tfplugin6.GetProviderSchema_Request) (*tfplugin6.GetProviderSchema_Response, error) {
	return &tfplugin6.GetProviderSchema_Response{
		Provider: &tfplugin6.Schema{
			Block: &tfplugin6.Schema_Block{
				Attributes: []*tfplugin6.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin6.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin6.Schema_Block{
					Attributes: []*tfplugin6.Schema_Attribute{
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Optional: true},
					},
				},
			},
		},
		// DataSourceSchemas (UBI-178 piece 4 follow-up): "fake_widget" here
		// deliberately shares its own name with the RESOURCE schema above --
		// a real provider's own data source and resource commonly do
		// (hashicorp/aws's own "aws_instance" is both, per
		// ir.ResourceType.IsDataSource's own doc comment) -- so this fixture
		// exercises the exact same-WireType collision
		// ir.ServiceAndLocalNameForType's own "data" namespace segment exists
		// to keep apart, not just a data source in isolation. Before this
		// entry existed, no fakeprovider mode ever served a real
		// DataSourceSchemas at all: provider.Schemas.DataSources was always
		// empty end to end through the real `ubx sdk gen` CLI path, so
		// nothing ever exercised writeGeneratedSDK's own
		// IsDataSource=true branch, ir.FromSchema against a real data-source
		// schema, or any per-language template's own DataSourceBinding
		// rendering through the actual wire protocol -- only through
		// hermetic, hand-built ir.ResourceType fixtures. That gap is why the
		// ResourceBinding/DataSourceBinding template swap (UBI-178 piece 4)
		// shipped through `go build`/`deno check`/`ast.parse` on generated
		// output unnoticed: none of those checks ever called the generated
		// code, and nothing ever generated a REAL data source to check.
		// Shape: name (Required) is the real lookup argument a caller
		// supplies; id/tags (Computed) are what a real lookup would return --
		// deliberately NOT identical Required/Optional/Computed markings to
		// the resource above, so Config/Attrs assertions can't pass by
		// accident from stale copy-paste.
		DataSourceSchemas: map[string]*tfplugin6.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin6.Schema_Block{
					Attributes: []*tfplugin6.Schema_Attribute{
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Computed: true},
					},
				},
			},
		},
	}, nil
}

func (s *fakeProviderServerV6) ConfigureProvider(context.Context, *tfplugin6.ConfigureProvider_Request) (*tfplugin6.ConfigureProvider_Response, error) {
	return &tfplugin6.ConfigureProvider_Response{}, nil
}

func (s *fakeProviderServerV6) ReadResource(_ context.Context, req *tfplugin6.ReadResource_Request) (*tfplugin6.ReadResource_Response, error) {
	if id, ok := currentStateID(req.CurrentState.GetMsgpack()); ok && isDestroyed(id) {
		// A destroy (UBI-30) landed against this id in a prior
		// ApplyResourceChange call, this same process lifetime -- report
		// genuinely not-found (no DynamicValue at all), the same shape a
		// real provider returns for a resource that's actually gone.
		return &tfplugin6.ReadResource_Response{}, nil
	}
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ReadResource_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

func (s *fakeProviderServerV6) ApplyResourceChange(ctx context.Context, req *tfplugin6.ApplyResourceChange_Request) (*tfplugin6.ApplyResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin6.ApplyResourceChange_Response{
			Diagnostics: []*tfplugin6.Diagnostic{{
				Severity: tfplugin6.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	case "slow":
		select {
		case <-time.After(fakeProviderSlowApplyDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "fail-create-not-found":
		// UBI-92: PlannedState decodes as null for a destroy (see
		// destroyRequestID below), so currentStateName's own decode
		// fails and ok is false -- this only ever fires for a create/
		// modify apply, never a destroy.
		if name, ok := currentStateName(req.PlannedState.GetMsgpack()); ok && failCreateNotFound(name) {
			return &tfplugin6.ApplyResourceChange_Response{
				Diagnostics: []*tfplugin6.Diagnostic{{
					Severity: tfplugin6.Diagnostic_ERROR,
					Summary:  "NoSuchEntity",
					Detail:   fmt.Sprintf("fakeprovider: simulated eventual-consistency lag -- the resource with name %q cannot be found (UBI-92 fail-create-not-found)", name),
				}},
			}, nil
		}
	}
	if id, ok, isDestroy := destroyRequestID(req.PriorState.GetMsgpack(), req.PlannedState.GetMsgpack()); isDestroy {
		// A destroy (UBI-30, docs/executor.md's own amendment): PlannedState
		// is the literal null ubx's own executor sends for "destroy this."
		// PlannedPrivate is REQUIRED here, not optional -- found empirically
		// against a real, complex SDKv2 AWS provider (UBI-30's own live AWS
		// finale): skipping PlanResourceChange (leaving PlannedPrivate
		// empty) makes a real provider's own shim silently no-op instead of
		// actually deleting anything. This fixture is deliberately STRICTER
		// than that real behavior -- returning a clear diagnostic instead
		// of a silent no-op -- so a regression here (ubx skipping
		// PlanResourceChange again) fails loudly as a test, rather than
		// passing while quietly reproducing the exact bug this session
		// found and fixed.
		if len(req.PlannedPrivate) == 0 {
			return &tfplugin6.ApplyResourceChange_Response{
				Diagnostics: []*tfplugin6.Diagnostic{{
					Severity: tfplugin6.Diagnostic_ERROR,
					Summary:  "destroy called without PlannedPrivate",
					Detail:   "fakeprovider: a real destroy requires a prior PlanResourceChange call (UBI-30) -- this fixture refuses to silently no-op the way a real provider's own shim was found to",
				}},
			}, nil
		}
		// Marked here, not deleted from any persistent map (this fixture
		// never had one to begin with -- ubx supplies the same lookup on
		// every read, never re-derives it) -- ReadResource above is what
		// actually honors this mark on a subsequent read within the same
		// process lifetime.
		//
		// "lying-destroy" (UBI-44) deliberately skips markDestroyed: the
		// response is the byte-for-byte identical clean-success shape (no
		// error, no diagnostics, an empty Response decoding to a genuine
		// NewState of null) a real destroy produces, but the resource stays
		// reachable on the next ReadResource -- the exact shape a real
		// google_pubsub_topic gave live, confirmed via Cloud Audit Logs
		// showing zero real DeleteTopic calls despite this identical
		// response. Exercises shipDestroyNode's own universal post-destroy
		// read-back (docs/executor.md's UBI-44 amendment) through the real
		// wire protocol, not just an in-process interface mock.
		if ok && os.Getenv("FAKEPROVIDER_APPLY_MODE") != "lying-destroy" {
			markDestroyed(id)
			removePersistedState(req.PriorState.GetMsgpack())
		}
		return &tfplugin6.ApplyResourceChange_Response{}, nil
	}
	out, err := echoAppliedState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ApplyResourceChange_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

// PlanResourceChange stands in for a real provider's own planning RPC
// (UBI-30): the only thing ubx's own executor actually needs back from it
// before a destroy Apply is a non-empty PlannedPrivate, so that's what this
// fixture supplies -- ProposedNewState is echoed through unchanged since
// this fixture never varies plan output from proposed input.
func (s *fakeProviderServerV6) PlanResourceChange(ctx context.Context, req *tfplugin6.PlanResourceChange_Request) (*tfplugin6.PlanResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin6.PlanResourceChange_Response{
			Diagnostics: []*tfplugin6.Diagnostic{{
				Severity: tfplugin6.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &tfplugin6.PlanResourceChange_Response{
		PlannedState:   req.ProposedNewState,
		PlannedPrivate: []byte("fakeprovider-planned-private"),
	}, nil
}

type fakeProviderServerV5 struct {
	tfplugin5.UnimplementedProviderServer
}

func (s *fakeProviderServerV5) GetSchema(context.Context, *tfplugin5.GetProviderSchema_Request) (*tfplugin5.GetProviderSchema_Response, error) {
	return &tfplugin5.GetProviderSchema_Response{
		Provider: &tfplugin5.Schema{
			Block: &tfplugin5.Schema_Block{
				Attributes: []*tfplugin5.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin5.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin5.Schema_Block{
					Attributes: []*tfplugin5.Schema_Attribute{
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Optional: true},
					},
				},
			},
		},
		// DataSourceSchemas -- see fakeProviderServerV6.GetProviderSchema's
		// own identical DataSourceSchemas entry for the full account of why
		// this exists and why it deliberately reuses "fake_widget" as both
		// a resource and a data source name.
		DataSourceSchemas: map[string]*tfplugin5.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin5.Schema_Block{
					Attributes: []*tfplugin5.Schema_Attribute{
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Computed: true},
					},
				},
			},
		},
	}, nil
}

func (s *fakeProviderServerV5) Configure(context.Context, *tfplugin5.Configure_Request) (*tfplugin5.Configure_Response, error) {
	return &tfplugin5.Configure_Response{}, nil
}

func (s *fakeProviderServerV5) ReadResource(_ context.Context, req *tfplugin5.ReadResource_Request) (*tfplugin5.ReadResource_Response, error) {
	if id, ok := currentStateID(req.CurrentState.GetMsgpack()); ok && isDestroyed(id) {
		return &tfplugin5.ReadResource_Response{}, nil
	}
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ReadResource_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

func (s *fakeProviderServerV5) ApplyResourceChange(ctx context.Context, req *tfplugin5.ApplyResourceChange_Request) (*tfplugin5.ApplyResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin5.ApplyResourceChange_Response{
			Diagnostics: []*tfplugin5.Diagnostic{{
				Severity: tfplugin5.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	case "slow":
		select {
		case <-time.After(fakeProviderSlowApplyDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "fail-create-not-found":
		// See fakeProviderServerV6.ApplyResourceChange's matching comment
		// (UBI-92).
		if name, ok := currentStateName(req.PlannedState.GetMsgpack()); ok && failCreateNotFound(name) {
			return &tfplugin5.ApplyResourceChange_Response{
				Diagnostics: []*tfplugin5.Diagnostic{{
					Severity: tfplugin5.Diagnostic_ERROR,
					Summary:  "NoSuchEntity",
					Detail:   fmt.Sprintf("fakeprovider: simulated eventual-consistency lag -- the resource with name %q cannot be found (UBI-92 fail-create-not-found)", name),
				}},
			}, nil
		}
	}
	if id, ok, isDestroy := destroyRequestID(req.PriorState.GetMsgpack(), req.PlannedState.GetMsgpack()); isDestroy {
		// See fakeProviderServerV6.ApplyResourceChange's matching comment
		// (UBI-30): PlannedPrivate is required, not optional, mirroring the
		// empirically-found real-provider requirement -- this fixture
		// refuses to silently no-op a destroy the way that real bug did.
		if len(req.PlannedPrivate) == 0 {
			return &tfplugin5.ApplyResourceChange_Response{
				Diagnostics: []*tfplugin5.Diagnostic{{
					Severity: tfplugin5.Diagnostic_ERROR,
					Summary:  "destroy called without PlannedPrivate",
					Detail:   "fakeprovider: a real destroy requires a prior PlanResourceChange call (UBI-30) -- this fixture refuses to silently no-op the way a real provider's own shim was found to",
				}},
			}, nil
		}
		// "lying-destroy" (UBI-44): see fakeProviderServerV6's matching
		// comment -- skips markDestroyed, reporting the identical clean
		// success a real destroy produces while the resource stays
		// reachable on the next ReadResource.
		if ok && os.Getenv("FAKEPROVIDER_APPLY_MODE") != "lying-destroy" {
			markDestroyed(id)
			removePersistedState(req.PriorState.GetMsgpack())
		}
		return &tfplugin5.ApplyResourceChange_Response{}, nil
	}
	out, err := echoAppliedState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ApplyResourceChange_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

// PlanResourceChange mirrors fakeProviderServerV6.PlanResourceChange (UBI-30)
// over the tfplugin5 wire protocol.
func (s *fakeProviderServerV5) PlanResourceChange(ctx context.Context, req *tfplugin5.PlanResourceChange_Request) (*tfplugin5.PlanResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin5.PlanResourceChange_Response{
			Diagnostics: []*tfplugin5.Diagnostic{{
				Severity: tfplugin5.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &tfplugin5.PlanResourceChange_Response{
		PlannedState:   req.ProposedNewState,
		PlannedPrivate: []byte("fakeprovider-planned-private"),
	}, nil
}

// fakeWidgetType mirrors the fake_widget schema advertised above (id/name
// string, tags map of string) as a cty type, so this fixture can do real
// cty-msgpack decode/encode — same wire encoding ubx's provider package
// uses against real binaries — rather than just echoing opaque bytes.
var fakeWidgetType = cty.Object(map[string]cty.Type{
	"id":   cty.String,
	"name": cty.String,
	"tags": cty.Map(cty.String),
})

// echoWidgetState stands in for a real provider's ReadResource logic: it
// decodes the requested current_state, fills in "id" if the caller left it
// null (as a real provider would compute it), and re-encodes — proving a
// real cty-msgpack round trip happened rather than trusting a canned
// response.
//
// If FAKEPROVIDER_EXTRA_TAG ("key=value") is set, it's merged into the
// response's tags regardless of what current_state asked for. This lets a
// test simulate a real out-of-band mutation (the same thing "aws s3api
// put-bucket-tagging" did in the real-world verification) between two
// separate process launches that pass the identical lookup both times —
// varying the lookup itself doesn't model that scenario, since a real
// caller keeps asking the same question and gets a different answer.
//
// Deliberately NOT reused for ApplyResourceChange (see echoAppliedState):
// a real provider's Apply is a deterministic function of exactly what it
// was asked to plan, never something that also re-discovers an unrelated
// out-of-band change on the side -- that's what ReadResource/reconciliation
// are for. Reusing this same "re-inject the drift" behavior for apply too
// would make it impossible for a test (or a manual CLI run, UBI-26 session
// 3) to tell "ubx correctly reverted this" apart from "the fixture
// re-injected the drift into its own apply response regardless."
func echoWidgetState(msgpackBytes []byte) ([]byte, error) {
	if name, ok := currentStateName(msgpackBytes); ok {
		if persisted, found := loadPersistedWidgetState(name); found {
			msgpackBytes = persisted
		}
	}
	vals, err := decodeWidgetState(msgpackBytes)
	if err != nil {
		return nil, err
	}
	if extra := os.Getenv("FAKEPROVIDER_EXTRA_TAG"); extra != "" {
		if k, v, ok := strings.Cut(extra, "="); ok {
			tags := vals["tags"].AsValueMap()
			if tags == nil {
				tags = map[string]cty.Value{}
			}
			tags[k] = cty.StringVal(v)
			vals["tags"] = cty.MapVal(tags)
		}
	}
	return ctymsgpack.Marshal(cty.ObjectVal(vals), fakeWidgetType)
}

// echoAppliedState stands in for a real provider's ApplyResourceChange
// logic (UBI-26): decodes planned_state, fills in "id" if left null (the
// same computed-attribute convention echoWidgetState uses), and re-encodes
// exactly what it was asked to plan -- no FAKEPROVIDER_EXTRA_TAG
// reinjection, since a real Apply is deterministic in its own inputs.
func echoAppliedState(msgpackBytes []byte) ([]byte, error) {
	vals, err := decodeWidgetState(msgpackBytes)
	if err != nil {
		return nil, err
	}
	out, err := ctymsgpack.Marshal(cty.ObjectVal(vals), fakeWidgetType)
	if err != nil {
		return nil, err
	}
	if name := vals["name"]; !name.IsNull() && name.IsKnown() && name.AsString() != "" {
		if err := persistWidgetState(name.AsString(), out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fakeProviderStateDir returns FAKEPROVIDER_STATE_DIR, empty if unset (see
// the package doc comment).
func fakeProviderStateDir() string {
	return os.Getenv("FAKEPROVIDER_STATE_DIR")
}

// persistedStatePath is keyed by the widget's own "name", not "id" --
// fake_widget's schema-Computed "id" is deterministically the SAME literal
// "computed-id" for every instance (see decodeWidgetState), so it can never
// disambiguate between two different fake_widget resources; "name" is the
// one attribute this fixture's own callers always supply distinctly per
// resource (its own address name), and — for this exact reason — is
// already what a real ship's own Lookup carries alongside "id" for this
// type (fake_widget's schema marks "name" Required, so
// cli/stateadapter.go's own required-attr computation already includes it;
// confirmed via a live apply record during the UBI-85 finale).
func persistedStatePath(dir, name string) string {
	return filepath.Join(dir, url.PathEscape(name)+".msgpack")
}

// persistWidgetState is echoAppliedState's own write side — a no-op
// whenever FAKEPROVIDER_STATE_DIR isn't set, preserving every existing
// test's pure-echo behavior exactly.
func persistWidgetState(name string, msgpackBytes []byte) error {
	dir := fakeProviderStateDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("fakeprovider: persist state: %w", err)
	}
	if err := os.WriteFile(persistedStatePath(dir, name), msgpackBytes, 0o644); err != nil {
		return fmt.Errorf("fakeprovider: persist state: %w", err)
	}
	return nil
}

// loadPersistedWidgetState is echoWidgetState's own read side — found is
// false whenever FAKEPROVIDER_STATE_DIR isn't set or name was never
// persisted, in which case the caller falls back to its original
// echo-the-request behavior.
func loadPersistedWidgetState(name string) (msgpackBytes []byte, found bool) {
	dir := fakeProviderStateDir()
	if dir == "" {
		return nil, false
	}
	b, err := os.ReadFile(persistedStatePath(dir, name))
	if err != nil {
		return nil, false
	}
	return b, true
}

// removePersistedState is a destroy's own cleanup — a no-op whenever
// FAKEPROVIDER_STATE_DIR isn't set or priorMsgpackBytes carries no "name"
// (best-effort, mirroring markDestroyed's own id-based tracking, which
// this is independent of).
func removePersistedState(priorMsgpackBytes []byte) {
	dir := fakeProviderStateDir()
	if dir == "" {
		return
	}
	if name, ok := currentStateName(priorMsgpackBytes); ok {
		os.Remove(persistedStatePath(dir, name))
	}
}

// decodeWidgetState is echoWidgetState/echoAppliedState's shared decode +
// computed-id-fill-in step. "id" is filled in when it's either Null (the
// pre-UBI-27 convention: an absent JSON key decoded through
// encodeDynamicValue) or genuinely Unknown (UBI-27:
// encodeUnknownAwareDynamicValue's own real cty.UnknownVal, for a
// from-scratch create or an explicit $computed marker) -- a real
// SDKv2-vintage provider's Apply only ever actually computes an attribute
// it finds Unknown, never one it finds Null (docs/executor.md's own
// empirical finding), but this fixture accepts either shape so it keeps
// working against both encoders' output.
func decodeWidgetState(msgpackBytes []byte) (map[string]cty.Value, error) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil {
		return nil, fmt.Errorf("fakeprovider: decode state: %w", err)
	}
	vals := val.AsValueMap()
	if id := vals["id"]; id.IsNull() || !id.IsKnown() {
		vals["id"] = cty.StringVal("computed-id")
	}
	if tags := vals["tags"]; tags.IsNull() || !tags.IsKnown() {
		vals["tags"] = cty.MapValEmpty(cty.String)
	}
	return vals, nil
}

// destroyedIDs tracks fake_widget ids ApplyResourceChange has destroyed
// (UBI-30), this process's own lifetime only -- this fixture is otherwise
// fully stateless (every ReadResource call is a pure function of whatever
// current_state ubx itself supplies), but a destroy specifically needs the
// fixture to remember what it did: ubx's own precheck-then-apply-then-
// maybe-reconcile sequence, all within one `ubx ship` invocation, sends
// the exact same lookup on every read and expects a DIFFERENT answer after
// the destroy lands.
var (
	destroyedMu  sync.Mutex
	destroyedIDs = map[string]bool{}
)

func markDestroyed(id string) {
	destroyedMu.Lock()
	defer destroyedMu.Unlock()
	destroyedIDs[id] = true
}

func isDestroyed(id string) bool {
	destroyedMu.Lock()
	defer destroyedMu.Unlock()
	return destroyedIDs[id]
}

// failCreateAttempts tracks, per fake_widget "name" (UBI-92: "id" can't
// disambiguate -- see decodeWidgetState, every instance computes the same
// literal "computed-id"), how many times FAKEPROVIDER_APPLY_MODE=
// fail-create-not-found has already failed that name's own create --
// this process's own lifetime only, mirroring destroyedIDs' own convention.
var (
	failCreateMu       sync.Mutex
	failCreateAttempts = map[string]int{}
)

// failCreateNotFound reports whether this create attempt for the given
// fake_widget name should be failed with a not-found-shaped diagnostic
// (FAKEPROVIDER_APPLY_MODE=fail-create-not-found), consuming one attempt
// from FAKEPROVIDER_FAIL_CREATE_COUNT's own budget each time it does --
// mirroring a real IAM role's own visibility lag eventually resolving
// after a bounded number of attempts, not staying wrong forever. Every
// name other than FAKEPROVIDER_FAIL_CREATE_TARGET is unaffected.
func failCreateNotFound(name string) bool {
	if name == "" || name != os.Getenv("FAKEPROVIDER_FAIL_CREATE_TARGET") {
		return false
	}
	budget, _ := strconv.Atoi(os.Getenv("FAKEPROVIDER_FAIL_CREATE_COUNT"))
	if budget <= 0 {
		return false
	}
	failCreateMu.Lock()
	defer failCreateMu.Unlock()
	if failCreateAttempts[name] >= budget {
		return false
	}
	failCreateAttempts[name]++
	return true
}

// currentStateName is currentStateID's own twin for "name" -- the
// persistence key (see persistedStatePath) -- best-effort in the same way.
func currentStateName(msgpackBytes []byte) (name string, ok bool) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil || val.IsNull() {
		return "", false
	}
	nameVal := val.GetAttr("name")
	if nameVal.IsNull() || !nameVal.IsKnown() || nameVal.AsString() == "" {
		return "", false
	}
	return nameVal.AsString(), true
}

// currentStateID extracts "id" from a ReadResource request's current_state
// -- best-effort: ok is false for anything that doesn't decode as a
// fake_widget-shaped value with a known, non-null id (never an error, this
// is purely an optional destroyed-check, not a real read).
func currentStateID(msgpackBytes []byte) (id string, ok bool) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil || val.IsNull() {
		return "", false
	}
	idVal := val.GetAttr("id")
	if idVal.IsNull() || !idVal.IsKnown() {
		return "", false
	}
	return idVal.AsString(), true
}

// destroyRequestID reports whether an ApplyResourceChange request is a
// destroy (UBI-30: planned_state decodes to a genuine null value, ubx's
// own shipDestroyNode-constructed PlannedState) and, if so, prior_state's
// own "id" -- what markDestroyed should remember. isDestroy is true
// whenever planned_state is null, even if prior_state's own id couldn't be
// read (ok=false then) -- the caller still must not fall through to
// echoAppliedState, which requires a non-null planned_state to decode at
// all.
func destroyRequestID(priorMsgpackBytes, plannedMsgpackBytes []byte) (id string, ok bool, isDestroy bool) {
	plannedVal, err := ctymsgpack.Unmarshal(plannedMsgpackBytes, fakeWidgetType)
	if err != nil || !plannedVal.IsNull() {
		return "", false, false
	}
	id, ok = currentStateID(priorMsgpackBytes)
	return id, ok, true
}

// --- conformance mode: a generic, env-var-shaped resource, one per UBI-9
// fake-only type. See the package doc comment for the env var contract.

// conformanceAttrs reads FAKEPROVIDER_ATTRS. "id" is always included even if
// the caller forgot it — every real AWS resource schema has one, and
// RunAdoptMutateScanDiff's lookup always keys off it.
func conformanceAttrs() []string {
	raw := os.Getenv("FAKEPROVIDER_ATTRS")
	var attrs []string
	if raw != "" {
		attrs = strings.Split(raw, ",")
	}
	for _, a := range attrs {
		if a == "id" {
			return attrs
		}
	}
	return append([]string{"id"}, attrs...)
}

// conformanceAttrTypes reads FAKEPROVIDER_ATTR_TYPES ("name:kind,..."): an
// attribute name's non-default cty shape (UBI-63 session 3) — see the
// package doc comment. Attributes not listed here keep conformanceCtyType's
// original default (string, or map for "tags"/"tags_all").
func conformanceAttrTypes() map[string]string {
	raw := os.Getenv("FAKEPROVIDER_ATTR_TYPES")
	if raw == "" {
		return nil
	}
	m := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		name, kind, ok := strings.Cut(pair, ":")
		if ok {
			m[name] = kind
		}
	}
	return m
}

// conformanceComputedAttrs reads FAKEPROVIDER_COMPUTED_ATTRS (UBI-63
// session 3): additional attribute names (beyond "id", always Computed) to
// advertise as Computed rather than Optional — see the package doc comment.
func conformanceComputedAttrs() map[string]bool {
	raw := os.Getenv("FAKEPROVIDER_COMPUTED_ATTRS")
	if raw == "" {
		return nil
	}
	m := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		m[name] = true
	}
	return m
}

// conformanceCtyType builds the cty object type for conformanceAttrs():
// "tags"/"tags_all" as string maps (matching every AWS resource that has
// them), FAKEPROVIDER_ATTR_TYPES overrides as declared, everything else as
// plain strings — see the package doc comment for why scalar type-fidelity
// to AWS's real attribute types doesn't matter beyond what a given test
// actually needs to model (ubx's own core layer treats Observed state as
// opaque JSON, never type-checked against the schema).
func conformanceCtyType() cty.Type {
	types := conformanceAttrTypes()
	fields := make(map[string]cty.Type, len(conformanceAttrs()))
	for _, name := range conformanceAttrs() {
		switch {
		case name == "tags" || name == "tags_all":
			fields[name] = cty.Map(cty.String)
		case types[name] == "bool":
			fields[name] = cty.Bool
		case types[name] == "number":
			fields[name] = cty.Number
		case types[name] == "list":
			fields[name] = cty.List(cty.String)
		case types[name] == "map":
			fields[name] = cty.Map(cty.String)
		default:
			fields[name] = cty.String
		}
	}
	return cty.Object(fields)
}

// conformanceSensitiveAttrs reads FAKEPROVIDER_SENSITIVE_ATTRS (UBI-23):
// which of conformanceAttrs() to advertise as Sensitive.
func conformanceSensitiveAttrs() map[string]bool {
	raw := os.Getenv("FAKEPROVIDER_SENSITIVE_ATTRS")
	if raw == "" {
		return nil
	}
	m := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		m[name] = true
	}
	return m
}

func conformanceResourceType() string {
	t := os.Getenv("FAKEPROVIDER_RESOURCE_TYPE")
	if t == "" {
		t = "fake_conformance_resource"
	}
	return t
}

// echoConformanceState mirrors echoWidgetState's role (real cty-msgpack
// round trip, computed "id" fill-in, injected mutation) but against a
// dynamically shaped object instead of the fixed fake_widget type, so one
// mechanism serves every FakeOnly conformance type.
func echoConformanceState(msgpackBytes []byte) ([]byte, error) {
	ty := conformanceCtyType()
	val, err := ctymsgpack.Unmarshal(msgpackBytes, ty)
	if err != nil {
		return nil, fmt.Errorf("fakeprovider: decode current_state: %w", err)
	}
	vals := val.AsValueMap()

	// UBI-123: !v.IsKnown(), not just v.IsNull() -- ReadResource's own
	// CurrentState never carries an unknown value (there is no such thing
	// as "unknown current state"), so IsNull() alone was enough for this
	// function's original ReadResource-only use. ApplyResourceChange's own
	// PlannedState is different: a Computed-and-unset attribute is
	// genuinely UNKNOWN there, per the tfplugin protocol's own real
	// semantics, not null -- and a real ApplyResourceChange response is
	// never allowed to still carry an unknown value. Echoing one back
	// unfilled (this function's original id-fill checked IsNull() only)
	// is exactly what produced "decode value: value is not known" the
	// first time this function was reused for Apply (UBI-123's own
	// hermetic repro work).
	if idVal, ok := vals["id"]; ok && (idVal.IsNull() || !idVal.IsKnown()) {
		vals["id"] = cty.StringVal("computed-" + conformanceResourceType() + "-id")
	}
	for name, v := range vals {
		if (name == "tags" || name == "tags_all") && (v.IsNull() || !v.IsKnown()) {
			vals[name] = cty.MapValEmpty(cty.String)
		}
	}

	mutateAttr := os.Getenv("FAKEPROVIDER_MUTATE_ATTR")
	mutateValue := os.Getenv("FAKEPROVIDER_MUTATE_VALUE")
	if mutateAttr != "" {
		if cur, ok := vals[mutateAttr]; ok {
			if cur.Type().IsMapType() {
				m := cur.AsValueMap()
				if m == nil {
					m = map[string]cty.Value{}
				}
				if k, v, ok := strings.Cut(mutateValue, "="); ok {
					m[k] = cty.StringVal(v)
					vals[mutateAttr] = cty.MapVal(m)
				}
			} else {
				vals[mutateAttr] = cty.StringVal(mutateValue)
			}
		}
	}

	return ctymsgpack.Marshal(cty.ObjectVal(vals), ty)
}

func conformanceSchemaAttributesV6() []*tfplugin6.Schema_Attribute {
	sensitive := conformanceSensitiveAttrs()
	computed := conformanceComputedAttrs()
	types := conformanceAttrTypes()
	var attrs []*tfplugin6.Schema_Attribute
	for _, name := range conformanceAttrs() {
		a := &tfplugin6.Schema_Attribute{Name: name, Type: []byte(`"string"`), Optional: true}
		switch {
		case name == "tags" || name == "tags_all":
			a.Type = []byte(`["map","string"]`)
		case types[name] == "bool":
			a.Type = []byte(`"bool"`)
		case types[name] == "number":
			a.Type = []byte(`"number"`)
		case types[name] == "list":
			a.Type = []byte(`["list","string"]`)
		case types[name] == "map":
			a.Type = []byte(`["map","string"]`)
		}
		if name == "id" || computed[name] {
			a.Computed, a.Optional = true, false
		}
		if sensitive[name] {
			a.Sensitive = true
		}
		attrs = append(attrs, a)
	}
	return attrs
}

func conformanceSchemaAttributesV5() []*tfplugin5.Schema_Attribute {
	sensitive := conformanceSensitiveAttrs()
	computed := conformanceComputedAttrs()
	types := conformanceAttrTypes()
	var attrs []*tfplugin5.Schema_Attribute
	for _, name := range conformanceAttrs() {
		a := &tfplugin5.Schema_Attribute{Name: name, Type: []byte(`"string"`), Optional: true}
		switch {
		case name == "tags" || name == "tags_all":
			a.Type = []byte(`["map","string"]`)
		case types[name] == "bool":
			a.Type = []byte(`"bool"`)
		case types[name] == "number":
			a.Type = []byte(`"number"`)
		case types[name] == "list":
			a.Type = []byte(`["list","string"]`)
		case types[name] == "map":
			a.Type = []byte(`["map","string"]`)
		}
		if name == "id" || computed[name] {
			a.Computed, a.Optional = true, false
		}
		if sensitive[name] {
			a.Sensitive = true
		}
		attrs = append(attrs, a)
	}
	return attrs
}

type fakeConformanceServerV6 struct {
	tfplugin6.UnimplementedProviderServer
}

func (s *fakeConformanceServerV6) GetProviderSchema(context.Context, *tfplugin6.GetProviderSchema_Request) (*tfplugin6.GetProviderSchema_Response, error) {
	return &tfplugin6.GetProviderSchema_Response{
		Provider: &tfplugin6.Schema{
			Block: &tfplugin6.Schema_Block{
				Attributes: []*tfplugin6.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin6.Schema{
			conformanceResourceType(): {
				Version: 1,
				Block:   &tfplugin6.Schema_Block{Attributes: conformanceSchemaAttributesV6()},
			},
		},
	}, nil
}

func (s *fakeConformanceServerV6) ConfigureProvider(context.Context, *tfplugin6.ConfigureProvider_Request) (*tfplugin6.ConfigureProvider_Response, error) {
	return &tfplugin6.ConfigureProvider_Response{}, nil
}

func (s *fakeConformanceServerV6) ReadResource(_ context.Context, req *tfplugin6.ReadResource_Request) (*tfplugin6.ReadResource_Response, error) {
	out, err := echoConformanceState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ReadResource_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

// ApplyResourceChange (UBI-123) -- added alongside ReadResource so a
// conformance-mode fixture can serve a real CREATE, not just drift-check
// reads: echoes PlannedState back exactly like ReadResource echoes
// CurrentState (echoConformanceState's own id-fill-in/mutate logic is
// generic to whichever msgpack bytes it's handed), which is exactly what
// this repo's own real create-path encode-fidelity tests need -- a
// resource type/attribute shape defined per-test via env vars, round-
// tripped through a REAL cty-msgpack-encoded gRPC call to a REAL separate
// process, not a mock. Destroy/private-state handling deliberately not
// added here (fakeProviderServerV6's own ApplyResourceChange already
// covers that, for fake_widget) -- conformance mode's own real job is
// drift detection (adopt/mutate/scan-diff), a create-path fixture is this
// addition's only new use.
func (s *fakeConformanceServerV6) ApplyResourceChange(_ context.Context, req *tfplugin6.ApplyResourceChange_Request) (*tfplugin6.ApplyResourceChange_Response, error) {
	out, err := echoConformanceState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ApplyResourceChange_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

type fakeConformanceServerV5 struct {
	tfplugin5.UnimplementedProviderServer
}

func (s *fakeConformanceServerV5) GetSchema(context.Context, *tfplugin5.GetProviderSchema_Request) (*tfplugin5.GetProviderSchema_Response, error) {
	return &tfplugin5.GetProviderSchema_Response{
		Provider: &tfplugin5.Schema{
			Block: &tfplugin5.Schema_Block{
				Attributes: []*tfplugin5.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin5.Schema{
			conformanceResourceType(): {
				Version: 1,
				Block:   &tfplugin5.Schema_Block{Attributes: conformanceSchemaAttributesV5()},
			},
		},
	}, nil
}

func (s *fakeConformanceServerV5) Configure(context.Context, *tfplugin5.Configure_Request) (*tfplugin5.Configure_Response, error) {
	return &tfplugin5.Configure_Response{}, nil
}

func (s *fakeConformanceServerV5) ReadResource(_ context.Context, req *tfplugin5.ReadResource_Request) (*tfplugin5.ReadResource_Response, error) {
	out, err := echoConformanceState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ReadResource_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

// ApplyResourceChange -- see fakeConformanceServerV6's own identical
// addition (UBI-123) for the full reasoning; mirrored here for v5.
func (s *fakeConformanceServerV5) ApplyResourceChange(_ context.Context, req *tfplugin5.ApplyResourceChange_Request) (*tfplugin5.ApplyResourceChange_Response, error) {
	out, err := echoConformanceState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ApplyResourceChange_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

func serveConformanceV6() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	tfplugin6.RegisterProviderServer(srv, &fakeConformanceServerV6{})
	fmt.Printf("1|6|unix|%s|grpc\n", lis.Addr().String())
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

func serveConformanceV5() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	tfplugin5.RegisterProviderServer(srv, &fakeConformanceServerV5{})
	fmt.Printf("1|5|unix|%s|grpc\n", lis.Addr().String())
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}
