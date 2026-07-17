// Package cli's mcp.go is UBI-25's `ubx mcp` verb: serves the Model
// Context Protocol over stdio, so an AI assistant can call ubx's
// read-only tools directly in conversation instead of a human already
// knowing the CLI's own argument shapes (see docs/architecture.md --
// "MCP server").
//
// One binary, not a second `cmd/ubx-mcp` executable -- `ubx mcp` is a
// cobra subcommand like every other verb, just one that blocks serving
// requests instead of running once and exiting.
//
// Three tools, each a thin wrapper over the exact same computeWhyJSON/
// computeStatusJSON/computeScanJSON functions the CLI's own --json code
// paths call (cli/mcp_why.go, cli/mcp_status.go, cli/mcp_scan.go) --
// never a parallel API, never a different JSON shape than UBI-20's
// format:1 contract already defines.
//
// Boundary by omission, stated here and in --help, not left to be
// inferred: `ubx accept`/`ship`/`writeback`/`revert-plan` (and
// `scan --surface-as`, which opens a real GitHub issue/PR) are
// deliberately NOT exposed as tools. Accepting a proposal is a recorded
// human (or PR-merge-derived) decision -- never something an assistant
// does on a human's behalf mid-conversation.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve ubx's read-only tools (why/status/scan) over MCP, for an AI assistant to call directly",
		Long: `Serves the Model Context Protocol (MCP) over stdio, so an AI assistant (Claude Code, Claude
Desktop, any MCP client) can ask ubx questions directly in conversation -- "who changed this bucket and
when" -- without the human already needing to know ubx's own command shapes.

Three read-only tools, each a thin wrapper over the exact JSON payload the equivalent --json CLI command
already produces:

  ubx_why     resource address or proposal ID -> its recorded history / one decision's full receipt
  ubx_status  optional stack filter, optional live-state drift check -> fleet report
  ubx_scan    single resource -> new/drifted/unchanged classification + the generated proposal, inline

Boundary by omission: ubx accept/ship/writeback/revert-plan (and scan --surface-as, which opens a real
GitHub issue/PR) are NOT exposed here, deliberately. Accepting a proposal is a recorded human (or
PR-merge-derived) decision -- never something an assistant does on a human's behalf mid-conversation.
This server surfaces information; it never signs anything, never writes to a live resource, and never
appends to the ledger.

Configuration discovers .ubx/config from this process's own working directory, exactly like every other
ubx command -- point your MCP client's "cwd" at a real ledger checkout to get the same defaults a human
sitting there would (see ubx config).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newMCPServer().Run(cmd.Context(), &mcp.StdioTransport{}); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("mcp: %w", err)}
			}
			return nil
		},
	}
	return cmd
}

// newMCPServer builds the ubx MCP server with all three tools
// registered -- factored out from newMCPCmd's RunE so tests can connect
// to it directly over an in-memory transport (mcp.NewInMemoryTransports),
// never a real stdio subprocess.
func newMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ubx", Version: versionString()}, nil)
	registerWhyTool(server)
	registerStatusTool(server)
	registerScanTool(server)
	return server
}

// orDot defaults an empty MCP input field to "." -- there's no cobra
// flag-default machinery here (an MCP tool input field has no "changed"
// bit the way a flag does; empty just means "not supplied"), so the
// default is applied directly.
func orDot(v string) string {
	if v == "" {
		return "."
	}
	return v
}

// providerConfigJSON marshals a .ubx/config [provider_config] table
// (map[string]any, the same shape applyProviderConfigDefault marshals
// for the CLI) to the JSON string --provider-config/provider_config
// already takes.
func providerConfigJSON(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal provider_config: %w", err)
	}
	return string(b), nil
}

// Every tool handler below returns `any` as its output type, not the
// concrete *whyJSON/*statusJSON/*scanJSON type -- confirmed necessary,
// not a stylistic choice: mcp.AddTool auto-generates and validates an
// output JSON Schema from the Out type via reflection, and every one of
// these payloads embeds *core.Proposal, which uses json.RawMessage
// extensively (CostDelta.MonthlyUSD, Modification.Before/After,
// ResolutionInput.Lookup, ...) for canonical-JSON encoding
// (docs/schema.md's hashing rules). json.RawMessage is a []byte under
// the hood, which the schema generator infers as a JSON "array" -- but
// the real value at runtime is arbitrary JSON (an object, in the lookup
// case), which then fails the generator's own output validation with a
// real, reproducible error ("type: ...has type \"object\", want one of
// \"null, array\""), caught by actually calling the tool end-to-end
// (`session.CallTool`) during this session's own testing, not assumed
// safe from the type signature alone. `Out any` (per AddTool's own doc
// comment: "if the output type is 'any', no output schema is generated")
// skips that broken inference entirely; the payload is still returned
// as structured JSON content exactly as before, just unvalidated against
// a schema that couldn't correctly describe canonical-JSON's own
// "anything, by design" fields in the first place.

// --- ubx_why ---

type whyToolInput struct {
	Query            string `json:"query" jsonschema:"a resource address (<stack>.<type>.<name>) or a 64-character-hex proposal ID. A resource address returns its FULL history (every adoption and drift, newest first); a proposal ID returns one specific decision in detail, including attribution (who/when/from where) and every changed attribute."`
	LedgerDir        string `json:"ledger_dir,omitempty" jsonschema:"root directory containing ledger/ and .ubx/ (default: the server's own current directory)"`
	VerifyAcceptance bool   `json:"verify_acceptance,omitempty" jsonschema:"only meaningful with a proposal ID: re-derive a pr_merge acceptance against current git history and (if github_repo is set) the GitHub API, and report whether it still checks out"`
	RepoDir          string `json:"repo_dir,omitempty" jsonschema:"local git working tree to verify verify_acceptance's merge commit against (default: the server's own current directory)"`
	GithubRepo       string `json:"github_repo,omitempty" jsonschema:"owner/name of the GitHub repository, for verify_acceptance's reviewer re-check (the git-history re-check runs without it)"`
}

func registerWhyTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ubx_why",
		Description: "Explain who changed an infrastructure resource, when, and why -- or replay a resource's " +
			"entire recorded history. ubx's ledger records every adoption and every detected drift as a proposal, " +
			"each with a best-effort attribution (the IAM/GCP/Kubernetes identity, event, and timestamp responsible, " +
			"when it could be determined) and the exact attributes that changed. Reach for this whenever asked " +
			"\"who changed X\", \"when did Y change\", or \"why does Z look like this\" for infrastructure ubx " +
			"already tracks. A sensitive attribute (a password, a key) is never returned as real material -- only " +
			"a salted fingerprint (see the \"$redacted\" shape), so you can report that it changed without ever " +
			"seeing what it changed to.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in whyToolInput) (*mcp.CallToolResult, any, error) {
		payload, err := computeWhyJSON(ctx, orDot(in.LedgerDir), in.Query, in.VerifyAcceptance, orDot(in.RepoDir), in.GithubRepo)
		if err != nil {
			return nil, nil, err
		}
		return nil, payload, nil
	})
}

// --- ubx_status ---

type statusToolInput struct {
	Stack           string `json:"stack,omitempty" jsonschema:"restrict the report to one stack (default: every stack the ledger holds)"`
	Drift           bool   `json:"drift,omitempty" jsonschema:"also read each resource's current live state and classify it clean/drifted/unreadable (requires provider identity below); false (the default) is ledger-only -- fast, no credentials needed, reports what the ledger last recorded without checking whether it's still true"`
	LedgerDir       string `json:"ledger_dir,omitempty" jsonschema:"root directory containing ledger/ and .ubx/ (default: the server's own current directory)"`
	ProviderPath    string `json:"provider_path,omitempty" jsonschema:"path to a provider binary already on disk (mutually exclusive with source; only used when drift is true)"`
	Source          string `json:"source,omitempty" jsonschema:"provider registry source, e.g. hashicorp/aws or hashicorp/kubernetes (mutually exclusive with provider_path; requires provider_version; only used when drift is true)"`
	ProviderVersion string `json:"provider_version,omitempty" jsonschema:"explicit provider version to acquire, e.g. 6.54.0 (required with source)"`
	ProviderConfig  string `json:"provider_config,omitempty" jsonschema:"JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"} (only used when drift is true)"`
}

func registerStatusTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ubx_status",
		Description: "Report every infrastructure resource the ledger knows about -- its address, kind, and when " +
			"it was accepted -- optionally checked against live state. Reach for this to answer \"what does ubx " +
			"track\", \"what's the current fleet\", or (with drift=true and provider identity supplied) \"has " +
			"anything drifted since we last recorded it\". Ledger-only mode (the default) is instant and needs no " +
			"credentials; drift mode makes a real read against live infrastructure and needs the same provider " +
			"identity ubx_scan does. A resource this tool can't read live state for is reported \"unreadable\" " +
			"with a reason, not silently dropped -- the walk always covers every resource.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusToolInput) (*mcp.CallToolResult, any, error) {
		cfg, err := LoadConfig(os.Stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_status: %w", err)
		}
		providerPath, source, providerVersion := in.ProviderPath, in.Source, in.ProviderVersion
		if providerPath == "" && source == "" {
			switch {
			case cfg.Provider.Path != "":
				providerPath = cfg.Provider.Path
			case cfg.Provider.Source != "":
				source = cfg.Provider.Source
				if providerVersion == "" {
					providerVersion = cfg.Provider.Version
				}
			}
		}
		providerConfig := in.ProviderConfig
		if providerConfig == "" && len(cfg.ProviderConfig) > 0 {
			b, err := providerConfigJSON(cfg.ProviderConfig)
			if err != nil {
				return nil, nil, fmt.Errorf("ubx_status: %w", err)
			}
			providerConfig = b
		}

		payload, err := computeStatusJSON(ctx, statusJSONOptions{
			LedgerDir:       orDot(in.LedgerDir),
			Stack:           in.Stack,
			Drift:           in.Drift,
			ProviderPath:    providerPath,
			Source:          source,
			ProviderVersion: providerVersion,
			ProviderConfig:  providerConfig,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, payload, nil
	})
}

