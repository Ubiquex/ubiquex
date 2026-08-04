package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/intentprovider/claude"
)

// docsConfigRef is the one place this file names where the full,
// annotated key encyclopedia actually lives (UBI-59: it moved to
// cli/config.mdx -- `ubx init`'s own generated output, and its `--full`
// mode, both point here instead of re-documenting every key inline).
const docsConfigRef = "https://github.com/Ubiquex/ubiquex-docs, cli/config"

// newInitCmd is UBI-19's config bootstrapper -- a new verb, no
// init-shaped command existed before this session
// (docs/architecture.md — Config defaults).
//
// UBI-59 (founder first-user test): the original version of this command
// wrote pure documentation -- every key commented out, four overlapping
// provider shapes shown as equals with no guidance, rare keys getting the
// most prose while the one thing every next step actually needs (a
// provider) was buried. Default output now is a MINIMAL RUNNABLE config:
// a real stack (from --stack, or the directory's own name), and a real
// provider if one was given via flags or a short TTY prompt -- written
// straight into the modern [providers]/[provider_configs] map (docs/
// architecture.md §Multi-provider stacks) never the legacy singular
// [provider]/[provider_config] shape, which a brand-new user has no
// reason to ever see. `--full` restores the old exhaustive, annotated
// encyclopedia (every key, real or commented) for whoever wants the full
// reference inline instead of cli/config.mdx.
//
// --format defaults to "hcl" (UBI-32 Arc A: HCL is canonical -- "what
// `ubx init` writes by default, what docs examples show,"
// docs/architecture.md's own "Config formats" section). This is a real,
// deliberate behavior change from UBI-19's original default of an
// extensionless TOML `.ubx/config`: the legacy name stays fully
// supported for READING (third in configcascade.go's own discovery
// order, forever), but `ubx init` itself now writes the canonical format
// unless told otherwise.
func newInitCmd() *cobra.Command {
	var (
		dir             string
		force           bool
		full            bool
		format          string
		stack           string
		source          string
		providerVersion string
		providerPath    string
		providerConfig  string
		region          string
		githubRepo      string
		tfDir           string
		resetLedger     bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a minimal, runnable .ubx/config.<format> -- real stack + provider if given, a pointer to the full reference for everything else",
		Long: `Write .ubx/config.<format>, the defaults file every ubx command reads (docs/architecture.md — Config defaults
and Config formats). Default output is minimal and runnable: stack (from --stack, or this directory's own name)
and, if given, a provider -- written into the modern [providers]/[provider_configs] map, the same shape a
multi-provider stack uses (never the legacy singular [provider]/[provider_config] table, which --provider's own
local-binary-path escape hatch still writes for local/dev use -- see --provider below). Everything else
(github_repo, tf_dir, k8s_audit, ledger, intent) is left out of the default output entirely rather than shown
commented -- see the full key reference at https://github.com/Ubiquex/ubiquex-docs, cli/config, or pass --full to
get the old exhaustive, annotated file inline instead.

If no provider is given by flag and this is a real terminal, a short prompt asks for one (registry source,
version, optional region) -- press enter at the first prompt to skip and configure a provider later by hand.

Refuses to overwrite an existing config unless --force is given.`,
		// init has no "finding" concept -- it either writes the file or it
		// doesn't (UBI-20 exit-code contract): 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if resetLedger {
				return runResetLedger(cmd, dir, force)
			}
			if providerPath != "" && source != "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --provider and --source are mutually exclusive")}
			}
			if source != "" && providerVersion == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --source requires --provider-version (explicit version pins only)")}
			}
			if !cmd.Flags().Changed("format") {
				userFormat, err := userGlobalInitFormat()
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
				}
				if userFormat != "" {
					format = userFormat
				}
			}
			var render func(configTemplateValues) string
			switch {
			case full && format == "hcl":
				render = renderConfigTemplateFullHCL
			case full && format == "toml":
				render = renderConfigTemplateFullTOML
			case full && format == "yaml":
				render = renderConfigTemplateFullYAML
			case format == "hcl":
				render = renderConfigTemplateHCL
			case format == "toml":
				render = renderConfigTemplateTOML
			case format == "yaml":
				render = renderConfigTemplateYAML
			default:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --format must be one of hcl, toml, yaml (got %q)", format)}
			}

			var providerConfigMap map[string]any
			if providerConfig != "" {
				if err := json.Unmarshal([]byte(providerConfig), &providerConfigMap); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --provider-config: %w", err)}
				}
			}
			if region != "" {
				if providerConfigMap == nil {
					providerConfigMap = map[string]any{}
				}
				providerConfigMap["region"] = region
			}

			// UBI-59: a provider is the one thing a brand-new config
			// actually needs to be runnable -- if neither escape hatch
			// (--provider path, --source registry) was given by flag, and
			// this is a real terminal on both ends (not a hermetic test's
			// bytes.Buffer/strings.Reader, not CI/scripted stdin), ask
			// once, briefly, rather than write a config that immediately
			// fails the first `ubx plan`. A non-interactive invocation
			// with no provider flags gets exactly the old silent behavior
			// -- no hang, no guess -- just a config with no provider yet.
			if providerPath == "" && source == "" && isTerminal(cmd.InOrStdin()) && isTerminal(cmd.OutOrStdout()) {
				promptedSource, promptedVersion, promptedConfig := promptForProvider(cmd)
				source, providerVersion = promptedSource, promptedVersion
				if promptedConfig != nil {
					providerConfigMap = promptedConfig
				}
			}

			if stack == "" {
				stack = deriveStackFromDir(dir)
			}

			path := filepath.Join(dir, ".ubx", "config."+format)
			if info, err := os.Stat(path); err == nil {
				if info.IsDir() {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %s is a directory", path)}
				}
				if !force {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %s already exists (use --force to overwrite)", path)}
				}
			} else if !os.IsNotExist(err) {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
			} else if existing := pickConfigFile(dir); existing != "" && existing != path && !force {
				// A real, live-found gotcha (UBI-32 Arc A): configFileCandidates'
				// own discovery order means whichever of config.hcl/config.toml/
				// config/config.yaml is found FIRST wins for a directory, entirely
				// silently, if two happen to coexist there. Writing config.hcl
				// into a directory that already has a working config.toml (or the
				// legacy extensionless config) would either shadow that file's
				// values outright (if the new one outranks it) or itself never be
				// read at all (if it doesn't) -- both surprising enough to refuse
				// loudly by default, matching this session's own "ambiguity
				// rejected loudly, never guessed" standard for YAML strict mode.
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %s already exists in this directory -- writing %s would create two config files with only one ever read per discovery order (config.hcl -> config.toml -> config -> config.yaml); use --format to match the existing file, or --force to write %s anyway", existing, path, path)}
			}

			values := configTemplateValues{
				Stack:           stack,
				Source:          source,
				ProviderVersion: providerVersion,
				ProviderPath:    providerPath,
				ProviderConfig:  providerConfigMap,
				GithubRepo:      githubRepo,
				TFDir:           tfDir,
			}
			content := render(values)

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
			}

			out := cmd.OutOrStdout()
			st := newStyler(cmd)
			fmt.Fprintf(out, "%s\n", st.Green(fmt.Sprintf("+ %s has been generated successfully", path)))
			fmt.Fprintf(out, "%s\n", st.Dim(fmt.Sprintf("    see %s", docsConfigRef)))
			// UBI-87: printed unconditionally, even though the minimal
			// template never writes an [intent] block itself -- the founder
			// cost-trap this ticket fixes was hitting a credit wall with NO
			// prior warning at all, so the warning has to show up here, at
			// the one moment every new stack passes through, not only for
			// whoever thinks to open cli/config.mdx first.
			fmt.Fprintf(out, "%s\n", st.Dim(fmt.Sprintf(
				"    AI drafting (ubx propose --from-doc, ubx plan --from-doc, ubx chat) defaults to model %s if left unconfigured -- see %s, [intent]",
				claude.DefaultModel, docsConfigRef)))
			if !hasProvider(values) {
				fmt.Fprintf(out, "next: add a provider (re-run with --source/--provider-version, or edit %s by hand -- see %s), then ubx plan\n", path, docsConfigRef)
			} else {
				fmt.Fprintf(out, "next: write an intent file and run `ubx plan <file>.json`, or `ubx plan --from-doc <file>.md --stack %s` -- see %s\n", stack, docsConfigRef)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "directory to write .ubx/config.<format> into -- also where the stack name defaults from, if --stack is omitted")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .ubx/config.<format>")
	cmd.Flags().BoolVar(&full, "full", false, "write the full, exhaustive annotated reference instead of the default minimal runnable config (every key, real values where given, commented examples for the rest)")
	cmd.Flags().StringVar(&format, "format", "hcl", "config format to write: hcl (canonical), toml, or yaml (strict); falls back to ~/.ubx/config's own init_format, then hcl, if not given")
	cmd.Flags().StringVar(&stack, "stack", "", "default stack name to write into the config (default: this directory's own name)")
	cmd.Flags().StringVar(&source, "source", "", "default provider registry source, e.g. hashicorp/aws -- written into the modern [providers] map (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version, e.g. 6.54.0 (required with --source; no \"latest\" resolution)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "default provider binary path -- a local/dev escape hatch, written into the legacy singular [provider] table (mutually exclusive with --source; there is no local-path slot in the modern map)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "", `default provider config, e.g. {"region":"us-east-1"} -- merged with --region if both are given`)
	cmd.Flags().StringVar(&region, "region", "", "shorthand for --provider-config's most common key -- equivalent to --provider-config '{\"region\":\"<region>\"}'")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "default GitHub repository, e.g. acme/infra")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "default .tf directory")
	cmd.Flags().BoolVar(&resetLedger, "reset-ledger", false, "sanctioned fresh start for a broken git-directory ledger: clears the head pointer (.ubx/ledger.lock) and any stale plan drafts (.ubx/plans/), preserves the redaction salt untouched, never deletes ledger/proposals or ledger/applies. Ignores every other flag above. Refuses on a ledger whose head still resolves to a real proposal unless --force is also given (that head is healthy, not broken -- resetting it would orphan real history)")

	return cmd
}

