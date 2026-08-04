package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAcceptShip_NumericConfig_SurvivesEncodeUnchanged is UBI-123's
// own required hermetic regression test, once the ticket's own real-AWS
// repro (a resolved aws_sqs_queue's message_retention_seconds shipping
// with a different value than the resolved proposal recorded) turned out
// to have a different, non-encode root cause (see this test's own package
// doc note below) -- kept anyway, permanently, as the "correct at rest,
// corrupted in flight" class guard the ticket asked for: a genuinely
// numeric-typed config attribute (cty.Number, not cty.String -- the
// standing fake_widget fixture has no numeric attribute at all, so this
// exercises provider/ctyvalue.go's own cty.Number branch for the first
// time in this repo's own test suite) round-trips through the REAL
// executor create path -- shipCreate's own config-decode/substituteComputed/
// re-marshal, then a REAL cty-msgpack-encoded ApplyResourceChange call
// against a REAL fakeprovider subprocess (conformance-v6 mode, not a
// mock) -- completely unchanged, both immediately before the RPC
// (UBX_SHIP_DEBUG_TRACE_CONFIG, ship.go's own traceShipCreateConfig) and
// in the real provider's own echoed-back provider_result.
//
// UBI-123's actual finding, for the record: the resolved JSON on disk,
// the plannedState traced immediately before ApplyResourceChange, and the
// real (fake) provider's own echoed provider_result all agree: 2592000
// survives this entire path byte-for-byte, every time. The real AWS
// InvalidAttributeValue error the founder hit three times is SQS's own
// real, documented MessageRetentionPeriod bound (60-1,209,600 seconds,
// i.e. 60 seconds to 14 days -- confirmed against AWS's own SetQueueAttributes
// API reference) rejecting a value the blueprint's own retention_days=30
// input converts to (2,592,000 seconds = 30 days) -- not a wire-encoding
// bug at all. See docs/blueprint.md's own UBI-123 log entry for the full
// account of how this was distinguished from the encode-corruption
// hypothesis, not just asserted.
func TestResolveAcceptShip_NumericConfig_SurvivesEncodeUnchanged(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=conformance-v6",
		"FAKEPROVIDER_RESOURCE_TYPE=aws_sqs_queue",
		"FAKEPROVIDER_ATTRS=id,name,message_retention_seconds",
		"FAKEPROVIDER_ATTR_TYPES=message_retention_seconds:number",
		"UBX_SHIP_DEBUG_TRACE_CONFIG=1",
	}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "numeric config encode-path repro (UBI-123)"},
		"resources": []map[string]interface{}{
			{
				"type": "aws_sqs_queue",
				"name": "pipeline-events",
				"op":   "create",
				"config": map[string]interface{}{
					"name":                      "pipeline-events",
					"message_retention_seconds": 2592000,
				},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, resolveOut)
	}

	resolvedRaw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolvedRaw), `"message_retention_seconds": 2592000`) {
		t.Fatalf("resolved proposal itself doesn't carry 2592000 as a bare JSON number -- test setup is wrong, not the bug under test: %s", resolvedRaw)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	stderrCapture := captureStderr(t, func() {
		shipOut, err := runUbx(t, env, "ship", changeID,
			"--provider", fakeProviderBinary,
			"--ledger-dir", ledgerDir,
		)
		if err != nil {
			t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
		}
		if !strings.Contains(shipOut, "outcome: shipped") {
			t.Fatalf("expected outcome: shipped, got: %s", shipOut)
		}
	})

	// 1. The trace point immediately before ApplyResourceChange (ship.go's
	// own traceShipCreateConfig) -- proves what shipCreate is ACTUALLY
	// about to send, not what an earlier stage claims.
	if !strings.Contains(stderrCapture, `"message_retention_seconds":2592000`) {
		t.Fatalf("ship's own pre-ApplyResourceChange trace doesn't show 2592000 in plannedState:\n%s", stderrCapture)
	}

	// 2. The real fakeprovider subprocess's own echoed-back provider_result
	// -- proves the value actually survived a REAL cty-msgpack encode, a
	// REAL gRPC call to a REAL separate process, and a REAL decode back --
	// not just what ubx's own client claims to have sent.
	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "2592000") {
		t.Fatalf("ubx why doesn't show the real applied message_retention_seconds=2592000 -- the value was corrupted somewhere in the real encode/wire round trip:\n%s", whyOut)
	}
}

// captureStderr redirects the real process's os.Stderr for the duration of
// fn, returning everything written to it -- needed here specifically
// because runUbx executes cobra's command tree in-process (root.SetOut/
// SetErr only capture cobra's own writer, never a raw os.Stderr write deep
// inside core/executor, which is exactly what ship.go's own
// traceShipCreateConfig does, deliberately, matching this codebase's
// existing UBX_SHIP_DEBUG_DELAY_* convention of writing straight to the
// real process rather than threading a writer through Ship's own already-
// large parameter list for a test-only diagnostic).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		chunk := make([]byte, 4096)
		for {
			n, err := r.Read(chunk)
			if n > 0 {
				buf = append(buf, chunk[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()

	fn()

	os.Stderr = old
	_ = w.Close()
	captured := <-done
	_ = r.Close()
	return captured
}