// --- ubx_scan ---

type scanToolInput struct {
	Stack           string `json:"stack" jsonschema:"stack name the resource belongs to"`
	Type            string `json:"type" jsonschema:"resource type, e.g. aws_s3_bucket, google_pubsub_topic, kubernetes_secret_v1, helm_release"`
	Name            string `json:"name" jsonschema:"resource name within the stack"`
	Lookup          string `json:"lookup" jsonschema:"JSON object identifying the resource to the provider, e.g. {\"id\":\"my-bucket\"} -- see the ubx docs' lookup conventions page for per-type shapes"`
	ProviderPath    string `json:"provider_path,omitempty" jsonschema:"path to a provider binary already on disk (mutually exclusive with source)"`
	Source          string `json:"source,omitempty" jsonschema:"provider registry source, e.g. hashicorp/aws (mutually exclusive with provider_path; requires provider_version)"`
	ProviderVersion string `json:"provider_version,omitempty" jsonschema:"explicit provider version to acquire, e.g. 6.54.0 (required with source; no \"latest\" resolution)"`
	ProviderConfig  string `json:"provider_config,omitempty" jsonschema:"JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"}"`
	LedgerDir       string `json:"ledger_dir,omitempty" jsonschema:"root directory containing ledger/ and .ubx/ (default: the server's own current directory)"`
	Out             string `json:"out,omitempty" jsonschema:"optionally also write the generated proposal to this path on disk, exactly like ubx scan --out. The proposal is always returned inline regardless -- this never replaces the response, only additionally persists it"`
	NoAttribution   bool   `json:"no_attribution,omitempty" jsonschema:"skip best-effort attribution (CloudTrail/GCP Cloud Audit Logs/EKS audit logs) for a drift finding"`
	Propose         string `json:"propose,omitempty" jsonschema:"on drift, which resolution(s) to generate: adopt (record the new reality, default), revert (propose restoring the ledger's prior value), or both. No effect on a never-before-seen resource, which always generates an adoption"`
}

func registerScanTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ubx_scan",
		Description: "Read one resource's LIVE state from its real provider (cloud API or cluster) and compare it " +
			"against what the ledger last recorded, classifying it new/drifted/unchanged. This is the one tool that " +
			"makes a real network read against live infrastructure -- it is still entirely read-only: it never " +
			"applies, accepts, or writes anything to the ledger. On a new or drifted resource it returns the " +
			"generated proposal inline (the exact JSON ubx accept would later sign, if a human decides to) -- " +
			"reach for this to answer \"has this resource changed\", \"what would ubx propose if I scanned this " +
			"right now\", or to onboard a resource ubx has never seen. A Sensitive-flagged attribute (a password, " +
			"a key, a rendered Helm manifest that might carry one) is never returned as real material, only a " +
			"salted fingerprint.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in scanToolInput) (*mcp.CallToolResult, any, error) {
		cfg, err := LoadConfig(os.Stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_scan: %w", err)
		}
		stack := in.Stack
		if stack == "" {
			stack = cfg.Stack
		}
		providerPath, source, providerVersion := in.ProviderPath, in.Source, in.ProviderVersion
		if providerPath == "" && source == "" {
			switch {
			case cfg.Provider.Path != "":
				providerPath = cfg.Provider.Path
			case cfg.Provider.Source != "":
				source = cfg.Provider.Source
				if providerVersion == "" {
					providerVersion = cfg.Provider.Version
				}
			}
		}
		providerConfig := in.ProviderConfig
		if providerConfig == "" && len(cfg.ProviderConfig) > 0 {
			b, err := providerConfigJSON(cfg.ProviderConfig)
			if err != nil {
				return nil, nil, fmt.Errorf("ubx_scan: %w", err)
			}
			providerConfig = b
		}
		if providerConfig == "" {
			providerConfig = "{}"
		}
		lookup := in.Lookup
		if lookup == "" {
			lookup = "{}"
		}
		if stack == "" || in.Type == "" || in.Name == "" {
			return nil, nil, fmt.Errorf("ubx_scan: stack, type, and name are all required")
		}

		payload, err := computeScanJSON(ctx, scanJSONOptions{
			Stack:           stack,
			ResourceType:    in.Type,
			ResourceName:    in.Name,
			Lookup:          lookup,
			ProviderPath:    providerPath,
			Source:          source,
			ProviderVersion: providerVersion,
			ProviderConfig:  providerConfig,
			LedgerDir:       orDot(in.LedgerDir),
			Out:             in.Out,
			NoAttribution:   in.NoAttribution,
			Propose:         in.Propose,
			K8sAudit:        cfg.K8sAudit,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, payload, nil
	})
}
