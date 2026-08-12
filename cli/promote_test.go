package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// promoteFakeWidgetDraft is a fakeIntentAdapter draft producing one
// fake_widget create -- fakeprovider's own known-good schema (plan_test.go's
// own convention), reused here so promote's own re-derivation exercises a
// real resolver.Resolve/fakeprovider round trip, not a hand-rolled shortcut.
const promoteFakeWidgetDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "widget, modeled on staging"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\",\"tags\":{\"env\":\"prod\"}}"}
  ],
  "destroys": []
}`

// appendAcceptedProposal directly appends an already-"accepted" proposal to
// ledgerDir's own ledger -- the same shortcut cli/status_test.go's own
// TestStatus_Drift_MissingLookup_Unreadable_ExitTwo uses to build a ledger
// fixture without running the full accept ceremony, since promote's own
// adversarial tests care about intent.sources shape, not how a proposal
// came to be accepted.
func appendAcceptedProposal(t *testing.T, ledgerDir, id, stack string, sources []core.IntentSource) *core.Proposal {
	t.Helper()
	ledger := core.Open(ledgerDir)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            id,
		Stack:         stack,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "test fixture", Sources: sources},
		Acceptance:    &core.Acceptance{Method: "local", Approvers: []string{"test"}, AcceptedAt: "2026-07-30T00:00:00Z"},
		Status:        core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatalf("append fixture proposal: %v", err)
	}
	return p
}

func TestPromote_HappyPath_ReResolvesAgainstTarget(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	docPath := filepath.Join(sourceDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget, like staging.")

	sourceID := strings.Repeat("a", 64)
	appendAcceptedProposal(t, sourceDir, sourceID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
		{Kind: "intent_provider", Ref: "fake:fake-model-v1", ContentHash: "sha256:beadfeed"},
	})

	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "promote", sourceID,
		"--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx promote: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "promoted "+displayHash(sourceID, false)+" -> payments") {
		t.Errorf("expected a promoted-header line, got:\n%s", out)
	}
	if !strings.Contains(out, "delta: +1 create(s)") {
		t.Errorf("expected the target's own fresh resolve delta, got:\n%s", out)
	}

	hash := mustExtractPlanHash(t, targetDir, out)
	planFile := filepath.Join(targetDir, ".ubx", "plans", hash+".json")
	if _, err := os.Stat(planFile); err != nil {
		t.Fatalf("expected a saved plan file at %s: %v", planFile, err)
	}

	np, err := readPlanFile(targetDir, hash)
	if err != nil {
		t.Fatalf("read saved plan: %v", err)
	}
	if !hasPromotionSourceRef(np, sourceID) {
		t.Fatalf("expected a promotion source referencing %s, got sources: %+v", sourceID, np.Intent.Sources)
	}
	var promo *core.IntentSource
	for i := range np.Intent.Sources {
		if np.Intent.Sources[i].Kind == "promotion" {
			promo = &np.Intent.Sources[i]
		}
	}
	if promo == nil || promo.Base != sourceDir {
		t.Errorf("promotion source's base = %+v, want %q (the source's own --ledger-dir, git-local has no other stack-base concept)", promo, sourceDir)
	}
	// The fresh re-resolution's own document/intent_provider sources must
	// also be present -- promotion is additive, never a replacement.
	var hasDoc, hasProvider bool
	for _, s := range np.Intent.Sources {
		hasDoc = hasDoc || s.Kind == "document"
		hasProvider = hasProvider || s.Kind == "intent_provider"
	}
	if !hasDoc || !hasProvider {
		t.Errorf("expected fresh document/intent_provider sources from re-resolution, got: %+v", np.Intent.Sources)
	}
}

func TestPromote_SourceProposalNotFound(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	_, err := runUbx(t, nil, "promote", strings.Repeat("f", 64), "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected an error for a nonexistent source proposal id")
	}
	if !strings.Contains(err.Error(), "proposal not found") {
		t.Errorf("error = %v, want it to name \"proposal not found\"", err)
	}
}

func TestPromote_SourceIsUnacceptedPlanDraft_Refused(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	intentPath := filepath.Join(sourceDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})

	planOut, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", sourceDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, sourceDir, planOut)

	_, err = runUbx(t, nil, "promote", hash, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse an unaccepted plan-store draft")
	}
	if !strings.Contains(err.Error(), "unaccepted \"ubx plan\" draft") {
		t.Errorf("error = %v, want it to name the unaccepted-draft reason", err)
	}
}

func TestPromote_NoReResolvableSource_Refused(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("b", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", nil)

	_, err := runUbx(t, nil, "promote", id, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse a proposal with no document/dialogue source")
	}
	if !strings.Contains(err.Error(), "no re-resolvable authoring source") {
		t.Errorf("error = %v, want it to name the missing-source reason", err)
	}
}

// TestPromote_DialogueSource_ReResolvesAgainstTarget is UBI-60's own
// dialogue happy-path proof, replacing the UBI-55 refusal this exact
// scenario used to hit. Builds a GENUINE .dlg.json the same way a real
// user would: a real "ubx chat" session (runUbxWithStdin, a fake
// adapter, a real /save), producing a real captured dialogue file
// through finalizeChat itself -- never a hand-marshaled Dialogue struct
// standing in for one -- then resolves+accepts the emitted draft for
// real, giving promote a real accepted proposal with a real dialogue
// source to work from.
func TestPromote_DialogueSource_ReResolvesAgainstTarget(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})

	draftPath := filepath.Join(t.TempDir(), "draft.json")
	chatOut, err := runUbxWithStdin(t, "We need a widget for payments.\n/save\n",
		"chat", "--stack", "payments", "--ledger-dir", sourceDir, "--out", draftPath)
	if err != nil {
		t.Fatalf("ubx chat: %v\noutput: %s", err, chatOut)
	}

	dialogueFiles, err := os.ReadDir(filepath.Join(sourceDir, "dialogues"))
	if err != nil {
		t.Fatalf("read dialogues/: %v", err)
	}
	if len(dialogueFiles) != 1 {
		t.Fatalf("dialogues/ = %v, want exactly one real .dlg.json file", dialogueFiles)
	}

	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", draftPath, "--provider", fakeProviderBinary, "--ledger-dir", sourceDir, "--out", resolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve (chat draft): %v\noutput: %s", err, resolveOut)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", sourceDir)
	if err != nil {
		t.Fatalf("ubx accept (chat draft): %v\noutput: %s", err, acceptOut)
	}
	sourceID := mustExtractID(t, acceptOut)

	out, err := runUbx(t, env, "promote", sourceID, "--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx promote (dialogue source): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "delta: +1 create(s)") {
		t.Errorf("expected the converged draft's own real fake_widget create, got:\n%s", out)
	}

	hash := mustExtractPlanHash(t, targetDir, out)
	np, err := readPlanFile(targetDir, hash)
	if err != nil {
		t.Fatalf("read saved plan: %v", err)
	}
	if len(np.Delta.Creates) != 1 || !strings.Contains(string(np.Delta.Creates[0]), `"widget1"`) {
		t.Fatalf("expected the real widget1 resource the converged dialogue draft declares, got: %+v", np.Delta.Creates)
	}

	// The dialogue + intent_provider sources are carried forward as
	// PINNED EVIDENCE from the original accepted proposal -- never
	// re-run, never dropped.
	var hasDialogue, hasIntentProvider bool
	for _, s := range np.Intent.Sources {
		hasDialogue = hasDialogue || s.Kind == "dialogue"
		hasIntentProvider = hasIntentProvider || s.Kind == "intent_provider"
	}
	if !hasDialogue || !hasIntentProvider {
		t.Errorf("expected the dialogue+intent_provider sources carried forward as pinned evidence, got: %+v", np.Intent.Sources)
	}
	if !hasPromotionSourceRef(np, sourceID) {
		t.Fatalf("expected a promotion source referencing %s, got sources: %+v", sourceID, np.Intent.Sources)
	}
}

// sdkResolveTSEntryPath and sdkResolveTSContentHash name the real, real
// existing TS SDK fixture resolve_from_code_test.go's own
// TestResolveFromCode_SimpleCreate already exercises
// (testdata/sdk_resolve/create_widget.ts) -- reused directly here rather
// than a second fixture file, matching this project's own "one real
// fixture, many tests" convention. The hash is the file's own real
// sha256, confirmed directly (shasum -a 256), not invented.
const (
	sdkResolveTSEntryPath   = "testdata/sdk_resolve/create_widget.ts"
	sdkResolveTSContentHash = "sha256:8452a657167972c2242293c8341cd20d0409feec7e67c9f463567f5a9033f065"
)

// TestPromote_SDKAuthoredSource_ReResolvesAgainstTarget is UBI-60's own
// SDK happy-path proof, replacing the UBI-55 refusal this exact scenario
// used to hit: a real, unchanged TS SDK program (its own real
// content_hash matching what the source proposal recorded) is re-run
// through the real tseval evaluator -- the identical mechanism `ubx
// resolve --from-code` already uses -- against the target, producing a
// real, fresh resolved proposal, not a copy of anything the source ever
// had (the source's own accepted proposal here carries a fixture, empty
// delta; the target's own real resolve is what actually creates the
// widget).
func TestPromote_SDKAuthoredSource_ReResolvesAgainstTarget(t *testing.T) {
	requireDeno(t)
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("d", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", []core.IntentSource{
		{Kind: "document", Ref: sdkResolveTSEntryPath, ContentHash: sdkResolveTSContentHash},
	})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "promote", id,
		"--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx promote (SDK source): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "delta: +1 create(s)") {
		t.Errorf("expected a real 1-create delta from actually re-running the program, got:\n%s", out)
	}

	hash := mustExtractPlanHash(t, targetDir, out)
	np, err := readPlanFile(targetDir, hash)
	if err != nil {
		t.Fatalf("read saved plan: %v", err)
	}
	if len(np.Delta.Creates) != 1 || !strings.Contains(string(np.Delta.Creates[0]), `"widget1"`) {
		t.Fatalf("expected the real widget1 resource the TS program actually declares, got: %+v", np.Delta.Creates)
	}

	var doc *core.IntentSource
	for i := range np.Intent.Sources {
		if np.Intent.Sources[i].Kind == "document" {
			doc = &np.Intent.Sources[i]
		}
	}
	if doc == nil || doc.Ref != sdkResolveTSEntryPath {
		t.Errorf("expected a fresh document source naming %q, got: %+v", sdkResolveTSEntryPath, np.Intent.Sources)
	}
	if !hasPromotionSourceRef(np, id) {
		t.Fatalf("expected a promotion source referencing %s, got sources: %+v", id, np.Intent.Sources)
	}
}

