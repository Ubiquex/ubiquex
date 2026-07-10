package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fakeProviderBinary is built once by TestMain and launched by every test
// below (mode selected via FAKEPROVIDER_MODE, see internal/fakeprovider)
// standing in for a real Terraform provider binary.
var fakeProviderBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ubx-provider-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fakeProviderBinary = filepath.Join(dir, "fakeprovider")
	build := exec.Command("go", "build", "-o", fakeProviderBinary, "./internal/fakeprovider")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("building fakeprovider fixture: " + err.Error())
	}

	os.Exit(m.Run())
}

// launchFake runs the fakeprovider fixture in the given mode through the
// real Launch entrypoint, exactly as it would run any provider binary.
func launchFake(t *testing.T, mode string, opts ...Option) (*Client, error) {
	t.Helper()
	opts = append([]Option{WithEnv("FAKEPROVIDER_MODE=" + mode)}, opts...)
	return Launch(context.Background(), fakeProviderBinary, opts...)
}

func TestLaunch_HappyPath_V6(t *testing.T) {
	client, err := launchFake(t, "ok-v6")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer client.Close()

	if got := client.Provider.ProtocolVersion(); got != 6 {
		t.Fatalf("ProtocolVersion() = %d, want 6", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	widget, ok := schemas.Resources["fake_widget"]
	if !ok {
		t.Fatalf("resource schema %q missing from response: %+v", "fake_widget", schemas.Resources)
	}
	if len(widget.Block.Attributes) != 3 {
		t.Fatalf("got %d attributes, want 3", len(widget.Block.Attributes))
	}

	state, err := client.Provider.ReadResource(ctx, widget, "fake_widget", json.RawMessage(`{"name":"a-widget"}`))
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	assertWidgetState(t, state, "a-widget")
}

func TestLaunch_HappyPath_V5(t *testing.T) {
	client, err := launchFake(t, "ok-v5")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer client.Close()

	if got := client.Provider.ProtocolVersion(); got != 5 {
		t.Fatalf("ProtocolVersion() = %d, want 5", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	widget, ok := schemas.Resources["fake_widget"]
	if !ok {
		t.Fatalf("resource schema %q missing from response: %+v", "fake_widget", schemas.Resources)
	}
	if len(widget.Block.Attributes) != 3 {
		t.Fatalf("got %d attributes, want 3", len(widget.Block.Attributes))
	}

	state, err := client.Provider.ReadResource(ctx, widget, "fake_widget", json.RawMessage(`{"name":"a-widget"}`))
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	assertWidgetState(t, state, "a-widget")
}

// assertWidgetState checks a ReadResource result against fakeprovider's
// echoWidgetState behavior: name passed through unchanged, id computed
// (was null on the way in), tags defaulted to {}.
func assertWidgetState(t *testing.T, got json.RawMessage, wantName string) {
	t.Helper()
	var decoded struct {
		ID   string            `json:"id"`
		Name string            `json:"name"`
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode ReadResource response %s: %v", got, err)
	}
	if decoded.Name != wantName {
		t.Fatalf("name = %q, want %q", decoded.Name, wantName)
	}
	if decoded.ID != "computed-id" {
		t.Fatalf("id = %q, want %q (provider should have computed it)", decoded.ID, "computed-id")
	}
	if len(decoded.Tags) != 0 {
		t.Fatalf("tags = %v, want empty", decoded.Tags)
	}
}

func TestLaunch_BinaryMissing(t *testing.T) {
	_, err := Launch(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestLaunch_HandshakeTimeout(t *testing.T) {
	_, err := launchFake(t, "hang", WithHandshakeTimeout(300*time.Millisecond))
	if !errors.Is(err, ErrHandshakeTimeout) {
		t.Fatalf("got %v, want ErrHandshakeTimeout", err)
	}
}

// TestLaunch_ProtocolMismatch covers version-negotiation edge cases: a
// go-plugin core version we don't speak, a tfplugin app version we have no
// wire implementation for, and a non-gRPC wire protocol.
func TestLaunch_ProtocolMismatch(t *testing.T) {
	cases := []string{"bad-core", "unsupported-version", "bad-protocol"}
	for _, mode := range cases {
		t.Run(mode, func(t *testing.T) {
			_, err := launchFake(t, mode, WithHandshakeTimeout(2*time.Second))
			if !errors.Is(err, ErrProtocolMismatch) {
				t.Fatalf("got %v, want ErrProtocolMismatch", err)
			}
		})
	}
}

func TestLaunch_MalformedHandshake(t *testing.T) {
	_, err := launchFake(t, "malformed", WithHandshakeTimeout(2*time.Second))
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("got %v, want ErrMalformedHandshake", err)
	}
}

func TestLaunch_PluginExitsBeforeHandshake(t *testing.T) {
	_, err := launchFake(t, "crash", WithHandshakeTimeout(2*time.Second))
	if !errors.Is(err, ErrPluginExited) {
		t.Fatalf("got %v, want ErrPluginExited", err)
	}
}

// TestLaunch_AdvertisesBothProtocolVersions locks in that Launch always
// tells the plugin it can speak both 5 and 6 (PLUGIN_PROTOCOL_VERSIONS), so
// a dual-protocol-capable provider can pick its best mutually supported
// version rather than falling back to some other default.
func TestLaunch_AdvertisesBothProtocolVersions(t *testing.T) {
	if got, want := supportedAppProtocolVersionsEnv(), "5,6"; got != want {
		t.Fatalf("supportedAppProtocolVersionsEnv() = %q, want %q", got, want)
	}
}
