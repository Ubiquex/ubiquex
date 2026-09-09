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
// Three read/query tools (cli/mcp_why.go, cli/mcp_status.go,
// cli/mcp_scan.go), each producing the exact same JSON shape the CLI's
// own --json output does -- never a parallel API, never a different
// shape than UBI-20's format:1 contract already defines. Not literally
// shared code with the CLI's own --json path, though: `cli/why.go`/
// `cli/status.go`/`cli/scan.go` each grew their own [ledger]-aware
// lookup (openLedgerForStack, UBI-32) independently of these
// compute*JSON functions, which needed the identical fix applied here
// separately (a real, if narrow, divergence this session found and
// closed -- see each compute*JSON function's own doc comment).
//
// Five more (cli/mcp_blueprint.go, UBI-223): draft_ubxfile,
// validate_ubxfile, build_blueprint, list_blueprints, describe_blueprint
// -- blueprint authoring, independent tools rather than a fixed
// pipeline, matching that ticket's own name for itself.
// push_blueprint is deliberately excluded (see cli/mcp_blueprint.go's
// own package doc comment for the full account) -- publishing is the one
// irreversible, externally-consumed step, and gets the identical
// boundary-by-omission treatment as accept/ship below rather than a
// runtime gate.
//
// Boundary by omission, stated here and in --help, not left to be
// inferred: `ubx accept`/`ship`/`writeback`/`revert-plan` (and
// `scan --surface-as`, which opens a real GitHub issue/PR, and
// `blueprint push`) are deliberately NOT exposed as tools. Accepting a
// proposal (or publishing a blueprint) is a recorded human (or
// PR-merge-derived) decision -- never something an assistant does on a
// human's behalf mid-conversation.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve ubx's read-only and blueprint-authoring tools over MCP, for an AI assistant to call directly",
		Long: `Serves the Model Context Protocol (MCP) over stdio, so an AI assistant (Claude Code, Claude
Desktop, any MCP client) can ask ubx questions directly in conversation -- "who changed this bucket and
when" -- without the human already needing to know ubx's own command shapes.

Four read-only tools, each a thin wrapper over the exact JSON payload the equivalent --json CLI command
already produces:

  ubx_why      resource address or proposal ID -> its recorded history / one decision's full receipt
  ubx_status   optional stack filter, optional live-state drift check -> fleet report, as it stands now
  ubx_history  a stack's whole proposal chain, newest first -> what has happened, including what is now gone
  ubx_scan     single resource -> new/drifted/unchanged classification + the generated proposal, inline

Five blueprint-authoring tools (UBI-223), independent, callable in any order:

  draft_ubxfile      pieces you've already decided -> an assembled Ubxfile (mechanical, never AI drafting)
  validate_ubxfile   an Ubxfile (a dir, or inline content) -> valid/invalid, cheap, no codegen
  build_blueprint    an Ubxfile -> compiled Go/TS/Python source, returned inline, nothing written by default
  list_blueprints    a directory tree -> every real Ubxfile found in it
  describe_blueprint one already-known ref (git/oci/tarball/local) -> its name, params, resources

Boundary by omission: ubx accept/ship/writeback/revert-plan (and scan --surface-as, which opens a real
GitHub issue/PR, and blueprint push) are NOT exposed here, deliberately. Accepting a proposal (or
publishing a blueprint) is a recorded human (or PR-merge-derived) decision -- never something an
assistant does on a human's behalf mid-conversation. This server surfaces information and, for blueprint
authoring, returns generated content; it never signs anything, never writes to a live resource or a real
repository, and never appends to the ledger.

Every tool taking ledger_dir refuses a path that isn't a ubx stack root (a directory holding .ubx/)
rather than reporting it as a ledger that happens to be empty, and expands a leading ~/ against this
process's own home directory -- there is no shell in front of an MCP call to do either, and an empty
result for a mistyped path is indistinguishable from a stack that genuinely tracks nothing.

Configuration comes from the stack root each call names: a tool's own ledger_dir supplies its .ubx/config
(cascading upward from there, exactly like every other ubx command does from cwd), so a call against one
stack never silently inherits another stack's provider identity or ledger store. With ledger_dir omitted
that root is this process's own working directory, unchanged -- point your MCP client's "cwd" at a real
ledger checkout to get the same defaults a human sitting there would (see ubx config).`,
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
	registerHistoryTool(server)
	registerDraftUbxfileTool(server)
	registerValidateUbxfileTool(server)
	registerBuildBlueprintTool(server)
	registerListBlueprintsTool(server)
	registerDescribeBlueprintTool(server)
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

