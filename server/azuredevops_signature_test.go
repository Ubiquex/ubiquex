package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func azureDevOpsRequest(t *testing.T, headerName, headerValue string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/webhook/azuredevops", strings.NewReader(`{"eventType":"git.pullrequest.created"}`))
	if headerName != "" {
		r.Header.Set(headerName, headerValue)
	}
	return r
}

func TestValidAzureDevOpsSignature_Accepted(t *testing.T) {
	r := azureDevOpsRequest(t, "X-Ubx-Webhook-Secret", "s3cr3t")
	if !validAzureDevOpsSignature("X-Ubx-Webhook-Secret", "s3cr3t", r) {
		t.Error("expected a matching shared-secret header to be accepted")
	}
}

func TestValidAzureDevOpsSignature_WrongSecretRejected(t *testing.T) {
	r := azureDevOpsRequest(t, "X-Ubx-Webhook-Secret", "wrong-value")
	if validAzureDevOpsSignature("X-Ubx-Webhook-Secret", "s3cr3t", r) {
		t.Error("expected a mismatched header value to be rejected")
	}
}

func TestValidAzureDevOpsSignature_MissingHeaderRejected(t *testing.T) {
	r := azureDevOpsRequest(t, "", "")
	if validAzureDevOpsSignature("X-Ubx-Webhook-Secret", "s3cr3t", r) {
		t.Error("expected a missing header to be rejected")
	}
}

func TestValidAzureDevOpsSignature_WrongHeaderNameRejected(t *testing.T) {
	// The real secret value is present, but under a different header
	// name than configured -- must not be found under the wrong key.
	r := azureDevOpsRequest(t, "X-Some-Other-Header", "s3cr3t")
	if validAzureDevOpsSignature("X-Ubx-Webhook-Secret", "s3cr3t", r) {
		t.Error("expected the configured header name to be required, not just the value present somewhere")
	}
}

func TestValidAzureDevOpsSignature_UnconfiguredSecretAlwaysRejected(t *testing.T) {
	// A real, deliberate refusal: an empty configured secret must never
	// accidentally accept an empty (or any) header value -- the same
	// "no confused fail-open" discipline every other platform's own
	// signature check in this package already has.
	r := azureDevOpsRequest(t, "X-Ubx-Webhook-Secret", "")
	if validAzureDevOpsSignature("X-Ubx-Webhook-Secret", "", r) {
		t.Error("expected an unconfigured (empty) secret to always be rejected, never treated as a valid empty-string match")
	}
}
