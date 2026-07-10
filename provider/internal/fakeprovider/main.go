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
//	bad-core            handshake line with the wrong core (go-plugin) protocol version
//	unsupported-version handshake line with an app protocol version ubx has no wire impl for
//	bad-protocol        handshake line with a non-grpc wire protocol
//	malformed           an unparseable line
//	hang                never writes anything to stdout
//	crash                exits immediately with no output
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"

	"github.com/ubiquex/ubiquex-cli/provider/tfplugin5"
	"github.com/ubiquex/ubiquex-cli/provider/tfplugin6"
)

func main() {
	mode := os.Getenv("FAKEPROVIDER_MODE")

	switch mode {
	case "ok-v6":
		serveV6()
	case "ok-v5":
		serveV5()
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
	}, nil
}

func (s *fakeProviderServerV6) ReadResource(_ context.Context, req *tfplugin6.ReadResource_Request) (*tfplugin6.ReadResource_Response, error) {
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ReadResource_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
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
	}, nil
}

func (s *fakeProviderServerV5) ReadResource(_ context.Context, req *tfplugin5.ReadResource_Request) (*tfplugin5.ReadResource_Response, error) {
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ReadResource_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
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
func echoWidgetState(msgpackBytes []byte) ([]byte, error) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil {
		return nil, fmt.Errorf("fakeprovider: decode current_state: %w", err)
	}
	vals := val.AsValueMap()
	if vals["id"].IsNull() {
		vals["id"] = cty.StringVal("computed-id")
	}
	if vals["tags"].IsNull() {
		vals["tags"] = cty.MapValEmpty(cty.String)
	}
	return ctymsgpack.Marshal(cty.ObjectVal(vals), fakeWidgetType)
}
