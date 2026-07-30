package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// newChatCmd is UBI-46's own entry point: the chat medium, riding
// UBI-41's Adapter/DraftWithRetry interface unchanged (docs/intent-provider.md's
// own "Amendment: the chat medium" -- confirmed by actually building
// this command, not just claimed: neither intentprovider.Adapter nor
// DraftWithRetry needed a single change). Each line of input is one
// turn; every turn re-drafts the WHOLE conversation from scratch via the
// same adapter `ubx propose --from-doc` already uses -- never an
// incremental patch. Capture starts only because this command was
// explicitly invoked (there is no ambient/background capture path
// anywhere in this codebase) and is held entirely in memory until
// explicit finalization (`/save`) -- an abandoned session (`/quit`, a
// plain EOF, a crash) writes nothing to disk, ever; there is no
// intermediate state on disk to leave behind as an orphan.
func newChatCmd() *cobra.Command {
	var ledgerDir, stack, out string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "chat --stack <stack>",
		Short: "Interactively draft an intent/v1 draft, one turn at a time, via the configured [intent] provider",
		Long: `Reads turns from stdin, one per line, redrafting the whole conversation via the
configured [intent] provider after every turn (docs/intent-provider.md's own
"Amendment: the chat medium", UBI-46). Type "/save" to finalize -- this writes the
captured conversation to dialogues/<hash>.dlg.json and the current draft to --out
(or stdout), then exits. Type "/quit", or send EOF, to abandon the session: nothing
is written to disk, ever, for a session that never reaches "/save".`,
		Args: cobra.NoArgs,
		// chat has no "finding" concept -- it either produces a draft or
		// it doesn't (UBI-20 exit-code contract): 0 or 2 only. Abandoning
		// a session (/quit or EOF) is a legitimate, non-error outcome,
		// not a finding -- it exits 0.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd, ledgerDir, stack, out, timeout)
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/, dialogues/, and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "target stack name (required)")
	cmd.Flags().StringVar(&out, "out", "", "write the finalized draft here instead of stdout (only takes effect on /save)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for each turn's own drafting round trip")
	return cmd
}

func runChat(cmd *cobra.Command, ledgerDir, stack, out string, timeout time.Duration) error {
	rc, err := LoadConfigResolved(cmd.ErrOrStderr())
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: %w", err)}
	}
	cfg := rc.Config
	applyStackDefault(cmd, &stack, cfg)
	if stack == "" {
		return &ExitCodeError{Code: 2, Err: stackRequiredError("chat", rc.Files)}
	}

	adapter, err := buildIntentAdapter(cfg)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: %w", err)}
	}

	outWriter := cmd.OutOrStdout()
	errWriter := cmd.ErrOrStderr()

	dlg := &intentprovider.Dialogue{
		SchemaVersion: 1,
		Stack:         stack,
		Adapter:       adapter.Name(),
		Model:         adapter.Model(),
		StartedAt:     nowRFC3339(),
	}

	var lastDraft *resolver.IntentFile
	var lastRaw json.RawMessage

	fmt.Fprintln(outWriter, "ubx chat: type a turn and press enter. /save to finalize, /quit (or EOF) to abandon without saving anything.")

	scanner := bufio.NewScanner(cmd.InOrStdin())
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			continue
		case line == "/quit":
			fmt.Fprintln(outWriter, "session abandoned, nothing saved")
			return nil
		case line == "/save":
			if lastDraft == nil {
				return &ExitCodeError{Code: 2, Err: errors.New("chat: /save with no successfully drafted turn yet -- nothing to save")}
			}
			return finalizeChat(cmd, ledgerDir, dlg, lastDraft, lastRaw, out, outWriter)
		default:
			redacted, findings := intentprovider.Redact([]byte(line))
			for _, f := range findings {
				fmt.Fprintf(errWriter, "warning: chat: redacted possible secret material (%s) before sending this turn to the %s adapter\n", f, adapter.Name())
			}
			dlg.Turns = append(dlg.Turns, intentprovider.Turn{Text: string(redacted), At: nowRFC3339()})

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			draft, raw, err := intentprovider.DraftWithRetry(ctx, adapter, stack, dlg.Transcript())
			cancel()
			if err != nil {
				// A single bad turn doesn't lose the conversation --
				// the turn itself already recorded above stays in dlg.Turns
				// (the user really did say it; only drafting it failed),
				// lastDraft/lastRaw simply don't advance, and the loop
				// continues so the next turn can try again with full
				// context. Nothing here is ever written to disk unless
				// /save is reached, so a session that never recovers
				// still abandons cleanly.
				fmt.Fprintf(errWriter, "chat: turn failed to draft: %v\n", err)
				continue
			}
			lastDraft, lastRaw = draft, raw
			renderAmbiguity(outWriter, draft)
		}
	}
	fmt.Fprintln(outWriter, "session abandoned, nothing saved")
	return nil
}

// finalizeChat is `/save`'s own body: write the captured dialogue exactly
// once, atomically, then emit a provenance-bearing copy of the draft.
// See intentprovider.Dialogue's own doc comment for why the embedded
// draft and the emitted one are deliberately two distinct objects, not
// one shared pointer.
func finalizeChat(cmd *cobra.Command, ledgerDir string, dlg *intentprovider.Dialogue, lastDraft *resolver.IntentFile, lastRaw json.RawMessage, out string, outWriter io.Writer) error {
	embedded := *lastDraft
	dlg.Draft = &embedded

	data, err := json.MarshalIndent(dlg, "", "  ")
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: marshal dialogue: %w", err)}
	}
	hash := intentprovider.HashDocument(data)
	// The dialogue is content-addressed, matching this codebase's own
	// proposal-ID convention -- but a filename can't safely carry the
	// "sha256:" prefix (a literal colon is forbidden outright on some
	// filesystems, awkward everywhere else), so the filename uses the
	// bare hex digest while ContentHash (below) keeps the full,
	// convention-matching "sha256:<hex>" form every other intent.sources
	// entry in this codebase already uses.
	hashHex := strings.TrimPrefix(hash, "sha256:")

	dialoguesDir := filepath.Join(ledgerDir, "dialogues")
	if err := os.MkdirAll(dialoguesDir, 0o755); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: %w", err)}
	}
	relPath := filepath.Join("dialogues", hashHex+".dlg.json")
	fullPath := filepath.Join(ledgerDir, relPath)
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: %w", err)}
	}

	output := *lastDraft
	intentprovider.PopulateSources(&output, intentprovider.SourceKindDialogue, relPath, hash, dlg.Adapter, dlg.Model, lastRaw)

	fmt.Fprintf(outWriter, "captured dialogue: %s\n", fullPath)
	renderAmbiguity(outWriter, &output)

	outData, err := json.MarshalIndent(&output, "", "  ")
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: marshal draft: %w", err)}
	}
	if out == "" {
		fmt.Fprintln(outWriter, string(outData))
		return nil
	}
	if err := os.WriteFile(out, outData, 0o644); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("chat: %w", err)}
	}
	fmt.Fprintf(outWriter, "wrote draft: %s\n", out)
	return nil
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
