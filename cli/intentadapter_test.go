package cli

import (
	"strings"
	"testing"
)

func TestResolveKeyRef_UnsetIsAmbientCredentials(t *testing.T) {
	key, err := resolveKeyRef(KeyRefConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "" {
		t.Errorf("key = %q, want empty (falls through to the SDK's own default chain)", key)
	}
}

func TestResolveKeyRef_ReadsNamedEnvVar(t *testing.T) {
	t.Setenv("UBX_TEST_INTENT_KEY", "sk-ant-real-key")

	key, err := resolveKeyRef(KeyRefConfig{Env: "UBX_TEST_INTENT_KEY"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-ant-real-key" {
		t.Errorf("key = %q, want %q", key, "sk-ant-real-key")
	}
}

func TestResolveKeyRef_NamedButUnsetIsARealError(t *testing.T) {
	t.Setenv("UBX_TEST_INTENT_KEY_MISSING", "")

	_, err := resolveKeyRef(KeyRefConfig{Env: "UBX_TEST_INTENT_KEY_MISSING"})
	if err == nil {
		t.Fatal("expected an error -- a named but unset/empty key_ref.env is a real misconfiguration, not ambient credentials")
	}
	if !strings.Contains(err.Error(), "UBX_TEST_INTENT_KEY_MISSING") {
		t.Errorf("error = %v, want it to name the missing env var", err)
	}
}

func TestBuildIntentAdapter_DefaultsToClaude(t *testing.T) {
	a, err := buildIntentAdapter(&Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", a.Name(), "claude")
	}
}

func TestBuildIntentAdapter_UnsupportedAdapterNamesItself(t *testing.T) {
	_, err := buildIntentAdapter(&Config{Intent: IntentConfig{Adapter: "openai"}})
	if err == nil {
		t.Fatal("expected an error for an unsupported adapter")
	}
	if !strings.Contains(err.Error(), `"openai"`) {
		t.Errorf("error = %v, want it to name the unsupported adapter", err)
	}
}

func TestBuildIntentAdapter_PropagatesKeyRefError(t *testing.T) {
	_, err := buildIntentAdapter(&Config{Intent: IntentConfig{
		KeyRef: KeyRefConfig{Env: "UBX_TEST_DEFINITELY_UNSET_VAR"},
	}})
	if err == nil {
		t.Fatal("expected an error from an unresolvable key_ref")
	}
}