// runResetLedger is UBI-64's own sanctioned fresh-start path: the founder-
// test finding was that deleting ledger/ by hand while leaving .ubx/ in
// place leaves .ubx/ledger.lock pointing at a proposal that no longer
// exists, and every fold-touching command (status/scan/resolve/ship/plan
// short-hash lookup, ...) then fails with a raw chain error. Rather than
// teaching users to hand-delete .ubx internals to recover (which also
// destroys .ubx/salt -- see below), this gives them one sanctioned command.
//
// Scoped deliberately narrow, decided at build time per the ticket's own
// "consider... decide at build": git-directory ledgers only, not remote
// LedgerStore-backed ones -- a remote store is a shared bucket nobody
// "hand-deletes" the internals of the way a local .ubx/ directory invites;
// its equivalent fix is restoring the missing proposal objects from
// wherever the store's own backups live, not a local CLI command. Clears
// exactly two things -- the head pointer and any stale plan drafts (parked
// under .ubx/plans/, each one pinned to a Parent this reset is about to
// make stale) -- and deliberately leaves three untouched:
//   - .ubx/salt: still needed to keep any $redacted markers in whatever
//     ledger/proposals files remain on disk meaningful, and losing it
//     gains nothing a reset needs (core/salt.go's own "never part of any
//     hashed content" already means it's harmless to keep).
//   - ledger/proposals, ledger/applies: real accepted history. Not
//     deleted -- only orphaned (unreachable once the head is genesis) --
//     since deleting genuine ledger content is a materially bigger, more
//     dangerous decision than this ticket's own "start fresh" need, and
//     the motivating scenario already has these gone by the time anyone
//     would reach for this flag.
func runResetLedger(cmd *cobra.Command, dir string, force bool) error {
	out := cmd.OutOrStdout()
	ledger := core.Open(dir)

	head, headErr := ledger.Head()
	healthy := false
	if headErr == nil && head != "" {
		if _, rerr := ledger.Read(head); rerr == nil {
			healthy = true
		}
	}
	if healthy && !force {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf(
			"init --reset-ledger: the ledger head resolves to a real, readable proposal -- this looks like a healthy ledger, not a broken one; resetting it would orphan its real history (the proposal files themselves aren't deleted, but nothing will chain to them from a fresh head again). Re-run with --force if you really mean to start over, or run `ubx verify` first to confirm whether the chain is actually broken")}
	}

	headPath := filepath.Join(dir, ".ubx", "ledger.lock")
	hadHead := headErr != nil || head != ""
	if err := os.Remove(headPath); err != nil && !os.IsNotExist(err) {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("init --reset-ledger: %w", err)}
	}

	plansDir := filepath.Join(dir, ".ubx", "plans")
	nPlans := 0
	if entries, err := os.ReadDir(plansDir); err == nil {
		nPlans = len(entries)
	} else if !os.IsNotExist(err) {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("init --reset-ledger: %w", err)}
	}
	if err := os.RemoveAll(plansDir); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("init --reset-ledger: %w", err)}
	}

	if !hadHead && nPlans == 0 {
		fmt.Fprintf(out, "no ledger head or plan drafts found at %s -- nothing to reset (salt, if any, left untouched)\n", dir)
		return nil
	}
	fmt.Fprintf(out, "ledger head cleared, %d stale plan draft(s) removed at %s -- salt preserved untouched\n", nPlans, dir)
	return nil
}

