package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// aliasNamePattern is what makes an alias argument unambiguous against
// the two shapes ubx why/ubx restore already accept: a 64-hex-char
// proposal id (never matches -- this pattern requires a leading letter
// and rejects anything of exactly hash length below) and a
// <stack>.<type>.<name> resource address (never matches -- this pattern
// forbids dots entirely). UBI-228: docs/schema.md already anticipated
// "human-friendly aliases allowed as labels" for the id field; this is
// that mechanism, built.
var aliasNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// aliasFile is the whole of UBI-228's own persistence: a plain,
// git-trackable JSON file at <ledgerDir>/.ubx/aliases.json, alongside
// .ubx/plans/ (cli/plan.go's own doc comment) and .ubx/config.hcl --
// never part of the ledger itself (core.LedgerStore has no alias
// methods, and never gains any). An alias is workspace-local metadata
// about a ledger head, the same relationship a draft plan already has to
// an accepted proposal, not ledger content in its own right.
//
// Deliberately NOT a core.LedgerStore concern: core.Ledger for a remote
// (s3://, gs://, azblob://) store carries no local directory at all
// (core/ledger.go's own OpenStore/OpenStoreForStack leave `dir` empty --
// only the git-directory Open sets it), so an alias file addressed by
// LedgerStore would need new interface methods implemented across every
// store backend for what is fundamentally a human-managed, git-shared
// artifact, not machine-written ledger content. The workspace directory
// (--ledger-dir) that holds .ubx/config.hcl always exists locally
// regardless of which store backs the ledger's own proposals, so that is
// where aliases live too -- one mechanism, uniform for a git-local or a
// remote-store-backed stack alike.
//
// Sharing an alias with a team means committing this file, the same way
// .ubx/config.hcl already is -- two people adding conflicting aliases on
// separate branches is an ordinary git merge conflict on a JSON file,
// the same experience config.hcl already has, not a new problem this
// feature needs its own resolution mechanism for. Keys are sorted
// (encoding/json's own map-key ordering) specifically so most edits stay
// a small, mergeable diff.
type aliasFile struct {
	SchemaVersion int                               `json:"schema_version"`
	Aliases       map[string]map[string]aliasRecord `json:"aliases"` // stack -> name -> record
}

