package provider

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Terraform plugin protocol constants. These values are part of the wire
// protocol contract between a plugin host (us) and Terraform provider
// binaries; they are not configurable. Verified against
// github.com/hashicorp/terraform-plugin-go@v0.31.0 (tfprotov5/tf5server,
// tfprotov6/tf6server).
const (
	magicCookieKey   = "TF_PLUGIN_MAGIC_COOKIE"
	magicCookieValue = "d602bf8f470bc67ca7faa0386276bbdd4330efaf76d1a219cb4d6991ca9872b2"

	// coreProtocolVersion is go-plugin's own handshake protocol version
	// (the first field of the handshake line), independent of the tfplugin
	// app protocol version.
	coreProtocolVersion = "1"

	wireProtocolGRPC = "grpc"
)

// supportedAppProtocolVersions lists every tfplugin app protocol version
// ubx has a wire implementation for (see provider.go). Advertised to
// plugins via PLUGIN_PROTOCOL_VERSIONS so a dual-protocol-capable binary
// can pick the best mutually supported one; a single-protocol binary just
// reports whichever it has, and we check it against this set.
var supportedAppProtocolVersions = []int{5, 6}

func supportedAppProtocolVersionsEnv() string {
	strs := make([]string, len(supportedAppProtocolVersions))
	for i, v := range supportedAppProtocolVersions {
		strs[i] = strconv.Itoa(v)
	}
	return strings.Join(strs, ",")
}

func isSupportedAppProtocolVersion(v int) bool {
	for _, sv := range supportedAppProtocolVersions {
		if sv == v {
			return true
		}
	}
	return false
}

var (
	// ErrMalformedHandshake means the plugin's handshake line could not be
	// parsed at all (wrong field count, unrecognized network type, ...).
	ErrMalformedHandshake = errors.New("malformed plugin handshake")

	// ErrProtocolMismatch means the plugin spoke a handshake we understood
	// structurally, but declared a core protocol version, app protocol
	// version, or wire protocol ubx does not support.
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
	appProtocolVersion int    // 5 or 6 — which tfplugin wire protocol was negotiated
	network            string // "unix" or "tcp"
	addr               string
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

	appVersion, err := strconv.Atoi(parts[1])
	if err != nil {
		return handshakeInfo{}, fmt.Errorf("%w: unparseable app protocol version %q", ErrMalformedHandshake, parts[1])
	}
	if !isSupportedAppProtocolVersion(appVersion) {
		return handshakeInfo{}, fmt.Errorf("%w: tfplugin protocol version %d, ubx speaks %v",
			ErrProtocolMismatch, appVersion, supportedAppProtocolVersions)
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

	return handshakeInfo{appProtocolVersion: appVersion, network: network, addr: addr}, nil
}
