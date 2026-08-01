package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider/claude"
)

// newPlanCmd is UBI-49's own terraform-shaped fusion of propose+resolve+
// preview into one command (docs/architecture.md's "Two-step fusion"
// amendment): takes any medium input this codebase already knows how to
// turn into an ubx:intent/v1 document (a hand-written intent file,
// --from-code's TypeScript SDK, --from-doc's markdown draft, or
// --from-diagram's .d2 topology), resolves it through the exact same,
// unmodified core/resolver.Resolve every other entry point already uses,
// renders the full receipt (delta, cost_delta, blast radius, assumptions/
// questions) for a human to review right here, and saves the result as a
// hash-addressed local plan file `ubx ship <hash>` can later pick up for
// inline local-tier acceptance. Never touches the ledger itself -- exactly
// like `ubx resolve` and `ubx propose` today, this is preview-only; only
// `ubx accept`/`ubx ship` ever record anything.
//
// The md/diagram media's own established "draft, then a separate human
// checkpoint before resolving" posture (docs/intent-provider.md,
// docs/diagram-medium.md) is deliberately NOT reproduced here: `ubx plan`
// is a new, additional fast path for the sandbox/solo workflow, and its
// own receipt (rendered before anything is saved or shipped) already IS
// that checkpoint, now covering the full resolved proposal -- delta, cost,
// blast radius, and any ambiguity content together -- rather than the
// draft's ambiguity content alone. `ubx propose --from-doc`/`--from-
// diagram`'s own draft-only, stop-before-resolving behavior is completely
// unchanged for teams who want that extra checkpoint as a separate step.
func newPlanCmd() *cobra.Command {
	var (
		ledgerDir        string
		providerPath     string
		source           string
		providerVersion  string
		out              string
		timeout          time.Duration
		knownDependents  []string
		fromCode         string
		fromDoc          string
		fromDiagram      string
		stack            string
		summary          string
		neighborLedgers  []string
		fullHashes       bool
		showDefaultsFlag bool
		hideDefaultsFlag bool
	)

	cmd := &cobra.Command{
		Use:   "plan [intent-file]",
		Short: "Resolve any medium input into a draft proposal, render its full receipt, and save it as a hash-addressed plan for `ubx ship`",
		Long: `Fuses "ubx propose" + "ubx resolve" + a preview render into one command -- the
terraform-shaped, two-step half of this project's own workflow (plan, then "ubx ship <hash>").

Exactly one input is required: a hand-written ubx:intent/v1 file (the positional argument),
--from-code <entry>.ts|.go|.py (a TypeScript, Go, or Python SDK program, dispatched by
extension to the identical evaluator "ubx resolve --from-code" uses), --from-doc <file>.md
--stack <stack> (a markdown authoring document, transcribed via the configured [intent]
provider adapter exactly like "ubx propose --from-doc"), or --from-diagram <file>.d2 --stack
<stack> (a D2 diagram's own topology, parsed exactly like "ubx propose --from-diagram").

Bare "ubx plan" (no argument, no --from-*) auto-detects a single medium file in the working
directory (an intent .md authoring doc, a .d2 diagram, or a .ts/.go/.py SDK program) and plans
it automatically. Multiple candidates are listed, never guessed -- rerun naming one explicitly.

The result resolves through the identical, unmodified core/resolver.Resolve every other entry
point already uses -- same invariants, same orphan/pin checks, same failure modes. Its full
receipt (delta, cost_delta, blast radius, assumptions/defaults/questions) renders to the
terminal for review, and the resolved-but-unaccepted proposal is written to
.ubx/plans/<hash>.json, keyed by the exact hash "ubx ship <hash>" will later look for.

This command never touches a ledger -- nothing here is accepted or applied. Run "ubx ship
<hash>" to accept (local tier) and apply in one step, or run "ubx accept"/"ubx propose" by
hand against the written plan file for the four-verb ceremony (PR-merge signing, a separate
propose-time PR trailer hash, etc.).`,
		Args: cobra.MaximumNArgs(1),
		// plan has no "finding" concept, the same audit outcome as
		// propose/resolve (UBI-20 exit-code contract): it either resolves
		// or it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := len(args)
			if fromCode != "" {
				modes++
			}
			if fromDoc != "" {
				modes++
			}
			if fromDiagram != "" {
				modes++
			}
			if modes > 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("plan: an intent-file argument, --from-code, --from-doc, and --from-diagram are mutually exclusive")}
			}
			if modes == 0 {
				candidates, derr := autodetectMedium(ledgerDir)
				if derr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: requires exactly one of an intent-file argument, --from-code, --from-doc, or --from-diagram (auto-detection failed: %w)", derr)}
				}
				switch len(candidates) {
				case 1:
					switch candidates[0].flag {
					case "--from-doc":
						fromDoc = candidates[0].path
					case "--from-diagram":
						fromDiagram = candidates[0].path
					case "--from-code":
						fromCode = candidates[0].path
					}
				case 0:
					return &ExitCodeError{Code: 2, Err: errors.New("plan: requires exactly one of an intent-file argument, --from-code, --from-doc, or --from-diagram")}
				default:
					names := make([]string, len(candidates))
					hints := make([]string, len(candidates))
					for i, c := range candidates {
						names[i] = c.path
						hints[i] = fmt.Sprintf("ubx plan %s %s", c.flag, c.path)
					}
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: multiple mediums found: %s -- pick one: %s", strings.Join(names, ", "), strings.Join(hints, " | "))}
				}
			}

			rc, err := LoadConfigResolved(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			cfg := rc.Config

			// UBI-72: resolved up front, before any drafting/resolving
			// work -- a --show-defaults/--hide-defaults conflict is a
			// usage error, not something worth doing real work before
			// discovering.
			showDefaults, err := resolveShowDefaults(cmd, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			outWriter := cmd.OutOrStdout()

			var intent resolver.IntentFile
			var sourceLabel string
			var showedDraftProgress bool
			switch {
			case fromDoc != "":
				applyStackDefault(cmd, &stack, cfg)
				if stack == "" {
					return &ExitCodeError{Code: 2, Err: stackRequiredError("plan --from-doc", rc.Files)}
				}
				// docs/cli-output-spec.md §v2's own "plan -- progress" rule
				// (UBI-63): the intent-provider call is seconds-long against
				// a real API, so a bare receipt render with nothing printed
				// in between reads as a hang, not a wait. Named here (not
				// inside draftFromDoc itself, which `ubx propose --from-doc`
				// also calls) so propose's own output is untouched --
				// resolving, the second half of this line, is meaningless
				// there, since propose never resolves.
				adapterName := cfg.Intent.Adapter
				if adapterName == "" {
					adapterName = "claude"
				}
				model := cfg.Intent.Model
				if model == "" {
					model = claude.DefaultModel
				}
				fmt.Fprintf(outWriter, "drafting via %s:%s… ", adapterName, model)
				showedDraftProgress = true
				draft, err := draftFromDoc(cmd, cfg, fromDoc, stack, timeout)
				if err != nil {
					fmt.Fprintln(outWriter)
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan --from-doc: %w", err)}
				}
				fmt.Fprint(outWriter, "✓ · resolving…")
				intent = *draft
				sourceLabel = fromDoc
			case fromDiagram != "":
				applyStackDefault(cmd, &stack, cfg)
				if stack == "" {
					return &ExitCodeError{Code: 2, Err: stackRequiredError("plan --from-diagram", rc.Files)}
				}
				// draftFromDiagram already loads every declared provider
				// once for diagram.Parse's own type-inference pass; the
				// resolver.Resolve call below loads them again (schema-only,
				// no cloud calls either time) -- a real, known duplication,
				// accepted here rather than threading a providers slice
				// through draftFromDiagram's own signature, which every
				// other caller (propose.go's runProposeFromDiagram) doesn't
				// need at all.
				draft, err := draftFromDiagram(cmd, cfg, fromDiagram, stack, summary, neighborLedgers, timeout)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan --from-diagram: %w", err)}
				}
				intent = *draft
				sourceLabel = fromDiagram
			case fromCode != "":
				canon, err := evaluateSDKProgram(ctx, fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
				if err := json.Unmarshal(canon, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: parse evaluated intent: %w", err)}
				}
				sourceLabel = fromCode
			default:
				data, err := os.ReadFile(args[0])
				if err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				if err := json.Unmarshal(data, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: parse intent file: %w", err)}
				}
				sourceLabel = args[0]
			}

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				if showedDraftProgress {
					fmt.Fprintln(outWriter)
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, intent.Stack, cfg)
			if err != nil {
				if showedDraftProgress {
					fmt.Fprintln(outWriter)
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			defer closeLedger()

			p, err := resolver.Resolve(ledger, providers, &intent, knownDependents)
			if err != nil {
				if showedDraftProgress {
					fmt.Fprintln(outWriter)
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			if showedDraftProgress {
				fmt.Fprintln(outWriter)
			}

			hash, err := core.Hash(p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: marshal proposal: %w", err)}
			}

			_, err = writePlanFile(ledgerDir, hash, data)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
			}

			st := newStylerFull(cmd, fullHashes)
			renderPlanReceipt(outWriter, st, p, planReceiptHeader(st, p.Stack, sourceLabel), showDefaults)
			// UBI-49 polish: the hash IS the reference (docs/cli-output-
			// spec.md principle 3) -- the plan file's own path on disk is
			// an implementation detail nothing downstream ever needs (not
			// even `ubx ship`, which resolves by hash through the plan
			// store, never a path); dropped as pure noise a human had to
			// visually skip past to find the two lines that matter.
			//
			// docs/cli-output-spec.md §v2: both footer lines render green
			// AND bold -- the plain displayHash text, not st.Hash's own
			// Blue wrapping, since color()'s single-reset-at-the-end design
			// means nesting one style inside another clobbers the outer one
			// at the inner call's own reset (style.go's GreenBold doc
			// comment).
			fmt.Fprintf(outWriter, "\n%s\n%s\n",
				st.GreenBold(fmt.Sprintf("ubx-proposal: %s", displayHash(hash, st.fullHashes))),
				st.GreenBold(fmt.Sprintf("next: %s", nextShipHint([]string{hash}))))
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/ -- also where the plan is saved, at .ubx/plans/<hash>.json")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for provider/schema acquisition, drafting (--from-doc), and evaluation (--from-code) -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil,
		"ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	cmd.Flags().StringVar(&fromCode, "from-code", "", "evaluate a TypeScript (@ubx/sdk), Go (ubx-sdk-go), or Python (ubx_sdk) SDK program, dispatched by extension, instead of reading an intent file")
	cmd.Flags().StringVar(&fromDoc, "from-doc", "", "path to a markdown authoring document -- transcribes it into an intent/v1 draft via the configured [intent] provider, then resolves it")
	cmd.Flags().StringVar(&fromDiagram, "from-diagram", "", "path to a .d2 diagram -- transcribes its topology into an intent/v1 draft, then resolves it")
	cmd.Flags().StringVar(&stack, "stack", "", "target stack name (required with --from-doc or --from-diagram; the intent-file and --from-code inputs already name their own stack)")
	cmd.Flags().StringVar(&summary, "summary", "", "intent.summary for the draft (only with --from-diagram -- a diagram has no prose to derive one from; defaults to a generated summary naming the stack and resource count if omitted)")
	cmd.Flags().StringArrayVar(&neighborLedgers, "neighbor-ledger", nil, "<stack>=<path> mapping a diagram's own cross-stack reference to a real ledger directory, overriding the \"../<stack>\" convention (repeatable, only with --from-diagram)")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
	cmd.Flags().BoolVar(&showDefaultsFlag, "show-defaults", false, "render the full \"AI defaults\" block regardless of [intent] show_defaults (mutually exclusive with --hide-defaults)")
	cmd.Flags().BoolVar(&hideDefaultsFlag, "hide-defaults", false, "collapse the \"AI defaults\" block to a one-line count regardless of [intent] show_defaults (mutually exclusive with --show-defaults) -- full detail is always in the saved plan file and the signed proposal either way")
	return cmd
}

// nonAuthoringMarkdownBasenames is docs/cli-output-spec.md §v2's own
// "README.md-class files must never false-positive" detection rule --
// well-known project-meta markdown files that are never themselves an
// authoring document for bare `ubx plan`'s auto-detection, checked
// case-insensitively against the candidate's own basename (some of
// these conventionally have no extension at all).
var nonAuthoringMarkdownBasenames = map[string]bool{
	"readme": true, "readme.md": true,
	"changelog": true, "changelog.md": true,
	"license": true, "license.md": true,
	"contributing": true, "contributing.md": true,
	"code_of_conduct": true, "code_of_conduct.md": true,
	"security": true, "security.md": true,
	"authors": true, "authors.md": true,
	"notice": true, "notice.md": true,
}

// sdkImportMarkers is what distinguishes a real SDK authoring program
// from an arbitrary .ts/.go/.py file that happens to sit in the working
// directory (genuinely common, especially for .go -- this is itself a
// Go module) -- content sniffing on the real import every SDK program
// actually carries, never a bare extension match, per docs/cli-output-
// spec.md §v2's own "extension + intent-marker sniffing" rule.
var sdkImportMarkers = map[string]string{
	".ts": `"@ubx/sdk"`,
	".go": `"github.com/ubiquex/ubx-sdk-go/runtime"`,
	".py": "import ubx_sdk",
}

// detectedMedium is one candidate file autodetectMedium found, paired
// with the --from-* flag it corresponds to -- used both to auto-plan a
// lone candidate and to build the "pick one" teaching error naming each
// candidate's own correct flag.
type detectedMedium struct {
	path string
	flag string // "--from-doc", "--from-diagram", or "--from-code"
}

// autodetectMedium implements docs/cli-output-spec.md §v2's own bare
// "ubx plan" auto-detection: exactly one medium file in dir plans
// automatically, no --from-* flag needed; multiple candidates are
// listed and the caller must pick explicitly, never guessed. A single,
// non-recursive directory listing -- bare `ubx plan` is a one-
// directory-at-a-time convenience, matching every other relative-path
// flag this command already has.
func autodetectMedium(dir string) ([]detectedMedium, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []detectedMedium
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		switch ext := strings.ToLower(filepath.Ext(name)); ext {
		case ".md":
			if nonAuthoringMarkdownBasenames[strings.ToLower(name)] {
				continue
			}
			found = append(found, detectedMedium{path: path, flag: "--from-doc"})
		case ".d2":
			found = append(found, detectedMedium{path: path, flag: "--from-diagram"})
		case ".ts", ".go", ".py":
			marker := sdkImportMarkers[ext]
			content, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(content), marker) {
				continue
			}
			found = append(found, detectedMedium{path: path, flag: "--from-code"})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	return found, nil
}

// renderPlanReceipt is `ubx plan`'s own human-readable preview (also
// reused, unchanged, by `ubx terminate` and `ubx ship`'s own interactive
// confirmation) -- the same "make the content visible, not just the
// decision" posture `ubx why` already applies to an accepted proposal
// (renderProposal, why.go), rendered here for one that's ONLY been
// resolved -- nothing about it is accepted or signed yet, this is the
// review surface a human reads before ever running `ubx ship`.
//
// header is the caller-built "Plan  <stack> · from <source>"-shaped
// first line (docs/cli-output-spec.md's own worked plan example) --
// built by the caller, not derived here, since what counts as "source"
// differs per caller (an authoring document's path for `ubx plan
// --from-doc`/`--from-diagram`, an intent file's path for a hand-written
// one, nothing at all for `ubx terminate`, whose own address IS the
// spec) and `ubx ship`'s own confirmation header names a plan age
// instead of a source entirely.
//
// showDefaults is UBI-72's own [intent] show_defaults resolution
// (config.go's resolveShowDefaults) -- `ubx plan` passes its own resolved
// value; `ubx terminate`/`ubx promote` pass true unconditionally, since
// neither ever populates Intent.Assumptions/Defaults with real AI content
// (no LLM in either path) -- there's nothing for false to ever collapse
// there, so neither needs its own --show-defaults/--hide-defaults flags.
func renderPlanReceipt(out io.Writer, st *styler, p *core.Proposal, header string, showDefaults bool) {
	// docs/cli-output-spec.md §v2: "NO AI summary sentence under the
	// header (remove it)" -- the founder's own markup against the real
	// 5-resource platform.md case found this line pure noise once every
	// resource block below it already renders in full; p.Intent.Summary
	// still exists in the stored proposal (nothing here removes the
	// field), it's simply not rendered a second time.
	fmt.Fprintln(out, header)
	fmt.Fprintln(out)

	renderCreates(out, st, p.Delta.Creates, "  ")
	renderModifies(out, st, p.Delta.Modifies, "  ")
	renderDestroys(out, st, p.Delta.Destroys, "  ", true)
	if len(p.Delta.Creates) > 0 || len(p.Delta.Modifies) > 0 || len(p.Delta.Destroys) > 0 {
		fmt.Fprintln(out)
	}

	// docs/cli-output-spec.md §v2: every summary line bold, with one
	// empty line between the delta line and the blast-radius/cost block.
	// forceBold (not a naive nested st.Bold call, see its own doc
	// comment) keeps the create/modify/destroy counts individually
	// green/yellow/red while making the whole line bold throughout.
	fmt.Fprintln(out, st.forceBold(fmt.Sprintf("delta: %s, %s, %s",
		st.Green(fmt.Sprintf("+%d create(s)", len(p.Delta.Creates))),
		st.Yellow(fmt.Sprintf("~%d modify(ies)", len(p.Delta.Modifies))),
		st.Red(fmt.Sprintf("-%d destroy(s)", len(p.Delta.Destroys))))))
	fmt.Fprintln(out)
	fmt.Fprintln(out, st.forceBold(fmt.Sprintf("blast radius: %s %s %s",
		st.Green(fmt.Sprintf("+%d", p.BlastRadius.Creates)),
		st.Yellow(fmt.Sprintf("~%d", p.BlastRadius.Modifies)),
		st.Red(fmt.Sprintf("-%d", p.BlastRadius.Destroys)))))
	if len(p.CostDelta.MonthlyUSD) > 0 {
		fmt.Fprintln(out, st.Bold(fmt.Sprintf("cost delta: $%s/mo", p.CostDelta.MonthlyUSD)))
	}

	if len(p.Intent.Assumptions) == 0 && len(p.Intent.Defaults) == 0 && len(p.Intent.Questions) == 0 {
		return
	}
	fmt.Fprintln(out)
	renderAmbiguityStyled(out, st, p.Intent.Assumptions, p.Intent.Defaults, p.Intent.Questions, showDefaults)
}

// planReceiptHeader builds renderPlanReceipt's own "Plan  <stack> · from
// <source>" header line -- source is empty for a caller with no natural
// authoring-document source (a hand-written intent file passed
// positionally still names itself; `ubx terminate` passes "" since the
// address IS the spec, no file involved at all). The "from <source>"
// segment renders dim (docs/cli-output-spec.md §v2's own worked
// example) -- st is nil-safe (styler.Dim/color both tolerate a nil
// receiver), so callers that render header text through some other
// unstyled path are unaffected.
func planReceiptHeader(st *styler, stack, source string) string {
	if source == "" {
		return fmt.Sprintf("Plan  %s", stack)
	}
	return fmt.Sprintf("Plan  %s · %s", stack, st.Dim(fmt.Sprintf("from %s", source)))
}

// planFilePath is where `ubx plan` saves a resolved-but-unaccepted
// proposal, and where `ubx ship <hash>` looks for one when hash isn't
// already an accepted id in the ledger (ship.go's own inline-accept
// fallback) -- a local, hash-addressed store alongside the ledger's own
// .ubx/ directory (.ubx/salt, .ubx/lock), never part of the ledger itself:
// a plan is a draft, not a recorded decision, so it has no business living
// under ledger/. Keyed by content hash (docs/architecture.md's "Two-step
// fusion" amendment's own "hash-frozen" mental model) rather than any
// human-chosen name, so `ubx ship <hash>` needs nothing but the hash `ubx
// plan` already printed.
func planFilePath(ledgerDir, hash string) string {
	return filepath.Join(ledgerDir, ".ubx", "plans", hash+".json")
}

// writePlanFile saves a resolved proposal's already-marshaled JSON at its
// own content hash's canonical path, creating .ubx/plans/ if needed.
func writePlanFile(ledgerDir, hash string, data []byte) (string, error) {
	path := planFilePath(ledgerDir, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// readPlanFile reads back a plan `ubx plan` saved, for `ubx ship <hash>`'s
// own inline-accept fallback. A missing file is reported as-is (the
// caller decides how to present "no such plan or accepted proposal").
func readPlanFile(ledgerDir, hash string) (*core.Proposal, error) {
	data, err := os.ReadFile(planFilePath(ledgerDir, hash))
	if err != nil {
		return nil, err
	}
	var p core.Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plan file: %w", err)
	}
	return &p, nil
}

// ErrPlanNotFound and ErrPlanAmbiguous are resolvePlanHash's own sentinel
// outcomes (UBI-49 finding #6) -- distinct from the raw os.ReadFile error
// planFilePath/readPlanFile's exact-hash callers already return, so a
// caller like `ubx accept` can render a teaching error naming both "no
// such file" and "no such plan" without string-matching an OS error.
var (
	ErrPlanNotFound  = errors.New("no matching plan in the plan store")
	ErrPlanAmbiguous = errors.New("ambiguous plan hash prefix")
)

// resolvePlanHash resolves ref -- a full content hash, or any unique
// prefix of one (docs/cli-output-spec.md principle 3, "short-form input
// accepted wherever hashes are arguments") -- against the plan store at
// ledgerDir/.ubx/plans/. The exact-hash case is a single stat+read, no
// directory listing at all; a prefix falls back to scanning every file
// there. Returns the plan's own real full hash alongside its parsed
// proposal, since a caller (ship.go's acceptPlanInline, accept.go's own
// fallback) needs the real hash for its own integrity check and for
// whatever it records, not just whatever ref the user happened to type.
func resolvePlanHash(ledgerDir, ref string) (fullHash string, p *core.Proposal, err error) {
	if exact, err := readPlanFile(ledgerDir, ref); err == nil {
		return ref, exact, nil
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}

	dir := filepath.Join(ledgerDir, ".ubx", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%s: %w", ref, ErrPlanNotFound)
		}
		return "", nil, err
	}

	var matches []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if strings.HasPrefix(name, ref) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf("%s: %w", ref, ErrPlanNotFound)
	case 1:
		p, err := readPlanFile(ledgerDir, matches[0])
		if err != nil {
			return "", nil, err
		}
		return matches[0], p, nil
	default:
		sort.Strings(matches)
		return "", nil, fmt.Errorf("%s: %w (matches %s)", ref, ErrPlanAmbiguous, strings.Join(matches, ", "))
	}
}

// ErrProposalAmbiguous is resolveAcceptedProposal's own sentinel for a
// short hash prefix matching more than one already-accepted ledger
// proposal -- the ledger-side counterpart to ErrPlanAmbiguous above. A
// prefix matching NOTHING in the ledger is core.ErrProposalNotFound
// (ledger.Read's own sentinel, reused rather than duplicated) so a
// caller like ship.go's RunE can fall through to the plan store exactly
// as before this existed, on the same condition it already checked.
var ErrProposalAmbiguous = errors.New("ambiguous proposal hash prefix")

// resolveAcceptedProposal resolves ref -- a full proposal ID, or any
// unique prefix of one (docs/cli-output-spec.md principle 3, "short-form
// input accepted wherever hashes are arguments") -- against ledger's own
// already-accepted proposals (ledger.Chain), mirroring resolvePlanHash's
// own exact-then-prefix resolution for the plan store.
//
// UBI-63 session 5: a real, live divergence found blocking the founder's
// own cleanup -- `ubx ship <short-hash>` on an already-accepted destroy
// proposal refused with "no matching plan in the plan store," even
// though ship's own doc comment already promises "looked up two ways, in
// order: first as an already-accepted proposal id... if not found there,
// as a plan." Root cause: ledger.Read only ever did an exact-ID lookup,
// so a short hash that resolved fine against the plan store (which
// already had prefix matching) found nothing in the ledger and never got
// a chance to. The exact-hash case here is still a single store read,
// unchanged from before this existed -- prefix matching only walks the
// chain when that fails, the same "cheap path first" posture
// resolvePlanHash already has.
func resolveAcceptedProposal(ledger *core.Ledger, ref string) (*core.Proposal, error) {
	if p, err := ledger.Read(ref); err == nil {
		return p, nil
	} else if !errors.Is(err, core.ErrProposalNotFound) {
		return nil, err
	}

	chain, err := ledger.Chain()
	if err != nil {
		return nil, err
	}
	var matches []*core.Proposal
	for _, p := range chain {
		if strings.HasPrefix(p.ID, ref) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("proposal %s: %w", ref, core.ErrProposalNotFound)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%s: %w (matches %s)", ref, ErrProposalAmbiguous, strings.Join(ids, ", "))
	}
}
