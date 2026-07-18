package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestApplyResourceChange_HappyPath_V6V5 exercises a real cty-msgpack round
// trip through ApplyResourceChange over both wire protocols (UBI-26) --
// the fakeprovider fixture's own ApplyResourceChange handler echoes
// planned_state back exactly like ReadResource's echoWidgetState does,
// proving priorState/plannedState/config all encode without error and the
// response decodes back into the same shape.
func TestApplyResourceChange_HappyPath_V6V5(t *testing.T) {
	for _, mode := range []string{"ok-v6", "ok-v5"} {
		t.Run(mode, func(t *testing.T) {
			client, err := launchFake(t, mode)
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			defer client.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			schemas, err := client.Provider.Schema(ctx)
			if err != nil {
				t.Fatalf("Schema: %v", err)
			}
			widget := schemas.Resources["fake_widget"]

			prior := json.RawMessage(`{"id":"computed-id","name":"a-widget","tags":{}}`)
			planned := json.RawMessage(`{"id":"computed-id","name":"a-widget-renamed","tags":{}}`)

			result, err := client.Provider.ApplyResourceChange(ctx, widget, "fake_widget", prior, planned, planned, nil)
			if err != nil {
				t.Fatalf("ApplyResourceChange: %v", err)
			}
			assertWidgetState(t, result, "a-widget-renamed")
		})
	}
}

// TestApplyResourceChange_DiagnosticError_V6V5 confirms a provider's
// ERROR-severity diagnostic surfaces as *DiagnosticError specifically --
// the signal core/executor's terminal classification depends on
// (docs/executor.md's error taxonomy) -- distinguishable from a bare
// transport failure.
func TestApplyResourceChange_DiagnosticError_V6V5(t *testing.T) {
	for _, mode := range []string{"ok-v6", "ok-v5"} {
		t.Run(mode, func(t *testing.T) {
			client, err := launchFake(t, mode, WithEnv("FAKEPROVIDER_APPLY_MODE=diagnostic-error"))
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			defer client.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			schemas, err := client.Provider.Schema(ctx)
			if err != nil {
				t.Fatalf("Schema: %v", err)
			}
			widget := schemas.Resources["fake_widget"]

			planned := json.RawMessage(`{"id":"computed-id","name":"a-widget","tags":{}}`)
			_, err = client.Provider.ApplyResourceChange(ctx, widget, "fake_widget", planned, planned, planned, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			var diag *DiagnosticError
			if !errors.As(err, &diag) {
				t.Fatalf("err = %v (%T), want *DiagnosticError", err, err)
			}
		})
	}
}

// TestApplyResourceChange_Timeout_V6V5 confirms a provider that never
// responds surfaces as an ordinary context-deadline error -- never a
// *DiagnosticError -- so core/executor's caller correctly classifies it
// "retryable" rather than "terminal" (docs/executor.md).
func TestApplyResourceChange_Timeout_V6V5(t *testing.T) {
	for _, mode := range []string{"ok-v6", "ok-v5"} {
		t.Run(mode, func(t *testing.T) {
			client, err := launchFake(t, mode, WithEnv("FAKEPROVIDER_APPLY_MODE=hang"))
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			defer client.Close()

			schemaCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			schemas, err := client.Provider.Schema(schemaCtx)
			if err != nil {
				t.Fatalf("Schema: %v", err)
			}
			widget := schemas.Resources["fake_widget"]

			applyCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			planned := json.RawMessage(`{"id":"computed-id","name":"a-widget","tags":{}}`)
			_, err = client.Provider.ApplyResourceChange(applyCtx, widget, "fake_widget", planned, planned, planned, nil)
			if err == nil {
				t.Fatal("expected a timeout error")
			}
			var diag *DiagnosticError
			if errors.As(err, &diag) {
				t.Fatalf("err = %v, want a plain timeout error (a gRPC DeadlineExceeded status), not *DiagnosticError", err)
			}
		})
	}
}
