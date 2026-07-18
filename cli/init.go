package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// newInitCmd is UBI-19's config bootstrapper -- a new verb, no
// init-shaped command existed before this session
// (docs/architecture.md — Config defaults). Writes .ubx/config.<format>:
// a real, active value for every key the caller supplied a flag for, a
// commented-out example for everything else, so the file is immediately
// useful as its own documentation of what's possible.
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
		format          string
		stack           string
		source          string
		providerVersion string
		providerPath    string
		providerConfig  string
		githubRepo      string
		tfDir           string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter .ubx/config.<format> -- real values for whatever flags are given, commented examples for the rest",
		Long: `Write .ubx/config.<format>, the defaults file every ubx command reads (docs/architecture.md — Config defaults
and Config formats): provider identity, provider configuration, default stack, GitHub repository, and .tf directory.
Any flag you give here is written as a real, active value; anything you don't is written as a commented-out example
showing the correct syntax. --format selects hcl (default, canonical), toml, or yaml (strict, fully-quoted output).
Refuses to overwrite an existing config unless --force is given.`,
		// init has no "finding" concept -- it either writes the file or it
		// doesn't (UBI-20 exit-code contract): 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if providerPath != "" && source != "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --provider and --source are mutually exclusive")}
			}
			if source != "" && providerVersion == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: --source requires --provider-version (explicit version pins only)")}
			}
			var render func(configTemplateValues) string
			switch format {
			case "hcl":
				render = renderConfigTemplateHCL
			case "toml":
				render = renderConfigTemplateTOML
			case "yaml":
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

			content := render(configTemplateValues{
				Stack:           stack,
				Source:          source,
				ProviderVersion: providerVersion,
				ProviderPath:    providerPath,
				ProviderConfig:  providerConfigMap,
				GithubRepo:      githubRepo,
				TFDir:           tfDir,
			})

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("init: %w", err)}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "directory to write .ubx/config.<format> into")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .ubx/config.<format>")
	cmd.Flags().StringVar(&format, "format", "hcl", "config format to write: hcl (canonical, default), toml, or yaml (strict)")
	cmd.Flags().StringVar(&stack, "stack", "", "default stack name to write into the config")
	cmd.Flags().StringVar(&source, "source", "", "default provider source, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "default provider version, e.g. 6.54.0 (used with --source)")
	cmd.Flags().StringVar(&providerPath, "provider", "", "default provider binary path (mutually exclusive with --source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "", `default provider config, e.g. {"region":"us-east-1"}`)
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "default GitHub repository, e.g. acme/infra")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "default .tf directory")

	return cmd
}

// configTemplateValues holds whatever real values ubx init was given --
// zero values mean "write a commented example instead."
type configTemplateValues struct {
	Stack           string
	Source          string
	ProviderVersion string
	ProviderPath    string
	ProviderConfig  map[string]any
	GithubRepo      string
	TFDir           string
}