// promptForProvider is UBI-59's own "short TTY prompt" -- three lines,
// any of which can be skipped by pressing enter, never more than that:
// the whole point is getting a brand-new user to a runnable config
// faster than hand-editing commented-out TOML/HCL/YAML, not replacing
// that editing with an equally long interview. Returning "" for source
// means "configure later" -- the caller falls back to the default
// (empty/unconfigured) provider exactly like a flag-less, non-interactive
// invocation would.
func promptForProvider(cmd *cobra.Command) (source, version string, providerConfig map[string]any) {
	out := cmd.OutOrStdout()
	scanner := bufio.NewScanner(cmd.InOrStdin())

	fmt.Fprint(out, "Provider registry source, e.g. hashicorp/aws (enter to skip, configure later): ")
	if !scanner.Scan() {
		return "", "", nil
	}
	source = strings.TrimSpace(scanner.Text())
	if source == "" {
		return "", "", nil
	}

	fmt.Fprint(out, "Version, e.g. 6.60.0 (required -- no \"latest\" pin): ")
	if !scanner.Scan() {
		return "", "", nil
	}
	version = strings.TrimSpace(scanner.Text())
	if version == "" {
		fmt.Fprintln(out, "no version given -- leaving the provider unconfigured; fill in providers/provider_configs by hand, or re-run with --source/--provider-version")
		return "", "", nil
	}

	fmt.Fprint(out, "Region, optional (enter to skip): ")
	if scanner.Scan() {
		if r := strings.TrimSpace(scanner.Text()); r != "" {
			providerConfig = map[string]any{"region": r}
		}
	}
	return source, version, providerConfig
}

