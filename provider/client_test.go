package provider

import (
	"context"
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

func TestLaunch_HappyPath_GetProviderSchema(t *testing.T) {
	client, err := launchFake(t, "ok")
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetProviderSchema(ctx)
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}

	widget, ok := resp.ResourceSchemas["fake_widget"]
	if !ok {
		t.Fatalf("resource schema %q missing from response: %+v", "fake_widget", resp.ResourceSchemas)
	}
	if len(widget.Block.Attributes) != 3 {
		t.Fatalf("got %d attributes, want 3", len(widget.Block.Attributes))
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

func TestLaunch_ProtocolMismatch(t *testing.T) {
	cases := []string{"bad-core", "bad-app", "bad-protocol"}
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