type aliasRecord struct {
	Head      string `json:"head"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func aliasFilePath(ledgerDir string) string {
	return filepath.Join(ledgerDir, ".ubx", "aliases.json")
}

// loadAliasFile returns an empty, ready-to-use aliasFile if none has
// been written yet -- never an error for "no aliases assigned so far",
// matching every other "record-only if present" file this package reads
// (cli/plan.go's own readPlanFile is the one exception, precisely
// because a missing plan IS an error there; a missing alias file is the
// ordinary starting state).
func loadAliasFile(ledgerDir string) (*aliasFile, error) {
	data, err := os.ReadFile(aliasFilePath(ledgerDir))
	if errors.Is(err, os.ErrNotExist) {
		return &aliasFile{SchemaVersion: 1, Aliases: map[string]map[string]aliasRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var af aliasFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("parse %s: %w", aliasFilePath(ledgerDir), err)
	}
	if af.Aliases == nil {
		af.Aliases = map[string]map[string]aliasRecord{}
	}
	return &af, nil
}

func saveAliasFile(ledgerDir string, af *aliasFile) error {
	path := aliasFilePath(ledgerDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// resolveHeadOrAlias is the one new interaction UBI-228 adds: arg itself
// if it already matches the 64-hex-char hash shape, otherwise looked up
// as a stack-scoped alias. Every existing hash-accepting command keeps
// accepting a raw hash completely unchanged; a caller earns alias
// support by routing its own argument through this instead of
// proposalIDPattern.MatchString directly (cli/why.go, cli/restore.go).
//
// stack, if non-empty, scopes the lookup to that stack's own alias
// namespace only -- the same "an explicit stack is unambiguous, an
// omitted one searches everything and must resolve to exactly one hit"
// shape core.ParseAddress's own self-naming stack makes unnecessary for
// a resource address, but an alias argument (a bare name, never
// stack-qualified on the command line) has no such self-naming to rely
// on. Returns the alias's own stack alongside the resolved hash so a
// caller that opened its ledger with an empty --stack can adopt the
// resolved one, exactly as ubx why's address branch already does with
// addr.Stack.
func resolveHeadOrAlias(ledgerDir, stack, arg string) (resolvedHash, resolvedStack string, err error) {
	if proposalIDPattern.MatchString(arg) {
		return arg, stack, nil
	}
	af, err := loadAliasFile(ledgerDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve alias %q: %w", arg, err)
	}
	if stack != "" {
		if rec, ok := af.Aliases[stack][arg]; ok {
			return rec.Head, stack, nil
		}
		return "", "", fmt.Errorf("no alias %q in stack %q -- list known aliases with `ubx alias list --stack %s`", arg, stack, stack)
	}
	var matches []string
	var foundHash string
	for s, names := range af.Aliases {
		if rec, ok := names[arg]; ok {
			matches = append(matches, s)
			foundHash = rec.Head
		}
	}
	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("%q is not a 64-char proposal hash or a known alias -- list known aliases with `ubx alias list`", arg)
	case 1:
		return foundHash, matches[0], nil
	default:
		sort.Strings(matches)
		return "", "", fmt.Errorf("alias %q exists in more than one stack (%s) -- pass --stack to disambiguate", arg, strings.Join(matches, ", "))
	}
}

func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Human-readable names for ledger heads (UBI-228) -- assign, resolve, list, remove",
	}
	cmd.AddCommand(newAliasSetCmd())
	cmd.AddCommand(newAliasResolveCmd())
	cmd.AddCommand(newAliasListCmd())
	cmd.AddCommand(newAliasRemoveCmd())
	return cmd
}

func newAliasSetCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		force     bool
	)
	cmd := &cobra.Command{
		Use:           "set <name> <head-or-alias>",
		Short:         "Assign an alias to a ledger head -- refuses to repoint an existing alias without --force",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, target := args[0], args[1]
			if !aliasNamePattern.MatchString(name) {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %q is not a legal alias name -- letters, digits, \"_\", \"-\" only, must start with a letter (never a 64-char hex hash, never containing a dot)", name)}
			}
			if proposalIDPattern.MatchString(name) {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %q is itself a well-formed proposal hash -- it would never be reachable as an alias", name)}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: stackRequiredError("alias set", nil)}
			}

			head, resolvedStack, err := resolveHeadOrAlias(ledgerDir, stack, target)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %w", err)}
			}
			if resolvedStack != "" {
				stack = resolvedStack
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %w", err)}
			}
			defer closeLedger()
			if _, err := ledger.Read(head); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %s does not name a real proposal in stack %q: %w", displayHash(head, false), stack, err)}
			}

			af, err := loadAliasFile(ledgerDir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %w", err)}
			}
			if af.Aliases[stack] == nil {
				af.Aliases[stack] = map[string]aliasRecord{}
			}
			now := time.Now().UTC().Format(time.RFC3339)
			existing, exists := af.Aliases[stack][name]
			if exists && existing.Head != head && !force {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %q already points to %s in stack %q -- pass --force to repoint it to %s", name, displayHash(existing.Head, false), stack, displayHash(head, false))}
			}
			rec := aliasRecord{Head: head, CreatedAt: now}
			if exists {
				rec.CreatedAt = existing.CreatedAt
				rec.UpdatedAt = now
			}
			af.Aliases[stack][name] = rec
			if err := saveAliasFile(ledgerDir, af); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias set: %w", err)}
			}

			out := cmd.OutOrStdout()
			st := newStyler(cmd)
			if exists {
				fmt.Fprintf(out, "%s: %s -> %s (repointed from %s)\n", stack, name, st.Hash(head), st.Hash(existing.Head))
			} else {
				fmt.Fprintf(out, "%s: %s -> %s\n", stack, name, st.Hash(head))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack this alias belongs to -- defaults to .ubx/config.hcl's own stack key")
	cmd.Flags().BoolVar(&force, "force", false, "repoint an alias that already points somewhere else")
	return cmd
}

func newAliasResolveCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:           "resolve <name>",
		Short:         "Print the ledger head an alias currently points at",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias resolve: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			head, resolvedStack, err := resolveHeadOrAlias(ledgerDir, stack, args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias resolve: %w", err)}
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(out, struct {
					Format int    `json:"format"`
					Name   string `json:"name"`
					Stack  string `json:"stack"`
					Head   string `json:"head"`
				}{jsonFormatVersion, args[0], resolvedStack, head})
			}
			fmt.Fprintln(out, head)
			return nil
		},
	}
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to look the alias up in -- omit to search every stack this ledger dir holds an alias for (errors if the name is ambiguous across more than one)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of the bare hash")
	return cmd
}

func newAliasListCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List every alias this ledger dir knows about",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias list: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			af, err := loadAliasFile(ledgerDir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias list: %w", err)}
			}

			type row struct {
				Stack string `json:"stack"`
				Name  string `json:"name"`
				aliasRecord
			}
			var rows []row
			for s, names := range af.Aliases {
				if stack != "" && s != stack {
					continue
				}
				for name, rec := range names {
					rows = append(rows, row{Stack: s, Name: name, aliasRecord: rec})
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Stack != rows[j].Stack {
					return rows[i].Stack < rows[j].Stack
				}
				return rows[i].Name < rows[j].Name
			})

			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(out, struct {
					Format  int   `json:"format"`
					Entries []row `json:"entries"`
				}{jsonFormatVersion, rows})
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "(no aliases assigned yet -- see `ubx alias set`)")
				return nil
			}
			st := newStyler(cmd)
			for _, r := range rows {
				fmt.Fprintf(out, "%s.%s -> %s\n", r.Stack, r.Name, st.Hash(r.Head))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "restrict the listing to one stack -- every stack this ledger dir holds an alias for, by default")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	return cmd
}

func newAliasRemoveCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
	)
	cmd := &cobra.Command{
		Use:           "remove <name>",
		Short:         "Remove an alias -- never touches the proposal it pointed at",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias remove: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: stackRequiredError("alias remove", nil)}
			}

			af, err := loadAliasFile(ledgerDir)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias remove: %w", err)}
			}
			if _, ok := af.Aliases[stack][args[0]]; !ok {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias remove: no alias %q in stack %q", args[0], stack)}
			}
			delete(af.Aliases[stack], args[0])
			if err := saveAliasFile(ledgerDir, af); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("alias remove: %w", err)}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: removed alias %s\n", stack, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack this alias belongs to -- defaults to .ubx/config.hcl's own stack key")
	return cmd
}
