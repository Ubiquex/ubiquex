package provider

import (
	"errors"
	"testing"
)

func TestParseHandshakeLine_Valid(t *testing.T) {
	hs, err := parseHandshakeLine("1|6|unix|/tmp/plugin123.sock|grpc\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hs.network != "unix" || hs.addr != "/tmp/plugin123.sock" || hs.appProtocolVersion != 6 {
		t.Fatalf("got %+v", hs)
	}
}

func TestParseHandshakeLine_V5IsSupported(t *testing.T) {
	hs, err := parseHandshakeLine("1|5|unix|/tmp/plugin123.sock|grpc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hs.appProtocolVersion != 5 {
		t.Fatalf("got appProtocolVersion=%d, want 5", hs.appProtocolVersion)
	}
}

func TestParseHandshakeLine_V6IsSupported(t *testing.T) {
	hs, err := parseHandshakeLine("1|6|unix|/tmp/plugin123.sock|grpc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hs.appProtocolVersion != 6 {
		t.Fatalf("got appProtocolVersion=%d, want 6", hs.appProtocolVersion)
	}
}

func TestParseHandshakeLine_TCP(t *testing.T) {
	hs, err := parseHandshakeLine("1|6|tcp|127.0.0.1:1234|grpc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hs.network != "tcp" || hs.addr != "127.0.0.1:1234" {
		t.Fatalf("got %+v", hs)
	}
}

func TestParseHandshakeLine_TooFewFields(t *testing.T) {
	_, err := parseHandshakeLine("1|6|unix")
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("got %v, want ErrMalformedHandshake", err)
	}
}

func TestParseHandshakeLine_WrongCoreVersion(t *testing.T) {
	_, err := parseHandshakeLine("2|6|unix|/tmp/x|grpc")
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("got %v, want ErrProtocolMismatch", err)
	}
}

func TestParseHandshakeLine_UnsupportedAppVersion(t *testing.T) {
	cases := []string{"4", "7", "99"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			_, err := parseHandshakeLine("1|" + v + "|unix|/tmp/x|grpc")
			if !errors.Is(err, ErrProtocolMismatch) {
				t.Fatalf("got %v, want ErrProtocolMismatch", err)
			}
		})
	}
}

func TestParseHandshakeLine_NonNumericAppVersion(t *testing.T) {
	_, err := parseHandshakeLine("1|six|unix|/tmp/x|grpc")
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("got %v, want ErrMalformedHandshake", err)
	}
}

func TestParseHandshakeLine_UnknownNetwork(t *testing.T) {
	_, err := parseHandshakeLine("1|6|carrier-pigeon|/tmp/x|grpc")
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("got %v, want ErrMalformedHandshake", err)
	}
}

func TestParseHandshakeLine_NonGRPCWireProtocol(t *testing.T) {
	_, err := parseHandshakeLine("1|6|unix|/tmp/x|netrpc")
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("got %v, want ErrProtocolMismatch", err)
	}
}

func TestParseHandshakeLine_EmptyAddr(t *testing.T) {
	_, err := parseHandshakeLine("1|6|unix||grpc")
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("got %v, want ErrMalformedHandshake", err)
	}
}