// renderConfigTemplateTOML builds .ubx/config.toml's full text: a header
// comment, then one block per config key, real values where given,
// commented examples otherwise.
func renderConfigTemplateTOML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.toml -- generated by `ubx init --format=toml`.\n")
	b.WriteString("# CLI flags always override these; see https://github.com/Ubiquex/ubiquex-docs, cli/config.\n")
	b.WriteString("# Unknown keys warn, they don't fail -- safe to hand-edit.\n\n")

	// Root-level (non-table) keys MUST come before any [table] header --
	// in TOML, a bare key after a [table] section belongs to that table,
	// not the document root, regardless of blank lines between them. This
	// is a real ordering constraint, not a style choice: getting it
	// backwards (tables first) silently swallows stack/github_repo/tf_dir
	// into whatever table came last, and every value defined this way
	// reads back empty -- caught only by writing a real config file with
	// `ubx init` and decoding it back, not by inspecting the string
	// template by eye.
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

	b.WriteString("# Provider identity: EITHER a local binary path, OR a registry source + explicit version.\n")
	b.WriteString("[provider]\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "path = %q\n", v.ProviderPath)
		b.WriteString("# source = \"hashicorp/aws\"\n")
		b.WriteString("# version = \"6.54.0\"\n")
	case v.Source != "":
		b.WriteString("# path = \"/path/to/terraform-provider-aws\"\n")
		fmt.Fprintf(&b, "source = %q\n", v.Source)
		fmt.Fprintf(&b, "version = %q\n", v.ProviderVersion)
	default:
		b.WriteString("# path = \"/path/to/terraform-provider-aws\"\n")
		b.WriteString("# source = \"hashicorp/aws\"\n")
		b.WriteString("# version = \"6.54.0\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration, e.g. the region a provider should read from.\n")
	b.WriteString("[provider_config]\n")
	if len(v.ProviderConfig) > 0 {
		keys := make([]string, 0, len(v.ProviderConfig))
		for k := range v.ProviderConfig {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, literalValue(v.ProviderConfig[k]))
		}
	} else {
		b.WriteString("# region = \"us-east-1\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift (UBI-22) -- entirely optional, no CLI flag equivalent. Absent or\n")
	b.WriteString("# cluster unset means such a drift's attribution records\n")
	b.WriteString("# audit_unattributed/not_configured, never blocking detection.\n")
	b.WriteString("[k8s_audit]\n")
	b.WriteString("# cluster = \"my-eks-cluster\"\n")
	b.WriteString("# region = \"us-east-1\"\n")
	b.WriteString("# log_group = \"/aws/eks/my-eks-cluster/cluster\"  # optional; defaults to this shape from cluster\n")
	b.WriteString("\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("[ledger]\n")
	b.WriteString("# store = \"s3://acme-ledger/acme/prod/\"\n")

	return b.String()
}

// renderConfigTemplateHCL builds .ubx/config.hcl's full text -- the
// canonical format `ubx init` writes by default (UBI-32 Arc A).
// Every table is an attribute holding an object-constructor expression
// (`provider = { ... }`), never an HCL block -- see confighcl.go's own
// doc comment for why blocks don't work here at all (quoted argument
// names aren't valid HCL). Unlike the TOML renderer, key order here is
// never load-bearing -- an object-constructor expression has no
// "everything after this belongs to a different section" footgun.
func renderConfigTemplateHCL(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.hcl -- generated by `ubx init` (HCL is the canonical format).\n")
	b.WriteString("# CLI flags always override these; see https://github.com/Ubiquex/ubiquex-docs, cli/config.\n")
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

	b.WriteString("# Provider identity: EITHER a local binary path, OR a registry source + explicit version.\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider = {\n  path = %s\n  # source = \"hashicorp/aws\"\n  # version = \"6.54.0\"\n}\n", literalValue(v.ProviderPath))
	case v.Source != "":
		fmt.Fprintf(&b, "provider = {\n  # path = \"/path/to/terraform-provider-aws\"\n  source  = %s\n  version = %s\n}\n", literalValue(v.Source), literalValue(v.ProviderVersion))
	default:
		b.WriteString("# provider = {\n#   path = \"/path/to/terraform-provider-aws\"\n#   source = \"hashicorp/aws\"\n#   version = \"6.54.0\"\n# }\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration, e.g. the region a provider should read from.\n")
	if len(v.ProviderConfig) > 0 {
		b.WriteString("provider_config = {\n")
		keys := make([]string, 0, len(v.ProviderConfig))
		for k := range v.ProviderConfig {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s = %s\n", k, literalValue(v.ProviderConfig[k]))
		}
		b.WriteString("}\n")
	} else {
		b.WriteString("# provider_config = {\n#   region = \"us-east-1\"\n# }\n")
	}
	b.WriteString("\n")

	b.WriteString("# A stack's whole declared provider set (UBI-43 multi-provider stacks) --\n")
	b.WriteString("# source -> pinned version, explicit pins only.\n")
	b.WriteString("# providers = {\n#   \"hashicorp/aws\" = \"6.60.0\"\n# }\n\n")

	b.WriteString("# Per-source provider configuration, for a multi-provider stack.\n")
	b.WriteString("# provider_configs = {\n#   \"hashicorp/aws\" = {\n#     region = \"us-east-1\"\n#   }\n# }\n\n")

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift (UBI-22) -- entirely optional, no CLI flag equivalent. Absent or\n")
	b.WriteString("# cluster unset means such a drift's attribution records\n")
	b.WriteString("# audit_unattributed/not_configured, never blocking detection.\n")
	b.WriteString("# k8s_audit = {\n#   cluster = \"my-eks-cluster\"\n#   region = \"us-east-1\"\n#   log_group = \"/aws/eks/my-eks-cluster/cluster\"\n# }\n\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("# ledger = {\n#   store = \"s3://acme-ledger/acme/prod/\"\n# }\n")

	return b.String()
}

// renderConfigTemplateYAML builds .ubx/config.yaml's full text, in
// strict mode's own spirit: every real value quoted explicitly, never
// left as a bare token strict-mode parsing would otherwise have to judge
// (docs/architecture.md's own "writes fully-quoted, unambiguous output").
func renderConfigTemplateYAML(v configTemplateValues) string {
	var b strings.Builder
	b.WriteString("# .ubx/config.yaml -- generated by `ubx init --format=yaml`.\n")
	b.WriteString("# CLI flags always override these; see https://github.com/Ubiquex/ubiquex-docs, cli/config.\n")
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

	b.WriteString("# Provider identity: EITHER a local binary path, OR a registry source + explicit version.\n")
	switch {
	case v.ProviderPath != "":
		fmt.Fprintf(&b, "provider:\n  path: %s\n  # source: \"hashicorp/aws\"\n  # version: \"6.54.0\"\n", literalValue(v.ProviderPath))
	case v.Source != "":
		fmt.Fprintf(&b, "provider:\n  # path: \"/path/to/terraform-provider-aws\"\n  source: %s\n  version: %s\n", literalValue(v.Source), literalValue(v.ProviderVersion))
	default:
		b.WriteString("# provider:\n#   path: \"/path/to/terraform-provider-aws\"\n#   source: \"hashicorp/aws\"\n#   version: \"6.54.0\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# Provider configuration, e.g. the region a provider should read from.\n")
	if len(v.ProviderConfig) > 0 {
		b.WriteString("provider_config:\n")
		keys := make([]string, 0, len(v.ProviderConfig))
		for k := range v.ProviderConfig {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, literalValue(v.ProviderConfig[k]))
		}
	} else {
		b.WriteString("# provider_config:\n#   region: \"us-east-1\"\n")
	}
	b.WriteString("\n")

	b.WriteString("# A stack's whole declared provider set (UBI-43 multi-provider stacks) --\n")
	b.WriteString("# source -> pinned version, explicit pins only.\n")
	b.WriteString("# providers:\n#   \"hashicorp/aws\": \"6.60.0\"\n\n")

	b.WriteString("# Per-source provider configuration, for a multi-provider stack.\n")
	b.WriteString("# provider_configs:\n#   \"hashicorp/aws\":\n#     region: \"us-east-1\"\n\n")

	b.WriteString("# EKS control-plane audit log attribution for kubernetes_*/helm_release\n")
	b.WriteString("# drift (UBI-22) -- entirely optional, no CLI flag equivalent. Absent or\n")
	b.WriteString("# cluster unset means such a drift's attribution records\n")
	b.WriteString("# audit_unattributed/not_configured, never blocking detection.\n")
	b.WriteString("# k8s_audit:\n#   cluster: \"my-eks-cluster\"\n#   region: \"us-east-1\"\n#   log_group: \"/aws/eks/my-eks-cluster/cluster\"\n\n")

	b.WriteString("# Which LedgerStore backs this stack -- absent or \"git\" is today's exact\n")
	b.WriteString("# in-repo directory behavior (--ledger-dir), unchanged. A stack name is\n")
	b.WriteString("# always appended as a further path segment, never configured here.\n")
	b.WriteString("# ledger:\n#   store: \"s3://acme-ledger/acme/prod/\"\n")

	return b.String()
}

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
