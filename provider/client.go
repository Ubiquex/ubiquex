// Package provider launches Terraform provider binaries and speaks the
// tfplugin v6 gRPC protocol to them directly. No Terraform, OpenTofu, or
// Pulumi engine is involved (see docs/architecture.md — Execution layer).
package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ubiquex/ubiquex-cli/provider/tfplugin6"
)

// defaultHandshakeTimeout bounds how long Launch waits for the provider
// binary to print its handshake line before giving up.
const defaultHandshakeTimeout = 10 * time.Second

// Option configures Launch.
type Option func(*config)

type config struct {
	handshakeTimeout time.Duration
	extraEnv         []string
}

// WithHandshakeTimeout overrides the default wait for the plugin's
// handshake line.
func WithHandshakeTimeout(d time.Duration) Option {
	return func(c *config) { c.handshakeTimeout = d }
}

// WithEnv adds extra "KEY=VALUE" entries to the launched process's
// environment, on top of the current process's environment and the plugin
// magic cookie Launch always sets.
func WithEnv(vars ...string) Option {
	return func(c *config) { c.extraEnv = append(c.extraEnv, vars...) }
}

// Client is a launched provider binary, connected over tfplugin v6.
type Client struct {
	Provider tfplugin6.ProviderClient

	cmd    *exec.Cmd
	conn   *grpc.ClientConn
	stderr *syncBuffer
}

// Launch starts the provider binary at path, performs the tfplugin
// handshake, and dials the gRPC server it reports. The returned Client owns
// the subprocess and connection; call Close when done with it.
func Launch(ctx context.Context, path string, opts ...Option) (*Client, error) {
	cfg := config{handshakeTimeout: defaultHandshakeTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}

	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), magicCookieKey+"="+magicCookieValue)
	cmd.Env = append(cmd.Env, cfg.extraEnv...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("provider %q: create stdout pipe: %w", path, err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("provider %q: launch: %w", path, err)
	}

	line, err := readHandshakeLine(stdout, cfg.handshakeTimeout)
	if err != nil {
		_ = killAndWait(cmd)
		return nil, annotateWithStderr(fmt.Errorf("provider %q: %w", path, err), stderr)
	}

	hs, err := parseHandshakeLine(line)
	if err != nil {
		_ = killAndWait(cmd)
		return nil, annotateWithStderr(fmt.Errorf("provider %q: %w", path, err), stderr)
	}

	conn, err := grpc.NewClient(
		"passthrough:///"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, hs.network, hs.addr)
		}),
	)
	if err != nil {
		_ = killAndWait(cmd)
		return nil, fmt.Errorf("provider %q: dial %s %s: %w", path, hs.network, hs.addr, err)
	}

	return &Client{
		Provider: tfplugin6.NewProviderClient(conn),
		cmd:      cmd,
		conn:     conn,
		stderr:   stderr,
	}, nil
}

// GetProviderSchema fetches the provider's schema, including every managed
// resource and data source type it declares.
func (c *Client) GetProviderSchema(ctx context.Context) (*tfplugin6.GetProviderSchema_Response, error) {
	return c.Provider.GetProviderSchema(ctx, &tfplugin6.GetProviderSchema_Request{})
}

// Close tears down the gRPC connection and terminates the provider process.
func (c *Client) Close() error {
	var errs []error
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := killAndWait(c.cmd); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// readHandshakeLine reads a single newline-terminated line from r, bounded
// by timeout. It distinguishes a hung plugin (nothing arrives in time) from
// one that exited before writing anything (EOF with no data).
func readHandshakeLine(r io.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && line == "" {
			ch <- result{err: err}
			return
		}
		ch <- result{line: line}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			if errors.Is(res.err, io.EOF) {
				return "", ErrPluginExited
			}
			return "", fmt.Errorf("reading plugin handshake: %w", res.err)
		}
		return res.line, nil
	case <-time.After(timeout):
		return "", ErrHandshakeTimeout
	}
}

// killAndWait terminates cmd's process, if any, and reaps it. A killed
// process reports a non-nil Wait error (signal: killed); that is expected
// and not itself a failure worth surfacing.
func killAndWait(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill provider process: %w", err)
	}
	_ = cmd.Wait()
	return nil
}

// annotateWithStderr appends any captured stderr output to err, since a
// provider's own diagnostics (e.g. why it refused to start) land there.
func annotateWithStderr(err error, stderr *syncBuffer) error {
	if tail := stderr.String(); tail != "" {
		return fmt.Errorf("%w\nprovider stderr: %s", err, tail)
	}
	return err
}

// syncBuffer is an io.Writer safe for concurrent Write (from the
// subprocess's stderr pump goroutine managed by os/exec) and String (from
// the caller, after or during process teardown).
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