// deriveStackFromDir is UBI-59's "stack (from dir name or --stack)":
// dir's own resolved (absolute) base name -- resolving to absolute first
// so the common `ubx init` (dir defaults to ".") reads the real
// directory name, not the literal string ".". Returns "" for anything
// that isn't a usable name (root, or a resolution failure) -- the
// caller's own fallback (an empty Stack, exactly like today's
// no-flags-given behavior) handles that case identically to any other
// "nothing given" path.
func deriveStackFromDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	base := filepath.Base(abs)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// configTemplateValues holds whatever real values ubx init was given --
// zero values mean "write a commented example instead" (or, in the
// default minimal template, "omit entirely").
type configTemplateValues struct {
	Stack           string
	Source          string
	ProviderVersion string
	ProviderPath    string
	ProviderConfig  map[string]any
	GithubRepo      string
	TFDir           string
}

// hasProvider reports whether v carries a real provider, either shape --
// the one condition that decides both the closing "next:" hint's own
// wording and the minimal template's provider block content.
func hasProvider(v configTemplateValues) bool {
	return v.ProviderPath != "" || v.Source != ""
}

// nextStepComment is the closing "next:" pointer every template (minimal
// or --full, any format) ends with -- UBI-59's own explicit fix for the
// original template's "no next-step pointer" gap. hasProvider decides
// which of two honest next steps applies: a runnable config can go
// straight to `ubx plan`; one with no provider yet needs that filled in
// first, or `ubx plan` fails immediately, no better than before this
// session.
func nextStepComment(stack string, hasProvider bool) string {
	if stack == "" {
		stack = "<stack>"
	}
	if !hasProvider {
		return fmt.Sprintf(
			"# next: add a provider above (uncomment providers/provider_configs, or\n"+
				"# re-run `ubx init --source <registry-source> --provider-version <version>`),\n"+
				"# then `ubx plan --from-doc <file>.md --stack %s` -- see %s\n",
			stack, docsConfigRef)
	}
	return fmt.Sprintf(
		"# next: write an intent file and run `ubx plan <file>.json`, or\n"+
			"# `ubx plan --from-doc <file>.md --stack %s` -- see %s\n",
		stack, docsConfigRef)
}

// --- Minimal, runnable templates (UBI-59's own default) --------------------

