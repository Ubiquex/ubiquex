package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ubiquex/ubiquex-cli/core"
	ghub "github.com/ubiquex/ubiquex-cli/github"
	"github.com/ubiquex/ubiquex-cli/tfwrite"
)

// surfaceDrift is UBI-11 stage 3's GitHub App skeleton: given a
// scan-generated drift_adopt proposal, opens either a GitHub issue or a
// draft PR carrying a receipt (docs/architecture.md — Decision loop).
//
// Read-only-friendly by design: issue mode needs only "issues: write" on
// the target repo, never "contents: write" at all — the App never
// applies anything, it only records and asks a human to review (see
// docs/architecture.md's security-story framing: "because acceptance is
// derived, a GitHub App that only ever reads... can still do everything
// stage 3 needs"). PR mode automates stage 1's own by-hand flow (commit a
// draft proposal to a branch, trailer in the PR body) and does need
// "contents: write" — a real deployment would default to issue mode and
// opt into PR mode deliberately, which is why this CLI mirrors that with
// an explicit --surface-as flag rather than defaulting to the more
// privileged option.
func surfaceDrift(ctx context.Context, out io.Writer, p *core.Proposal, addr core.Address, surfaceAs, githubRepo, tfDir string) error {
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("--github-repo must be \"owner/name\", got %q", githubRepo)
	}

	diffPreview := driftDiffPreview(p, tfDir)

	var apiOpts []ghub.Option
	if base := os.Getenv("UBX_GITHUB_API_BASE_URL"); base != "" {
		apiOpts = append(apiOpts, ghub.WithBaseURL(base))
	}
	api := ghub.New(os.Getenv("GITHUB_TOKEN"), apiOpts...)

	switch surfaceAs {
	case "issue":
		receipt := buildReceipt(p, "", diffPreview)
		issue, err := ghub.CreateIssue(ctx, api, owner, repo, fmt.Sprintf("drift: %s", addr), receipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened issue %s\n", issue.HTMLURL)
		return nil

	case "pr":
		hash, err := core.Hash(p)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		receipt := buildReceipt(p, hash, diffPreview)
		proposalJSON, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		branch := fmt.Sprintf("ubx-drift/%s-%s-%s", addr.Stack, addr.Type, addr.Name)
		proposalPath := fmt.Sprintf("ubx-drift/%s.%s.%s.json", addr.Stack, addr.Type, addr.Name)
		pr, err := ghub.OpenDraftPR(ctx, api, owner, repo, branch, proposalPath, proposalJSON,
			fmt.Sprintf("record drift on %s", addr), fmt.Sprintf("drift: %s", addr), receipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened pull request %s\n", pr.HTMLURL)
		return nil

	default:
		return fmt.Errorf("--surface-as must be \"issue\" or \"pr\", got %q", surfaceAs)
	}
}

// driftDiffPreview computes a best-effort ".tf write-back would do this"
// preview for the receipt — if tfDir is empty, or tfwrite can't find/apply
// a match (no .tf files given, resource block absent, etc.), the preview
// is simply omitted rather than failing drift surfacing over an optional
// enrichment. Reuses tfwrite.FindAndApply exactly as `ubx writeback` does;
// nothing here requires the proposal to be accepted first, since this is
// purely a dry-run preview, never a real write (tfwrite.FindAndApply
// itself never touches disk — only cli/writeback.go's own --write flag
// does that).
func driftDiffPreview(p *core.Proposal, tfDir string) string {
	if tfDir == "" || len(p.Delta.Modifies) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range p.Delta.Modifies {
		path, newContent, _, err := tfwrite.FindAndApply(tfDir, m.Target, m)
		if err != nil {
			fmt.Fprintf(&b, "(no .tf preview for %s: %v)\n", m.Target, err)
			continue
		}
		diff, err := unifiedDiff(path, newContent)
		if err != nil {
			fmt.Fprintf(&b, "(no .tf preview for %s: %v)\n", m.Target, err)
			continue
		}
		b.WriteString(diff)
	}
	return strings.TrimSpace(b.String())
}