// TestPromote_SDKAuthoredSource_ContentHashMismatch_Refused is UBI-60's
// own required adversarial case: the file on disk no longer matches
// what the source proposal's own content_hash recorded (a real edit
// after the fact) -- promote must refuse outright, by name, never
// silently re-running a DIFFERENT program under the original's own
// provenance claim.
func TestPromote_SDKAuthoredSource_ContentHashMismatch_Refused(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("d", 64)
	staleHash := "sha256:" + strings.Repeat("0", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", []core.IntentSource{
		{Kind: "document", Ref: sdkResolveTSEntryPath, ContentHash: staleHash},
	})

	_, err := runUbx(t, nil, "promote", id, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse a content_hash mismatch")
	}
	if !strings.Contains(err.Error(), "changed since") || !strings.Contains(err.Error(), staleHash) || !strings.Contains(err.Error(), sdkResolveTSContentHash) {
		t.Errorf("error = %v, want it to name both the stale (%s) and current (%s) hashes", err, staleHash, sdkResolveTSContentHash)
	}
}

// promoteCrossStackTargetFixture sets up ONE independent, real remote
// ledger store (a fresh in-memory bucket, stack-prefixed -- the exact
// pattern cli/blueprint_crossref_test.go's own crossRefStoreFixture
// established for UBI-134) for --to targetDir, seeded with a
// "networking.aws_vpc.main" neighbor whose own "id" is vpcID -- a real,
// distinct value per call, so two calls produce two genuinely
// independently-addressable targets. Both openRemoteLedgerStore (the
// CLI seam, used by --to's own openLedgerForStack) and
// core.RegisterRemoteLedgerOpener (resolveCross's own "stack"-mode
// neighbor lookup, core.OpenRef -- never routed through the CLI seam at
// all) are pointed at the SAME shared bucket. Restored via t.Cleanup in
// LIFO order -- calling this twice in one test is safe, each call's own
// cleanup restores exactly what was active immediately before it ran.
func promoteCrossStackTargetFixture(t *testing.T, vpcID string) (targetDir string) {
	t.Helper()
	targetDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(targetDir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".ubx", "config"), []byte("[ledger]\nstore = \"mem://acme-ledger\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })

	prefixedOpener := func(ctx context.Context, ref string) (*ledgerstore.Store, func() error, error) {
		trimmed := strings.TrimSuffix(ref, "/")
		parts := strings.Split(trimmed, "/")
		stack := parts[len(parts)-1]
		return ledgerstore.New(blob.PrefixedBucket(shared, stack+"/")), func() error { return nil }, nil
	}

	origLedgerSeam := openRemoteLedgerStore
	openRemoteLedgerStore = prefixedOpener
	t.Cleanup(func() { openRemoteLedgerStore = origLedgerSeam })

	core.RegisterRemoteLedgerOpener(func(ctx context.Context, ref string) (core.LedgerStore, func() error, error) {
		return prefixedOpener(ctx, ref)
	})
	t.Cleanup(func() {
		core.RegisterRemoteLedgerOpener(func(ctx context.Context, storeURI string) (core.LedgerStore, func() error, error) {
			return ledgerstore.Open(ctx, storeURI)
		})
	})

	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedgerWithPinForTest(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"},
		fmt.Sprintf(`{"id":%q}`, vpcID), "", "", "")

	return targetDir
}

// TestPromote_SDKAuthoredSource_ContextAwareDrafting_DiffersByTarget is
// the ticket's own explicitly required proof that SDK promotion isn't
// just "runs without error" -- it genuinely picks up target-stack-
// specific context, differently per target, from re-running the real
// program against the real target's own real resolution context (UBI-81's
// own "a document can legitimately produce different, correct results
// depending on which stack it's evaluated against" finding, applied here
// via sdk.CrossStack's own real "stack"-mode resolution, UBI-134 --
// resolved against WHICHEVER ledger resolver.Resolve actually runs
// against, never a value baked in at evaluation time). The identical,
// UNCHANGED program (testdata/sdk_promote_crossstack/main.go, one real
// content_hash, promoted twice, never re-authored) resolves to two
// DIFFERENT real vpc ids, one per real target.
func TestPromote_SDKAuthoredSource_ContextAwareDrafting_DiffersByTarget(t *testing.T) {
	requireHermeticSandbox(t)
	sourceDir := t.TempDir()
	entryPath := filepath.Join("testdata", "sdk_promote_crossstack", "main.go")

	entryBytes, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(entryBytes)
	contentHash := "sha256:" + hex.EncodeToString(sum[:])

	id := strings.Repeat("7", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", []core.IntentSource{
		{Kind: "document", Ref: entryPath, ContentHash: contentHash},
	})

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	targetADir := promoteCrossStackTargetFixture(t, "vpc-target-a-111")
	outA, err := runUbx(t, env, "promote", id, "--ledger-dir", sourceDir, "--to", targetADir, "--provider", fakeProviderBinary, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx promote (target A): %v\noutput: %s", err, outA)
	}
	hashA := mustExtractPlanHash(t, targetADir, outA)
	planA, err := readPlanFile(targetADir, hashA)
	if err != nil {
		t.Fatalf("read target A's own saved plan: %v", err)
	}
	if len(planA.Delta.Creates) != 1 || !strings.Contains(string(planA.Delta.Creates[0]), "vpc-target-a-111") {
		t.Fatalf("expected target A's own real neighbor value vpc-target-a-111, got: %s", planA.Delta.Creates[0])
	}

	targetBDir := promoteCrossStackTargetFixture(t, "vpc-target-b-222")
	outB, err := runUbx(t, env, "promote", id, "--ledger-dir", sourceDir, "--to", targetBDir, "--provider", fakeProviderBinary, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx promote (target B): %v\noutput: %s", err, outB)
	}
	hashB := mustExtractPlanHash(t, targetBDir, outB)
	planB, err := readPlanFile(targetBDir, hashB)
	if err != nil {
		t.Fatalf("read target B's own saved plan: %v", err)
	}
	if len(planB.Delta.Creates) != 1 || !strings.Contains(string(planB.Delta.Creates[0]), "vpc-target-b-222") {
		t.Fatalf("expected target B's own real neighbor value vpc-target-b-222, got: %s", planB.Delta.Creates[0])
	}

	// The real proof: the SAME unchanged program produced two REAL,
	// DIFFERENT resolved values -- never a frozen value copied from
	// wherever it was first promoted.
	if string(planA.Delta.Creates[0]) == string(planB.Delta.Creates[0]) {
		t.Fatal("target A and target B resolved to byte-identical config -- the program's own context-dependent resolution isn't actually differing by target")
	}
}

