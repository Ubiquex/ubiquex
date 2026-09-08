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

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/hclstack"
)

// newPlanCmd is UBI-49's own terraform-shaped fusion of propose+resolve+
// preview into one command (docs/architecture.md's "Two-step fusion"
// amendment): takes either input this codebase now knows how to turn
// into an ubx:intent/v1 document (a hand-written intent file, or
// --from-code's SDK program), resolves it through the exact same,
// unmodified core/resolver.Resolve every other entry point already uses,
// renders the full receipt (delta, cost_delta, blast radius, assumptions/
// questions) for a human to review right here, and saves the result as a
// hash-addressed local plan file `ubx ship <hash>` can later pick up for
// inline local-tier acceptance. Never touches the ledger itself -- exactly
// like `ubx resolve` and `ubx propose` today, this is preview-only; only
// `ubx accept`/`ubx ship` ever record anything.
//
// UBI-224 removed this command's own --from-doc and --from-diagram
// modes along with the markdown and diagram authoring mediums: both
// used to draft an intent/v1 document from a real authoring input
// before resolving it here in the same command, the same two real
// draft producers `ubx propose` itself used to expose separately.
// --from-code has no draft step to remove: an SDK program has no
// ambiguity to review before resolving, by construction.
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
		fullHashes       bool
		showDefaultsFlag bool
		hideDefaultsFlag bool
	)

	cmd := &cobra.Command{
		Use:   "plan [intent-file]",
		Short: "Resolve an intent file, an SDK program, or a .ubx.hcl blueprint-calling file into a draft proposal, render its full receipt, and save it as a hash-addressed plan for `ubx ship`",
		Long: `Fuses "ubx propose" + "ubx resolve" + a preview render into one command -- the
terraform-shaped, two-step half of this project's own workflow (plan, then "ubx ship <hash>").

One file argument, dispatched by its own extension: an ubx:intent/v1 file, a
TypeScript, Go or Python SDK program (.ts/.go/.py) evaluated through the same evaluator
"ubx resolve" uses, or a .ubx.hcl blueprint-calling file, which is parsed rather than
evaluated so no code runs.

Bare "ubx plan" with no argument finds the entry file itself: stack.ts, stack.go,
stack.py or stack.ubx.hcl if one is there, otherwise the only SDK program in the
directory. Two conventional entries in different media are refused rather than ranked,
since one evaluates code and the other only parses.

The result resolves through the identical, unmodified core/resolver.Resolve every other entry
point already uses -- same invariants, same orphan/pin checks, same failure modes. Its full
receipt (delta, cost_delta, blast radius, assumptions/defaults/questions) renders to the
terminal for review, and the resolved-but-unaccepted proposal is written to
.ubx/plans/<hash>.json, keyed by the exact hash "ubx ship <hash>" will later look for.

This command never touches a ledger -- nothing here is accepted or shipped. Run "ubx ship
<hash>" to accept (local tier) and ship in one step, or run "ubx accept"/"ubx propose" by
hand against the written plan file for the four-verb ceremony (PR-merge signing, a separate
propose-time PR trailer hash, etc.).`,
		Args: cobra.MaximumNArgs(1),
		// plan has no "finding" concept, the same audit outcome as
		// propose/resolve (UBI-20 exit-code contract): it either resolves
		// or it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A positional SDK program needs no flag: `ubx plan stack.ts`.
			// Promoted before the mutual-exclusion check below, so passing
			// both an argument and --from-code still errors rather than
			// silently preferring one.
			if fromCode == "" && len(args) == 1 && sdkEntryFile(args[0], true) {
				fromCode = args[0]
				args = nil
			}

			modes := len(args)
			if fromCode != "" {
				modes++
			}
			if modes > 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("plan: pass one file argument, not both a positional file and --from-code")}
			}
			if modes == 0 {
				candidates, derr := autodetectMedium(ledgerDir)
				if derr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: no file argument given and the directory could not be searched for one: %w", derr)}
				}
				// A conventional name wins outright, so a directory can hold
				// more than one SDK program without bare `ubx plan` becoming
				// unusable. Deliberately NOT directory merging the way
				// Terraform concatenates every .tf file: these languages
				// already have imports, so a stack that spans files says so
				// itself, merging has no sane cross-language semantics, and
				// one entry file is what makes the provenance content hash
				// mean anything at all.
				entry, conflicting, ok := conventionalEntry(candidates)
				switch {
				case ok:
					fromCode = entry
				case len(conflicting) > 1:
					// Deliberately not the multiple-programs message. That
					// one reads as "you left two files lying around"; this
					// is a different situation and the error should say so,
					// because the fix is a decision about which medium the
					// stack is authored in, not tidying up.
					return &ExitCodeError{Code: 2, Err: fmt.Errorf(
						"plan: %s each name this stack's entry point, in different authoring media -- ubx will not choose between them, since one evaluates code and the other only parses; keep the one this stack is authored in, or name the file explicitly",
						strings.Join(conflicting, " and "))}
				default:
					switch len(candidates) {
					case 1:
						fromCode = candidates[0].path
					case 0:
						return &ExitCodeError{Code: 2, Err: errors.New("plan: no SDK program found here -- write stack.ts (or stack.go, stack.py) and run `ubx plan` again, or name a file explicitly")}
					default:
						names := make([]string, len(candidates))
						hints := make([]string, len(candidates))
						for i, c := range candidates {
							names[i] = c.path
							hints[i] = fmt.Sprintf("ubx plan %s", c.path)
						}
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: multiple SDK programs found: %s -- pick one with `ubx plan <file>`, or name one of them %s: %s", strings.Join(names, ", "), conventionalEntryNames(), strings.Join(hints, " | "))}
					}
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
			switch {
			case isHCLStackFile(fromCode):
				// A .ubx.hcl file is parsed, never evaluated: no code runs,
				// so there is no receipts/blueprintRefs output and nothing
				// to stamp. The identical branch `ubx resolve` has always
				// had (UBI-226), shared here rather than reimplemented.
				//
				// plan refused this medium outright until now, which made
				// the front door the one command that could not plan a
				// whole authoring medium. Widening it is the same asymmetry
				// the positional form fixed.
				parsed, err := hclstack.Parse(fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
				intent = *parsed
				sourceLabel = fromCode
			case fromCode != "":
				// blueprintRefs (UBI-126) is deliberately unused here --
				// `ubx plan --from-code` has never wired blueprint
				// direct-call provenance stamping in for ANY language (a
				// real, pre-existing gap distinct from this ticket's own
				// scope, predating it for Go too); not fixed in this
				// session, named rather than silently perpetuated further.
				canon, receipts, _, err := evaluateSDKProgram(ctx, fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
				}
				// UBI-130: see cli/resolve.go's own identical comment --
				// every blueprint dependency a Python program's own
				// requirements.txt declared was already pulled+verified
				// before evaluateSDKProgram ran the script; print its
				// receipt line(s) now, before planning proceeds.
				for _, r := range receipts {
					fmt.Fprintln(outWriter, r)
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
				if looksLikeHCL(args[0], data) {
					// Reaching the intent-file reader with HCL used to
					// report "invalid character 's' looking for beginning of
					// value", a JSON parse error about a file that was never
					// JSON. It named the wrong problem entirely.
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %s looks like HCL but is not named *.ubx.hcl -- a blueprint-calling file has to carry that exact suffix to be parsed as one, and anything else is read as an ubx:intent/v1 JSON document", args[0])}
				}
				if err := json.Unmarshal(data, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: parse intent file: %w", err)}
				}
				sourceLabel = args[0]
			}

			// UBI-86: cli/resolve.go's own identical pair of calls,
			// mirrored here so the override round trip works via
			// `ubx plan`, not only `ubx resolve`.
			if err := blueprint.ExpandCalls(ctx, &intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			if err := blueprint.ApplyOverrides(&intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, intent.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
			}
			defer closeLedger()

			p, err := resolver.Resolve(ledger, providers, &intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("plan: %w", err)}
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
				st.GreenBold(fmt.Sprintf("next: %s", nextShipHint([]string{hash}, p.BlastRadius.Destroys > 0))))
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/ -- also where the plan is saved, at .ubx/plans/<hash>.json")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for provider/schema acquisition and SDK program evaluation -- one shared budget for the whole command")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil,
		"ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	cmd.Flags().StringVar(&fromCode, "from-code", "", "evaluate a TypeScript (@ubx/sdk), Go (ubx-sdk-go), or Python (ubx_sdk) SDK program, dispatched by extension, instead of reading an intent file")
	// --from-code is kept, hidden, as an alias for the positional form.
	// It distinguishes nothing since UBI-224 removed the other authoring
	// mediums, but it is spelled out across the tutorials, in `ubx
	// promote`'s own teaching errors, and in this command's own
	// multiple-candidate hint, so removing it outright would break
	// working invocations for no gain.
	_ = cmd.Flags().MarkHidden("from-code")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
	cmd.Flags().BoolVar(&showDefaultsFlag, "show-defaults", false, "render the full \"AI defaults\" block regardless of [intent] show_defaults (mutually exclusive with --hide-defaults)")
	cmd.Flags().BoolVar(&hideDefaultsFlag, "hide-defaults", false, "collapse the \"AI defaults\" block to a one-line count regardless of [intent] show_defaults (mutually exclusive with --show-defaults) -- full detail is always in the saved plan file and the signed proposal either way")
	return cmd
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

// detectedMedium is one SDK program candidate autodetectMedium found --
// used both to auto-plan a lone candidate and to build the "pick one"
// teaching error naming each candidate's own path.
type detectedMedium struct {
	path string
}

// autodetectMedium implements docs/cli-output-spec.md §v2's own bare
// "ubx plan" auto-detection: exactly one SDK program in dir plans
// automatically, no --from-code flag needed; multiple candidates are
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
		if isHCLStackFile(name) {
			// No content sniffing here, unlike the three SDK languages. A
			// .ubx.hcl file carries no import to look for -- it is parsed,
			// not run -- and the double extension is itself the marker: no
			// file is named .ubx.hcl by accident the way a stray .go file
			// lands in a Go module.
			found = append(found, detectedMedium{path: path})
			continue
		}
		switch ext := strings.ToLower(filepath.Ext(name)); ext {
		case ".ts", ".go", ".py":
			marker := sdkImportMarkers[ext]
			content, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(content), marker) {
				continue
			}
			found = append(found, detectedMedium{path: path})
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
// differs per caller (an SDK program's path for `ubx plan --from-code`,
// an intent file's path for a hand-written one, nothing at all for
// `ubx terminate`, whose own address IS the spec) and `ubx ship`'s own
// confirmation header names a plan age instead of a source entirely.
//
// showDefaults is UBI-72's own [intent] show_defaults resolution
// (config.go's resolveShowDefaults) -- `ubx plan` passes its own resolved
// value; `ubx terminate`/`ubx promote` pass true unconditionally, since
// neither ever populates Intent.Assumptions/Defaults with real AI content
// (no LLM in either path) -- there's nothing for false to ever collapse
// there, so neither needs its own --show-defaults/--hide-defaults flags.
func renderPlanReceipt(out io.Writer, st *styler, p *core.Proposal, header string, showDefaults bool) {
	// UBI-251: the summary sentence is back, under the header, but only
	// where it carries authored or AI-derived content. v2 removed it as
	// noise against a case where it paraphrased the resource list;
	// authoredSummary (cli/intentrender.go) documents why that was right
	// about that case and wrong as a rule, and why the gate is on source
	// kind rather than proposal kind.
	fmt.Fprintln(out, header)
	if summary := authoredSummary(p.Intent); summary != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, summary)
	}
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
	// comment) keeps the create/change/terminate counts individually
	// green/yellow/red while making the whole line bold throughout.
	// UBI-88: "change(s)"/"terminate(s)", not "modify(ies)"/"destroy(s)" --
	// matching the change/terminate vocabulary the op headers above
	// already use (renderModifies' "~ <address> change", renderDestroys'
	// "- <address> destroy" -- word ORDER now matches, the op word itself
	// stays "destroy", a deliberately scoped decision, not an oversight).
	fmt.Fprintln(out, st.forceBold("delta: "+deltaCounts(st,
		int64(len(p.Delta.Creates)), int64(len(p.Delta.Modifies)), int64(len(p.Delta.Destroys)))))
	fmt.Fprintln(out)
	fmt.Fprintln(out, st.forceBold(fmt.Sprintf("blast radius: %s %s %s",
		st.Green(fmt.Sprintf("+%d", p.BlastRadius.Creates)),
		st.Yellow(fmt.Sprintf("~%d", p.BlastRadius.Modifies)),
		st.Red(fmt.Sprintf("-%d", p.BlastRadius.Destroys)))))
	// UBI-251, interim: render nothing rather than $0/mo.
	//
	// The field exists and this line has always rendered, but every writer
	// sets a literal 0 (core/scan.go twice, core/resolver/resolver.go,
	// conformance/destroy_probe.go) because there is no pricing source
	// anywhere in the tree. The old guard was len(...) > 0, which never
	// suppressed anything: json.RawMessage("0") has length 1.
	//
	// A visible "$0/mo" reads as free. That is a stronger and more wrong
	// claim than saying nothing, since a reader has no way to tell it
	// apart from a real zero. The line comes back when a pricing source
	// does; the scope of that arc is recorded on UBI-251.
	if isPricedCostDelta(p.CostDelta) {
		fmt.Fprintln(out, st.Bold(fmt.Sprintf("cost delta: $%s/mo", p.CostDelta.MonthlyUSD)))
	}
	renderPinnedHeads(out, st, p.Resolution.Inputs)

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

// conventionalEntryBase is the file name, without extension, that bare
// `ubx plan` prefers over any other SDK program in the directory.
//
// One conventional entry point per language, "stack.ts"/"stack.go"/
// "stack.py", so a directory holding more than one SDK program is still
// plannable without naming a file every time. Terraform's own answer to
// the same question is to merge every .tf file in the directory; this is
// deliberately not that. These languages already have imports, so a
// stack spanning several files expresses that itself, in its own
// language, checked by its own compiler. Merging would also have no
// coherent cross-language meaning, and it would break the one thing that
// makes an SDK-authored proposal auditable: intent.sources stamps ONE
// entry file's content hash, and a hash over a set of files the tool
// happened to concatenate proves much less than a hash over the file the
// author actually wrote.
const conventionalEntryBase = "stack"

// conventionalEntrySuffixes are the exact file names bare `ubx plan`
// prefers, one per authoring medium.
//
// ".ubx.hcl" is why this is a suffix list rather than a base name plus
// filepath.Ext. It is a DOUBLE extension, so TrimSuffix(name, Ext(name))
// yields "stack.ubx", not "stack", and the original check silently
// failed to match the one medium it was later asked to cover.
var conventionalEntrySuffixes = []string{
	conventionalEntryBase + ".ts",
	conventionalEntryBase + ".go",
	conventionalEntryBase + ".py",
	conventionalEntryBase + ".ubx.hcl",
}

// isConventionalEntry reports whether path's base name is one of them.
func isConventionalEntry(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, s := range conventionalEntrySuffixes {
		if base == s {
			return true
		}
	}
	return false
}

// isHCLStackFile reports whether path is a .ubx.hcl blueprint-calling
// file, the one medium that is parsed rather than evaluated.
func isHCLStackFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".ubx.hcl")
}