// renderConfigTemplateHCL builds the default, minimal .ubx/config.hcl:
// a real stack, a real provider if one was given (modern [providers]
// map, or the legacy singular [provider] table for --provider's own
// local-path escape hatch -- never both), real github_repo/tf_dir if
// given, and a closing next-step pointer. Nothing else -- every other
// key (k8s_audit, ledger, intent) is left for cli/config.mdx or --full,
// not shown commented here at all.
func renderConfigTemplateHCL(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.hcl -- generated by `ubx init` (HCL is the canonical format).\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n\n")

	if v.Stack != "" {
		fmt.Fprintf(&b, "stack = %s\n\n", literalValue(v.Stack))
	}

	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider = {\n  path = %s\n}\n\n", literalValue(v.ProviderPath))
	case v.Source != "":
		fmt.Fprintf(&b, "providers = {\n  %s = %s\n}\n\n", literalValue(v.Source), literalValue(v.ProviderVersion))
		if len(v.ProviderConfig) > 0 {
			b.WriteString("provider_configs = {\n")
			fmt.Fprintf(&b, "  %s = {\n", literalValue(v.Source))
			for _, k := range sortedKeys(v.ProviderConfig) {
				fmt.Fprintf(&b, "    %s = %s\n", k, literalValue(v.ProviderConfig[k]))
			}
			b.WriteString("  }\n}\n\n")
		}
	default:
		b.WriteString("# providers = {\n#   \"hashicorp/aws\" = \"6.60.0\"\n# }\n")
		b.WriteString("# provider_configs = {\n#   \"hashicorp/aws\" = {\n#     region = \"us-east-1\"\n#   }\n# }\n\n")
	}

	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo = %s\n", literalValue(v.GithubRepo))
	}
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir = %s\n", literalValue(v.TFDir))
	}
	if v.GithubRepo != "" || v.TFDir != "" {
		b.WriteString("\n")
	}

	b.WriteString("# Full key reference (github_repo, tf_dir, k8s_audit, ledger, intent, the\n")
	b.WriteString("# legacy single-provider shape, ...): " + docsConfigRef + "\n")
	b.WriteString("# -- or `ubx init --full` for the same reference written inline.\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

// renderConfigTemplateTOML is renderConfigTemplateHCL's TOML sibling.
// Root-level (non-table) keys MUST come before any [table] header -- in
// TOML, a bare key after a [table] section belongs to that table, not the
// document root, regardless of blank lines between them (the same real
// ordering constraint the original --full template already had to get
// right).
func renderConfigTemplateTOML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.toml -- generated by `ubx init --format=toml`.\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n\n")

	if v.Stack != "" {
		fmt.Fprintf(&b, "stack = %q\n\n", v.Stack)
	}
	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo = %q\n", v.GithubRepo)
	}
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir = %q\n", v.TFDir)
	}
	if v.GithubRepo != "" || v.TFDir != "" {
		b.WriteString("\n")
	}

	switch {
	case v.ProviderPath != "":
		b.WriteString("[provider]\n")
		fmt.Fprintf(&b, "path = %q\n\n", v.ProviderPath)
	case v.Source != "":
		b.WriteString("[providers]\n")
		fmt.Fprintf(&b, "%q = %q\n\n", v.Source, v.ProviderVersion)
		if len(v.ProviderConfig) > 0 {
			fmt.Fprintf(&b, "[provider_configs.%q]\n", v.Source)
			for _, k := range sortedKeys(v.ProviderConfig) {
				fmt.Fprintf(&b, "%s = %s\n", k, literalValue(v.ProviderConfig[k]))
			}
			b.WriteString("\n")
		}
	default:
		b.WriteString("# [providers]\n# \"hashicorp/aws\" = \"6.60.0\"\n#\n")
		b.WriteString("# [provider_configs.\"hashicorp/aws\"]\n# region = \"us-east-1\"\n\n")
	}

	b.WriteString("# Full key reference (github_repo, tf_dir, k8s_audit, ledger, intent, the\n")
	b.WriteString("# legacy single-provider shape, ...): " + docsConfigRef + "\n")
	b.WriteString("# -- or `ubx init --full` for the same reference written inline.\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

// renderConfigTemplateYAML is renderConfigTemplateHCL's YAML sibling, in
// strict mode's own spirit: every real value quoted explicitly, never
// left as a bare token strict-mode parsing would otherwise have to judge.
func renderConfigTemplateYAML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.yaml -- generated by `ubx init --format=yaml`.\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n")
	b.WriteString("# Strict mode: quote every value explicitly -- an unquoted numeric-looking\n")
	b.WriteString("# value that would silently narrow (e.g. 6.60 -> 6.6) is a hard error.\n\n")

	if v.Stack != "" {
		fmt.Fprintf(&b, "stack: %s\n\n", literalValue(v.Stack))
	}
	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo: %s\n", literalValue(v.GithubRepo))
	}
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir: %s\n", literalValue(v.TFDir))
	}
	if v.GithubRepo != "" || v.TFDir != "" {
		b.WriteString("\n")
	}

	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider:\n  path: %s\n\n", literalValue(v.ProviderPath))
	case v.Source != "":
		fmt.Fprintf(&b, "providers:\n  %s: %s\n\n", literalValue(v.Source), literalValue(v.ProviderVersion))
		if len(v.ProviderConfig) > 0 {
			fmt.Fprintf(&b, "provider_configs:\n  %s:\n", literalValue(v.Source))
			for _, k := range sortedKeys(v.ProviderConfig) {
				fmt.Fprintf(&b, "    %s: %s\n", k, literalValue(v.ProviderConfig[k]))
			}
			b.WriteString("\n")
		}
	default:
		b.WriteString("# providers:\n#   \"hashicorp/aws\": \"6.60.0\"\n\n")
		b.WriteString("# provider_configs:\n#   \"hashicorp/aws\":\n#     region: \"us-east-1\"\n\n")
	}

	b.WriteString("# Full key reference (github_repo, tf_dir, k8s_audit, ledger, intent, the\n")
	b.WriteString("# legacy single-provider shape, ...): " + docsConfigRef + "\n")
	b.WriteString("# -- or `ubx init --full` for the same reference written inline.\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

// --- Full, exhaustive annotated templates (`ubx init --full`) --------------
//
// UBI-59: this is (modulo internal ticket numbers, stripped -- "generated
// output" is user-facing, this project's own internal issue tracker
// isn't) the same exhaustive, one-block-per-key reference the original
// `ubx init` always wrote. It moved out of the default path because a
// brand-new user's very first file was pure documentation with no
// working provider in it -- it hasn't gone away; cli/config.mdx is now
// its primary home, and --full is the same content for whoever wants it
// inline instead of a browser tab.

func renderConfigTemplateFullTOML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.toml -- generated by `ubx init --format=toml --full`.\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n\n")

	b.WriteString("# Default stack for commands that need one (e.g. `ubx scan`, `ubx status --stack`).\n")
	if v.Stack != "" {
		fmt.Fprintf(&b, "stack = %q\n", v.Stack)
	} else {
		b.WriteString("# stack = \"payments\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default GitHub repository for --from-merge / --verify-acceptance / --surface-as.\n")
	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo = %q\n", v.GithubRepo)
	} else {
		b.WriteString("# github_repo = \"acme/infra\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default .tf directory for --tf-dir (ubx writeback, ubx revert-plan, ubx scan --surface-as).\n")
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir = %q\n", v.TFDir)
	} else {
		b.WriteString("# tf_dir = \"./terraform\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider identity, legacy single-provider shape -- a local/dev escape hatch only;\n")
	b.WriteString("# new stacks should prefer [providers] below instead.\n")
	b.WriteString("[provider]\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "path = %q\n", v.ProviderPath)
		b.WriteString("# source = \"hashicorp/aws\"\n")
		b.WriteString("# version = \"6.54.0\"\n")
	default:
		b.WriteString("# path = \"/path/to/terraform-provider-aws\"\n")
		b.WriteString("# source = \"hashicorp/aws\"\n")
		b.WriteString("# version = \"6.54.0\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration for the legacy [provider] shape above.\n")
	b.WriteString("[provider_config]\n")
	b.WriteString("# region = \"us-east-1\"\n\n")

	b.WriteString("# A stack's whole declared provider set -- source -> pinned version, explicit\n")
	b.WriteString("# pins only. The modern, preferred shape for any stack, single- or\n")
	b.WriteString("# multi-provider alike.\n")
	if v.Source != "" {
		b.WriteString("[providers]\n")
		fmt.Fprintf(&b, "%q = %q\n\n", v.Source, v.ProviderVersion)
	} else {
		b.WriteString("# [providers]\n# \"hashicorp/aws\" = \"6.60.0\"\n\n")
	}

	b.WriteString("# Per-source provider configuration, for [providers] above.\n")
	if v.Source != "" && len(v.ProviderConfig) > 0 {
		fmt.Fprintf(&b, "[provider_configs.%q]\n", v.Source)
		for _, k := range sortedKeys(v.ProviderConfig) {
			fmt.Fprintf(&b, "%s = %s\n", k, literalValue(v.ProviderConfig[k]))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("# [provider_configs.\"hashicorp/aws\"]\n# region = \"us-east-1\"\n\n")
	}

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift -- entirely optional, no CLI flag equivalent. Absent or cluster unset\n")
	b.WriteString("# means such a drift's attribution records audit_unattributed/not_configured,\n")
	b.WriteString("# never blocking detection.\n")
	b.WriteString("[k8s_audit]\n")
	b.WriteString("# cluster = \"my-eks-cluster\"\n")
	b.WriteString("# region = \"us-east-1\"\n")
	b.WriteString("# log_group = \"/aws/eks/my-eks-cluster/cluster\"  # optional; defaults to this shape from cluster\n")
	b.WriteString("\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("[ledger]\n")
	b.WriteString("# store = \"s3://acme-ledger/acme/prod/\"\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

func renderConfigTemplateFullHCL(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.hcl -- generated by `ubx init --full` (HCL is the canonical format).\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n")
	b.WriteString("# Literal values only: no variables, functions, or interpolation.\n\n")

	b.WriteString("# Default stack for commands that need one (e.g. `ubx scan`, `ubx status --stack`).\n")
	if v.Stack != "" {
		fmt.Fprintf(&b, "stack = %s\n", literalValue(v.Stack))
	} else {
		b.WriteString("# stack = \"payments\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default GitHub repository for --from-merge / --verify-acceptance / --surface-as.\n")
	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo = %s\n", literalValue(v.GithubRepo))
	} else {
		b.WriteString("# github_repo = \"acme/infra\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default .tf directory for --tf-dir (ubx writeback, ubx revert-plan, ubx scan --surface-as).\n")
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir = %s\n", literalValue(v.TFDir))
	} else {
		b.WriteString("# tf_dir = \"./terraform\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider identity, legacy single-provider shape -- a local/dev escape hatch\n")
	b.WriteString("# only; new stacks should prefer `providers` below instead.\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider = {\n  path = %s\n  # source = \"hashicorp/aws\"\n  # version = \"6.54.0\"\n}\n", literalValue(v.ProviderPath))
	default:
		b.WriteString("# provider = {\n#   path = \"/path/to/terraform-provider-aws\"\n#   source = \"hashicorp/aws\"\n#   version = \"6.54.0\"\n# }\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration for the legacy provider shape above.\n")
	b.WriteString("# provider_config = {\n#   region = \"us-east-1\"\n# }\n\n")

	b.WriteString("# A stack's whole declared provider set -- source -> pinned version, explicit\n")
	b.WriteString("# pins only. The modern, preferred shape for any stack, single- or\n")
	b.WriteString("# multi-provider alike.\n")
	if v.Source != "" {
		fmt.Fprintf(&b, "providers = {\n  %s = %s\n}\n\n", literalValue(v.Source), literalValue(v.ProviderVersion))
	} else {
		b.WriteString("# providers = {\n#   \"hashicorp/aws\" = \"6.60.0\"\n# }\n\n")
	}

	b.WriteString("# Per-source provider configuration, for `providers` above.\n")
	if v.Source != "" && len(v.ProviderConfig) > 0 {
		b.WriteString("provider_configs = {\n")
		fmt.Fprintf(&b, "  %s = {\n", literalValue(v.Source))
		for _, k := range sortedKeys(v.ProviderConfig) {
			fmt.Fprintf(&b, "    %s = %s\n", k, literalValue(v.ProviderConfig[k]))
		}
		b.WriteString("  }\n}\n\n")
	} else {
		b.WriteString("# provider_configs = {\n#   \"hashicorp/aws\" = {\n#     region = \"us-east-1\"\n#   }\n# }\n\n")
	}

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift -- entirely optional, no CLI flag equivalent. Absent or cluster unset\n")
	b.WriteString("# means such a drift's attribution records audit_unattributed/not_configured,\n")
	b.WriteString("# never blocking detection.\n")
	b.WriteString("# k8s_audit = {\n#   cluster = \"my-eks-cluster\"\n#   region = \"us-east-1\"\n#   log_group = \"/aws/eks/my-eks-cluster/cluster\"\n# }\n\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("# ledger = {\n#   store = \"s3://acme-ledger/acme/prod/\"\n# }\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

func renderConfigTemplateFullYAML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.yaml -- generated by `ubx init --format=yaml --full`.\n")
	b.WriteString("# CLI flags always override these; see " + docsConfigRef + ".\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n")
	b.WriteString("# Strict mode: quote every value explicitly -- an unquoted numeric-looking\n")
	b.WriteString("# value that would silently narrow (e.g. 6.60 -> 6.6) is a hard error.\n\n")

	b.WriteString("# Default stack for commands that need one (e.g. `ubx scan`, `ubx status --stack`).\n")
	if v.Stack != "" {
		fmt.Fprintf(&b, "stack: %s\n", literalValue(v.Stack))
	} else {
		b.WriteString("# stack: \"payments\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default GitHub repository for --from-merge / --verify-acceptance / --surface-as.\n")
	if v.GithubRepo != "" {
		fmt.Fprintf(&b, "github_repo: %s\n", literalValue(v.GithubRepo))
	} else {
		b.WriteString("# github_repo: \"acme/infra\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Default .tf directory for --tf-dir (ubx writeback, ubx revert-plan, ubx scan --surface-as).\n")
	if v.TFDir != "" {
		fmt.Fprintf(&b, "tf_dir: %s\n", literalValue(v.TFDir))
	} else {
		b.WriteString("# tf_dir: \"./terraform\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider identity, legacy single-provider shape -- a local/dev escape hatch\n")
	b.WriteString("# only; new stacks should prefer `providers` below instead.\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider:\n  path: %s\n  # source: \"hashicorp/aws\"\n  # version: \"6.54.0\"\n", literalValue(v.ProviderPath))
	default:
		b.WriteString("# provider:\n#   path: \"/path/to/terraform-provider-aws\"\n#   source: \"hashicorp/aws\"\n#   version: \"6.54.0\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration for the legacy provider shape above.\n")
	b.WriteString("# provider_config:\n#   region: \"us-east-1\"\n\n")

	b.WriteString("# A stack's whole declared provider set -- source -> pinned version, explicit\n")
	b.WriteString("# pins only. The modern, preferred shape for any stack, single- or\n")
	b.WriteString("# multi-provider alike.\n")
	if v.Source != "" {
		fmt.Fprintf(&b, "providers:\n  %s: %s\n\n", literalValue(v.Source), literalValue(v.ProviderVersion))
	} else {
		b.WriteString("# providers:\n#   \"hashicorp/aws\": \"6.60.0\"\n\n")
	}

	b.WriteString("# Per-source provider configuration, for `providers` above.\n")
	if v.Source != "" && len(v.ProviderConfig) > 0 {
		fmt.Fprintf(&b, "provider_configs:\n  %s:\n", literalValue(v.Source))
		for _, k := range sortedKeys(v.ProviderConfig) {
			fmt.Fprintf(&b, "    %s: %s\n", k, literalValue(v.ProviderConfig[k]))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("# provider_configs:\n#   \"hashicorp/aws\":\n#     region: \"us-east-1\"\n\n")
	}

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift -- entirely optional, no CLI flag equivalent. Absent or cluster unset\n")
	b.WriteString("# means such a drift's attribution records audit_unattributed/not_configured,\n")
	b.WriteString("# never blocking detection.\n")
	b.WriteString("# k8s_audit:\n#   cluster: \"my-eks-cluster\"\n#   region: \"us-east-1\"\n#   log_group: \"/aws/eks/my-eks-cluster/cluster\"\n\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("# ledger:\n#   store: \"s3://acme-ledger/acme/prod/\"\n\n")

	b.WriteString(nextStepComment(v.Stack, hasProvider(v)))
	return b.String()
}

// sortedKeys (configcascade.go) returns m's own keys, sorted --
// determinism is a feature (CLAUDE.md's own standing rule): a provider
// config is a Go map once decoded from --provider-config's JSON, and
// writing it out must do so in a reproducible order, not whatever map
// iteration happens to produce this run. Reused here unchanged, not
// redefined -- genericTree is a type alias for map[string]any.

// literalValue renders a decoded-JSON value (string, float64, bool --
// the only types encoding/json ever produces from a flat
// {"key":"value"} object) as a literal -- the identical syntax TOML,
// HCL, and YAML's own double-quoted-scalar form all happen to share for
// these three value kinds, so one renderer serves all three templates.
func literalValue(v any) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", t)
	}
}