// expandTilde resolves a leading "~/" (or a bare "~") in a
// caller-supplied path against the server's own home directory.
//
// ubx expands tilde nowhere else, on purpose: every other path it takes
// arrives through a shell, which has already expanded it, and adding
// expansion to `--ledger-dir` would change long-settled CLI semantics
// for no gain. MCP is the one boundary with no shell in front of it,
// and a model writes "~/stacks/payments" because that is how a human
// writes a path. Left alone, Go reads that as a RELATIVE directory
// literally named "~", so the path silently resolves under the server's
// own cwd, finds nothing, and (before the ubx-root check below existed)
// came back as a successful empty result. Expanding is the only one of
// the three available answers the caller can act on: refusing would
// require the model to supply the server's home directory, which it has
// no way to learn -- there is no tool that reports it, and the server
// may not even run as the user.
//
// "~user/" is refused rather than guessed. Resolving another user's
// home is not portably available here, and a wrong guess produces a
// real-looking absolute path, which is the failure mode this whole
// change exists to remove.
//
// Resolved through userHomeDir, the package var (configcascade.go), not
// a bare os.UserHomeDir call, so tests point it at a scratch directory
// instead of depending on the host's own $HOME.
func expandTilde(p string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return "", fmt.Errorf("%q: a ~user path cannot be resolved here -- pass an absolute path instead", p)
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: %w", p, err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// mcpDir prepares a caller-supplied directory that defaults to the
// server's own cwd: tilde first, then the "." default.
func mcpDir(v string) (string, error) {
	expanded, err := expandTilde(v)
	if err != nil {
		return "", err
	}
	return orDot(expanded), nil
}

// mcpRootDir is mcpDir for a directory that also supplies the config
// cascade's own starting point, which is every ledger_dir now that the
// config comes from the stack root rather than from wherever the server
// process was started.
//
// The default routes through configSearchStartDir, the package var,
// rather than a literal "." -- in production that IS os.Getwd, so the
// behavior is identical, but it keeps one notion of "where this process
// looks by default" instead of two that could drift apart. It also
// keeps this path inside the hermeticity pin the test package's own
// TestMain sets on that var (cli/scan_test.go): a raw os.Getwd default
// would read whatever .ubx/config happens to sit above the directory
// `go test` was invoked from, which is exactly the ambient host state
// that pin exists to rule out.
func mcpRootDir(v string) (string, error) {
	if v != "" {
		return mcpDir(v)
	}
	dir, err := configSearchStartDir()
	if err != nil {
		return "", fmt.Errorf("resolve the server's own working directory: %w", err)
	}
	return dir, nil
}

// resolveLedgerDir prepares ledger_dir for every tool that takes one:
// applies the cwd default, expands a leading tilde, and refuses a path
// that is not a ubx root.
//
// The refusal is the point. core.Open is a pure constructor that stats
// nothing (core/ledger.go), so a ledger opened at a directory that does
// not exist, or that was never initialized, walks an absent tree, finds
// no proposals, and reports total: 0 with no error. A caller could not
// distinguish "this stack tracks nothing" from "you gave me the wrong
// path" -- and an MCP caller, unlike a person at a terminal, cannot see
// the server's cwd to work out which it got. Reported from a real
// Claude Desktop session, where the model hit it and said so.
//
// The discriminator is `.ubx/`, not `ledger/`. Both of ubx's own
// legitimate zero-resource shapes have it and only one has ledger/:
// `ubx init` writes .ubx/config.hcl and no ledger/ at all (ledger/ is
// created lazily on the first accept), while a ledger built by
// `ubx accept --ledger-dir` gets .ubx/ledger.lock, .ubx/salt and
// ledger/ but no config file of its own. Requiring a config file would
// refuse that second shape, which ubx itself creates and this package's
// own fixtures use.
//
// The result is absolute, and that is load-bearing rather than tidiness:
// the config cascade this directory now feeds (loadConfigFromDir, per
// each handler) walks UPWARD from its start directory, and
// filepath.Dir(".") is "." -- so a relative "." start would stop dead at
// the first directory and never see a parent's config, silently
// narrowing the search the default case is supposed to leave untouched.
func resolveLedgerDir(v string) (string, error) {
	dir, err := mcpRootDir(v)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", dir, err)
	}
	dir = abs
	info, statErr := os.Stat(filepath.Join(dir, ".ubx"))
	if statErr == nil && info.IsDir() {
		return dir, nil
	}
	shown := dir
	if v == "" {
		return "", fmt.Errorf("no ledger_dir was given and the server's own current directory (%s) is not a ubx root: it holds no .ubx/ directory. Pass ledger_dir naming the stack's root directory", shown)
	}
	return "", fmt.Errorf("ledger_dir %s is not a ubx root: it holds no .ubx/ directory. A ubx root is a directory `ubx init` (or `ubx accept --ledger-dir`) has written .ubx/ into; check the path, or initialize that directory first", shown)
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
	Stack            string `json:"stack,omitempty" jsonschema:"which stack's ledger to open, for a bare proposal-id query -- required only when .ubx/config's [ledger] store is a remote store (a resource-address query already names its own stack); unused for the default git store"`
	LedgerDir        string `json:"ledger_dir,omitempty" jsonschema:"root directory of a ubx stack: the directory holding its .ubx/ (and its ledger/, once anything has been accepted). This supplies BOTH the ledger and the .ubx/config the call runs under, so naming one stack never picks up another's provider identity or ledger store. A leading ~/ is expanded against the server's home directory; a path with no .ubx/ in it is refused rather than reported as an empty ledger. Default: the server's own current directory"`
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
		ledgerDir, err := resolveLedgerDir(in.LedgerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_why: %w", err)
		}
		repoDir, err := mcpDir(in.RepoDir)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_why: repo_dir: %w", err)
		}
		cfg, err := loadConfigFromDir(ledgerDir, os.Stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_why: %w", err)
		}
		stack := in.Stack
		if stack == "" {
			stack = cfg.Stack
		}
		payload, err := computeWhyJSON(ctx, cfg, ledgerDir, stack, in.Query, in.VerifyAcceptance, repoDir, in.GithubRepo)
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
	LedgerDir       string `json:"ledger_dir,omitempty" jsonschema:"root directory of a ubx stack: the directory holding its .ubx/ (and its ledger/, once anything has been accepted). This supplies BOTH the ledger and the .ubx/config the call runs under, so naming one stack never picks up another's provider identity or ledger store. A leading ~/ is expanded against the server's home directory; a path with no .ubx/ in it is refused rather than reported as an empty ledger. Default: the server's own current directory"`
	ProviderPath    string `json:"provider_path,omitempty" jsonschema:"path to a provider binary already on disk (mutually exclusive with source; only used when drift is true)"`
	Source          string `json:"source,omitempty" jsonschema:"provider registry source, e.g. hashicorp/aws or hashicorp/kubernetes (mutually exclusive with provider_path; requires provider_version; only used when drift is true)"`
	ProviderVersion string `json:"provider_version,omitempty" jsonschema:"explicit provider version to acquire, e.g. 6.54.0 (required with source)"`
	ProviderConfig  string `json:"provider_config,omitempty" jsonschema:"JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"} (only used when drift is true)"`
}

func registerStatusTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ubx_status",
		Description: "Report what a stack tracks RIGHT NOW: every resource currently in its ledger's folded " +
			"state, with address, kind, and when it was accepted, optionally checked against live state. Reach " +
			"for this to answer \"what does ubx track\", \"what's the current fleet\", or (with drift=true and " +
			"provider identity supplied) \"has anything drifted since we last recorded it\". This is the CURRENT " +
			"state, not the history: a resource that was created and later destroyed is gone from it while its " +
			"proposals remain in the ledger permanently, so zero resources does NOT mean the ledger is empty. " +
			"The summary's proposals_total says how many proposals the chain holds; if that is above zero and " +
			"resources is empty, call ubx_history to see what happened. Ledger-only mode (the default) is " +
			"instant and needs no credentials; drift mode makes a real read against live infrastructure and " +
			"needs the same provider identity ubx_scan does. A resource this tool can't read live state for is " +
			"reported \"unreadable\" with a reason, not silently dropped -- the walk always covers every " +
			"resource.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusToolInput) (*mcp.CallToolResult, any, error) {
		ledgerDir, err := resolveLedgerDir(in.LedgerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_status: %w", err)
		}
		cfg, err := loadConfigFromDir(ledgerDir, os.Stderr)
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
			Config:          cfg,
			LedgerDir:       ledgerDir,
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

// --- ubx_history ---