// looksLikeHCL is a last-resort check for a file that reached the
// intent-file reader and is plainly not JSON. Deliberately narrow: an
// intent/v1 document always starts with "{", so anything with a .hcl
// extension or an HCL-shaped first token is worth naming rather than
// letting a JSON decoder describe.
func looksLikeHCL(path string, data []byte) bool {
	if strings.HasSuffix(strings.ToLower(path), ".hcl") {
		return true
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || strings.HasPrefix(trimmed, "{") {
		return false
	}
	return strings.HasPrefix(trimmed, "stack ") || strings.HasPrefix(trimmed, "stack=") ||
		strings.HasPrefix(trimmed, "blueprint ")
}

// conventionalEntry picks the conventional entry file out of candidates,
// if exactly one is present. More than one (stack.ts AND stack.go in the
// same directory) is a genuine ambiguity and falls through to the normal
// multiple-candidates error rather than picking a language for the
// author.
func conventionalEntry(candidates []detectedMedium) (string, []string, bool) {
	var conventional []string
	for _, c := range candidates {
		if isConventionalEntry(c.path) {
			conventional = append(conventional, c.path)
		}
	}
	switch len(conventional) {
	case 1:
		return conventional[0], nil, true
	case 0:
		return "", nil, false
	default:
		// More than one conventional entry is a genuine ambiguity and gets
		// refused, never resolved by precedence. stack.ts and
		// stack.ubx.hcl are not two spellings of one stack: they are two
		// different authoring media, and one of them runs code while the
		// other only parses. Any fixed precedence would mean a user who
		// adds a second file silently changes which one ships.
		sort.Strings(conventional)
		return "", conventional, false
	}
}

// conventionalEntryNames renders the conventional names for a teaching
// error, in a stable order.
func conventionalEntryNames() string {
	return strings.Join(conventionalEntrySuffixes, "/")
}

// sdkEntryFile reports whether path names an authoring program rather
// than a pre-resolved intent/v1 document, by extension, using exactly
// the dispatch --from-code already performed.
//
// This is what lets `ubx plan stack.ts` work without a flag. --from-code
// existed to tell an SDK program apart from the markdown, diagram and
// chat mediums, and UBI-224 removed all three, so from then on it
// distinguished nothing: every non-intent-file input was an SDK program.
// A flag whose only job is to say "this argument is the kind of argument
// it obviously is" is a flag worth not typing.
//
// allowHCL follows each command's own existing contract rather than
// unifying them behind this change's back: `ubx resolve --from-code`
// has always accepted a .ubx.hcl blueprint-calling file, and `ubx plan
// --from-code` has always rejected one. Widening plan's accepted set is
// a real behaviour change with its own argument to make, not a
// side effect of dropping a flag.
func sdkEntryFile(path string, allowHCL bool) bool {
	lower := strings.ToLower(path)
	if allowHCL && strings.HasSuffix(lower, ".ubx.hcl") {
		return true
	}
	switch filepath.Ext(lower) {
	case ".ts", ".go", ".py":
		return true
	}
	return false
}
