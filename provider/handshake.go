package provider

import (
	"errors"
	"fmt"
	"strings"
)

// Terraform plugin protocol constants. These values are part of the wire
// protocol contract between a plugin host (us) and Terraform provider
// binaries; they are not configurable. Verified against
// github.com/hashicorp/terraform-plugin-go@v0.31.0 (tfprotov6/tf6server).
const (
	magicCookieKey   = "TF_PLUGIN_MAGIC_COOKIE"
	magicCookieValue = "d602bf8f470bc67ca7faa0386276bbdd4330efaf76d1a219cb4d6991ca9872b2"

	// coreProtocolVersion is go-plugin's own handshake protocol version
	// (the first field of the handshake line), independent of the tfplugin
	// app protocol version.
	coreProtocolVersion = "1"

	// appProtocolVersion is the tfplugin protocol major version this client
	// speaks. ubx targets v6 only (see docs/architecture.md — protocol v6
	// only, conformance suite grows per provider).
	appProtocolVersion = "6"

	wireProtocolGRPC = "grpc"
)

var (
	// ErrMalformedHandshake means the plugin's handshake line could not be
	// parsed at all (wrong field count, unrecognized network type, ...).
	ErrMalformedHandshake = errors.New("malformed plugin handshake")

	// ErrProtocolMismatch means the plugin spoke a handshake we understood
	// structurally, but declared a core/app protocol version or wire
	// protocol ubx does not support.
	ErrProtocolMismatch = errors.New("plugin protocol mismatch")

	// ErrHandshakeTimeout means the plugin never produced a handshake line
	// within the configured timeout.
	ErrHandshakeTimeout = errors.New("timed out waiting for plugin handshake")

	// ErrPluginExited means the plugin process exited (stdout closed)
	// before it produced a handshake line.
	ErrPluginExited = errors.New("plugin exited before completing handshake")
)

// handshakeInfo is the parsed result of a plugin's handshake line:
//
//	CORE-PROTOCOL-VERSION|APP-PROTOCOL-VERSION|NETWORK-TYPE|NETWORK-ADDR|PROTOCOL|[SERVER-CERT]
type handshakeInfo struct {
	network string // "unix" or "tcp"
	addr    string
}

func parseHandshakeLine(line string) (handshakeInfo, error) {
	line = strings.TrimSpace(line)
	parts := strings.Split(line, "|")
	if len(parts) < 4 {
		return handshakeInfo{}, fmt.Errorf("%w: expected at least 4 fields, got %q", ErrMalformedHandshake, line)
	}

	if parts[0] != coreProtocolVersion {
		return handshakeInfo{}, fmt.Errorf("%w: core protocol version %q, ubx speaks %q",
			ErrProtocolMismatch, parts[0], coreProtocolVersion)
	}

	if parts[1] != appProtocolVersion {
		return handshakeInfo{}, fmt.Errorf("%w: tfplugin protocol version %q, ubx speaks %q",
			ErrProtocolMismatch, parts[1], appProtocolVersion)
	}

	network := parts[2]
	if network != "unix" && network != "tcp" {
		return handshakeInfo{}, fmt.Errorf("%w: unknown network type %q", ErrMalformedHandshake, network)
	}

	addr := parts[3]
	if addr == "" {
		return handshakeInfo{}, fmt.Errorf("%w: empty network address", ErrMalformedHandshake)
	}

	wireProtocol := "netrpc"
	if len(parts) >= 5 && parts[4] != "" {
		wireProtocol = parts[4]
	}
	if wireProtocol != wireProtocolGRPC {
		return handshakeInfo{}, fmt.Errorf("%w: wire protocol %q, ubx only speaks %q",
			ErrProtocolMismatch, wireProtocol, wireProtocolGRPC)
	}

	return handshakeInfo{network: network, addr: addr}, nil
}