type historyToolInput struct {
	Stack     string `json:"stack,omitempty" jsonschema:"which stack's history to list -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store"`
	LedgerDir string `json:"ledger_dir,omitempty" jsonschema:"root directory of a ubx stack: the directory holding its .ubx/ (and its ledger/, once anything has been accepted). This supplies BOTH the ledger and the .ubx/config the call runs under, so naming one stack never picks up another's provider identity or ledger store. A leading ~/ is expanded against the server's home directory; a path with no .ubx/ in it is refused rather than reported as an empty ledger. Default: the server's own current directory"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many proposals to return, newest first (default 50, maximum 500). The response always reports total (how many the chain actually holds) and truncated, so a shortened list is never mistaken for a complete history"`
}

func registerHistoryTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ubx_history",
		Description: "List what has actually HAPPENED in a stack: every proposal in its ledger, newest first, " +
			"with each one's kind, summary, blast radius, who accepted it and when. Reach for this to answer " +
			"\"what has happened here\", \"what has been done to this stack\", or any question about the past " +
			"rather than the present. This is the tool to use when ubx_status returns no resources but you have " +
			"not established that the ledger is empty: status reports the CURRENT state, and a resource that was " +
			"created and later destroyed leaves nothing in it while its proposals remain in the history " +
			"permanently. Returns proposal IDs, each of which ubx_why takes directly for the full detail of one " +
			"decision. Ledger-only and instant: no provider, no credentials, no network.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in historyToolInput) (*mcp.CallToolResult, any, error) {
		ledgerDir, err := resolveLedgerDir(in.LedgerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_history: %w", err)
		}
		cfg, err := loadConfigFromDir(ledgerDir, os.Stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_history: %w", err)
		}
		stack := in.Stack
		if stack == "" {
			stack = cfg.Stack
		}
		payload, err := computeHistoryJSON(ctx, historyJSONOptions{
			Config:    cfg,
			LedgerDir: ledgerDir,
			Stack:     stack,
			Limit:     historyToolLimit(in.Limit),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_history: %w", err)
		}
		return nil, payload, nil
	})
}

// historyToolLimit applies ubx_history's own default and ceiling.
//
// A real default rather than "everything": a long-lived ledger's whole
// chain dumped into a model's context is an unbounded cost, and the
// answer to "what has happened here" is almost always in the recent
// end. A ceiling as well as a default, because a caller asking for
// 100000 is not making an informed choice about context budget, and the
// payload's own total/truncated fields mean a capped answer still says
// how much history it did not return. Zero or negative means unset, not
// unlimited: unlimited is not on the menu here at all.
func historyToolLimit(requested int) int {
	const (
		defaultLimit = 50
		maxLimit     = 500
	)
	switch {
	case requested <= 0:
		return defaultLimit
	case requested > maxLimit:
		return maxLimit
	default:
		return requested
	}
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
	LedgerDir       string `json:"ledger_dir,omitempty" jsonschema:"root directory of a ubx stack: the directory holding its .ubx/ (and its ledger/, once anything has been accepted). This supplies BOTH the ledger and the .ubx/config the call runs under, so naming one stack never picks up another's provider identity or ledger store. A leading ~/ is expanded against the server's home directory; a path with no .ubx/ in it is refused rather than reported as an empty ledger. Default: the server's own current directory"`
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
			"ships, accepts, or writes anything to the ledger. On a new or drifted resource it returns the " +
			"generated proposal inline (the exact JSON ubx accept would later sign, if a human decides to) -- " +
			"reach for this to answer \"has this resource changed\", \"what would ubx propose if I scanned this " +
			"right now\", or to onboard a resource ubx has never seen. A Sensitive-flagged attribute (a password, " +
			"a key, a rendered Helm manifest that might carry one) is never returned as real material, only a " +
			"salted fingerprint.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in scanToolInput) (*mcp.CallToolResult, any, error) {
		ledgerDir, err := resolveLedgerDir(in.LedgerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_scan: %w", err)
		}
		out, err := expandTilde(in.Out)
		if err != nil {
			return nil, nil, fmt.Errorf("ubx_scan: out: %w", err)
		}
		cfg, err := loadConfigFromDir(ledgerDir, os.Stderr)
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
			Config:          cfg,
			Stack:           stack,
			ResourceType:    in.Type,
			ResourceName:    in.Name,
			Lookup:          lookup,
			ProviderPath:    providerPath,
			Source:          source,
			ProviderVersion: providerVersion,
			ProviderConfig:  providerConfig,
			LedgerDir:       ledgerDir,
			Out:             out,
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