func TestPromote_TargetDirWithNoConfig_Allowed(t *testing.T) {
	// "target dir has no config" is deliberately NOT an error -- every
	// other command already treats a missing .ubx/config as "defaults
	// apply" (LoadConfig's own documented behavior), and promote follows
	// the identical convention for --to. This is exercised implicitly by
	// every other test here (none of them create a .ubx/config in
	// targetDir), asserted explicitly here as its own adversarial case.
	sourceDir := t.TempDir()
	targetDir := t.TempDir() // no .ubx/config ever written here

	docPath := filepath.Join(sourceDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget.")
	sourceID := strings.Repeat("e", 64)
	appendAcceptedProposal(t, sourceDir, sourceID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
	})
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "promote", sourceID,
		"--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx promote against a target dir with no .ubx/config: %v\noutput: %s", err, out)
	}
}

func TestPromote_ChainOfPromotionEvidence_WalksOneHopAtATime(t *testing.T) {
	stagingDir := t.TempDir()
	qaDir := t.TempDir()
	prodDir := t.TempDir()

	docPath := filepath.Join(stagingDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget.")
	stagingID := strings.Repeat("1", 64)
	appendAcceptedProposal(t, stagingDir, stagingID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
	})

	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// staging -> qa, then accept it for real in qa so it can itself be promoted.
	qaPromoteOut, err := runUbx(t, env, "promote", stagingID, "--ledger-dir", stagingDir, "--to", qaDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("promote staging->qa: %v\noutput: %s", err, qaPromoteOut)
	}
	qaHash := mustExtractPlanHash(t, qaDir, qaPromoteOut)
	shipOut, err := runUbx(t, env, "ship", qaHash, "--ledger-dir", qaDir, "--provider", fakeProviderBinary, "--yes")
	if err != nil {
		t.Fatalf("ubx ship (accept the qa promotion): %v\noutput: %s", err, shipOut)
	}

	// qa's own accepted proposal must itself carry a promotion source
	// pointing back at staging.
	qaLedger := core.Open(qaDir)
	qaProposal, err := qaLedger.Read(qaHash)
	if err != nil {
		t.Fatalf("read qa's own accepted proposal: %v", err)
	}
	if !hasPromotionSourceRef(qaProposal, stagingID) {
		t.Fatalf("expected qa's own proposal to carry a promotion source referencing staging id %s, got sources: %+v", stagingID, qaProposal.Intent.Sources)
	}

	// qa -> prod: promotes qa's own now-accepted id.
	prodPromoteOut, err := runUbx(t, env, "promote", qaHash, "--ledger-dir", qaDir, "--to", prodDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("promote qa->prod: %v\noutput: %s", err, prodPromoteOut)
	}
	prodHash := mustExtractPlanHash(t, prodDir, prodPromoteOut)
	prodPlan, err := readPlanFile(prodDir, prodHash)
	if err != nil {
		t.Fatalf("read prod's own saved plan: %v", err)
	}

	// prod's own new proposal names qa (its immediate source), NOT
	// staging directly -- the chain is walked one hop at a time, not
	// flattened. qa's own ledger entry (still readable, asserted above)
	// is what recovers the earlier staging<-qa hop.
	if !hasPromotionSourceRef(prodPlan, qaHash) {
		t.Fatalf("expected prod's own proposal to carry a promotion source referencing qa's own id %s (its immediate source), got sources: %+v", qaHash, prodPlan.Intent.Sources)
	}
	if hasPromotionSourceRef(prodPlan, stagingID) {
		t.Fatalf("prod's own proposal should NOT directly reference staging (%s) -- the chain is per-hop, reconstructed by following qa's own promotion source, not flattened", stagingID)
	}
}

// hasPromotionSourceRef reports whether p has a "promotion"-kind
// intent.sources entry whose Ref matches want.
func hasPromotionSourceRef(p *core.Proposal, want string) bool {
	for _, s := range p.Intent.Sources {
		if s.Kind == "promotion" && s.Ref == want {
			return true
		}
	}
	return false
}

func TestWhy_RendersPromotionSource(t *testing.T) {
	ledgerDir := t.TempDir()
	id := strings.Repeat("9", 64)
	appendAcceptedProposal(t, ledgerDir, id, "payments", []core.IntentSource{
		{Kind: "promotion", Ref: strings.Repeat("8", 64), Base: "staging"},
	})

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, out)
	}
	want := "source: promoted from staging/" + displayHash(strings.Repeat("8", 64), false)
	if !strings.Contains(out, want) {
		t.Errorf("expected why to render %q, got:\n%s", want, out)
	}
}
