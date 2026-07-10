// Command fakeprovider is a test fixture only: it speaks (or deliberately
// misspeaks) just enough of the tfplugin6 handshake to exercise
// provider.Launch's success and adversarial paths without depending on a
// real Terraform provider binary.
//
// Mode is selected by the FAKEPROVIDER_MODE environment variable (not argv:
// real provider binaries are launched with no arguments, so tests drive
// this fixture the same way Launch drives a real provider, via
// provider.WithEnv):
//
//	ok           valid handshake, serves a minimal real schema over gRPC
//	bad-core     handshake line with the wrong core protocol version
//	bad-app      handshake line with the wrong tfplugin app protocol version
//	bad-protocol handshake line with a non-grpc wire protocol
//	malformed    an unparseable line
//	hang         never writes anything to stdout
//	crash        exits immediately with no output
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"

	"github.com/ubiquex/ubiquex-cli/provider/tfplugin6"
)

func main() {
	mode := os.Getenv("FAKEPROVIDER_MODE")

	switch mode {
	case "ok":
		serveOK()
	case "bad-core":
		fmt.Println("99|6|unix|/tmp/does-not-matter|grpc")
		block()
	case "bad-app":
		fmt.Println("1|5|unix|/tmp/does-not-matter|grpc")
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

func serveOK() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	tfplugin6.RegisterProviderServer(srv, &fakeProviderServer{})

	fmt.Printf("1|6|unix|%s|grpc\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
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

type fakeProviderServer struct {
	tfplugin6.UnimplementedProviderServer
}

// GetProviderSchema returns a small, fixed schema exposing exactly one
// resource type (fake_widget), enough to exercise a real dump end to end.
func (s *fakeProviderServer) GetProviderSchema(context.Context, *tfplugin6.GetProviderSchema_Request) (*tfplugin6.GetProviderSchema_Response, error) {
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
