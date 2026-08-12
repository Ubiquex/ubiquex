package cli

import "encoding/json"

// stackConfigContext is UBI-81 v1's own deliberately narrow, read-only
// projection of *Config -- the ONLY data intentprovider.DraftRequest.
// StackConfig ever carries, and therefore the only data the intent-
// drafting adapter's new read_stack_config tool can ever answer with
// (intentprovider/claude/adapter.go). Real scope decision, not an
// oversight: this is NOT "the whole Config" JSON-marshaled. Config.Intent
// (the [intent] table itself -- KeyRef/Auth/Vertex are all credential-
// adjacent, docs/intent-provider.md's own "KeyRef is never material"
// rule) and Config.ProviderConfig/ProviderConfigs/K8sAudit (freeform,
// user-authored tables this project makes no promise are secret-free)
// are both deliberately excluded -- a stack-naming-convention inference
// (the ticket's own "payments-prod" motivating case) never needs either,
// and handing an LLM anything credential-shaped, even indirectly, is
// never an acceptable trade for a modest drafting-quality improvement.
type stackConfigContext struct {
	Stack           string            `json:"stack"`
	ProviderSource  string            `json:"provider_source,omitempty"`
	ProviderVersion string            `json:"provider_version,omitempty"`
	Providers       map[string]string `json:"providers,omitempty"`
	LedgerStore     string            `json:"ledger_store,omitempty"`
	GithubRepo      string            `json:"github_repo,omitempty"`
	TFDir           string            `json:"tf_dir,omitempty"`
}

// buildStackConfigContext computes the read-only JSON blob a draft's own
// DraftRequest.StackConfig carries -- stack is the ACTUAL stack this
// specific draft targets (which may differ from cfg.Stack, e.g. an
// explicit --stack override), never assumed to match cfg.Stack blindly.
// A marshal failure (Config values are always plain strings/maps here,
// so this cannot realistically happen) returns nil rather than aborting
// drafting over what is, at most, a missed drafting-quality improvement,
// never a correctness requirement.
func buildStackConfigContext(cfg *Config, stack string) json.RawMessage {
	c := stackConfigContext{
		Stack:           stack,
		ProviderSource:  cfg.Provider.Source,
		ProviderVersion: cfg.Provider.Version,
		Providers:       cfg.Providers,
		LedgerStore:     cfg.Ledger.Store,
		GithubRepo:      cfg.GithubRepo,
		TFDir:           cfg.TFDir,
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	return raw
}
