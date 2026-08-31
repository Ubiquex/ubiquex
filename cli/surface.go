package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	adevops "github.com/ubiquex/ubiquex/vcs/azuredevops"
	bbcloud "github.com/ubiquex/ubiquex/vcs/bitbucketcloud"
	bbserver "github.com/ubiquex/ubiquex/vcs/bitbucketserver"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/vcs/github"
	glab "github.com/ubiquex/ubiquex/vcs/gitlab"
	"github.com/ubiquex/ubiquex/writeback"
)

// surfaceTarget names the one real repository, on one real platform,
// that `--surface-as` opens its issue/pull request in (UBI-165).
//
// Exactly one platform selector may be set, the same "exactly one of"
// discipline `ubx accept --from-merge` already applies -- surfacing the
// same drift twice, on two platforms, is never what an operator meant.
type surfaceTarget struct {
	githubRepo             string // owner/name
	gitlabProject          string // namespace/project
	azureDevOpsProject     string // organization/project/repository
	bitbucketServerURL     string
	bitbucketServerProject string // PROJECTKEY/repository-slug
	bitbucketCloudRepo     string // workspace/repo-slug

	azureDevOpsWorkItemType string

	githubAPIBaseURL      string
	gitlabAPIBaseURL      string
	azureDevOpsAPIBaseURL string
}

// selected returns the one platform selector that is set, or an error
// naming the real problem when zero or more than one is.
func (t surfaceTarget) selected() (platform string, err error) {
	set := map[string]string{
		"github":          t.githubRepo,
		"gitlab":          t.gitlabProject,
		"azuredevops":     t.azureDevOpsProject,
		"bitbucketserver": t.bitbucketServerProject,
		"bitbucketcloud":  t.bitbucketCloudRepo,
	}
	var found []string
	for k, v := range set {
		if v != "" {
			found = append(found, k)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("--surface-as requires exactly one of --github-repo, --gitlab-project, --azure-devops-project, --bitbucket-server-project, or --bitbucket-cloud-repo")
	default:
		sort := strings.Join(found, ", ")
		return "", fmt.Errorf("--surface-as takes exactly one platform, got %d (%s) -- surfacing the same drift on two platforms is never what was meant", len(found), sort)
	}
}

// surfaceDrift is UBI-11 stage 3's GitHub App skeleton, extended in
// UBI-165 to every platform `ubx server` actually supports: given a
// scan-generated drift_adopt proposal, opens either an issue (or that
// platform's own real equivalent) or a draft pull request carrying a
// receipt (docs/architecture.md -- Decision loop).
//
// Read-only-friendly by design: issue mode needs only the platform's own
// issue-write permission, never write access to repository contents at
// all -- the tool never applies anything, it only records and asks a
// human to review. PR mode automates stage 1's own by-hand flow (commit a
// draft proposal to a branch, trailer in the PR body) and does need
// contents-write, which is why this stays an explicit --surface-as choice
// rather than defaulting to the more privileged option.
//
// The receipt itself is byte-identical across all five platforms
// (buildReceipt): the platforms differ in how they take it, never in
// what it says.
func surfaceDrift(ctx context.Context, out io.Writer, p *core.Proposal, addr core.Address, surfaceAs string, target surfaceTarget, tfDir string) error {
	platform, err := target.selected()
	if err != nil {
		return err
	}
	if surfaceAs != "issue" && surfaceAs != "pr" {
		return fmt.Errorf("--surface-as must be \"issue\" or \"pr\", got %q", surfaceAs)
	}

	diffPreview := driftDiffPreview(p, tfDir)
	title := fmt.Sprintf("drift: %s", addr)

	hash, err := core.Hash(p)
	if err != nil {
		return fmt.Errorf("surface drift: %w", err)
	}
	proposalJSON, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("surface drift: %w", err)
	}
	branch := fmt.Sprintf("ubx-drift/%s-%s-%s", addr.Stack, addr.Type, addr.Name)
	proposalPath := fmt.Sprintf("ubx-drift/%s.%s.%s.json", addr.Stack, addr.Type, addr.Name)
	commitMessage := fmt.Sprintf("record drift on %s", addr)

	// issueReceipt carries no trailer: nothing merges an issue, so there
	// is no acceptance to derive from one. prReceipt carries the real
	// ubx-proposal trailer, which is exactly what makes `ubx accept
	// --from-merge` work once a human merges the result.
	issueReceipt := buildReceipt(p, "", diffPreview)
	prReceipt := buildReceipt(p, hash, diffPreview)

	switch platform {
	case "github":
		owner, repo, ok := strings.Cut(target.githubRepo, "/")
		if !ok || owner == "" || repo == "" {
			return fmt.Errorf("--github-repo must be \"owner/name\", got %q", target.githubRepo)
		}
		var opts []ghub.Option
		switch {
		case target.githubAPIBaseURL != "":
			opts = append(opts, ghub.WithEnterpriseBaseURL(target.githubAPIBaseURL))
		case os.Getenv("UBX_GITHUB_API_BASE_URL") != "":
			opts = append(opts, ghub.WithBaseURL(os.Getenv("UBX_GITHUB_API_BASE_URL")))
		}
		api, err := ghub.New(os.Getenv("GITHUB_TOKEN"), opts...)
		if err != nil {
			return err
		}
		if surfaceAs == "issue" {
			issue, err := ghub.CreateIssue(ctx, api, owner, repo, title, issueReceipt)
			if err != nil {
				return fmt.Errorf("surface drift: %w", err)
			}
			fmt.Fprintf(out, "opened issue %s\n", issue.HTMLURL)
			return nil
		}
		pr, err := ghub.OpenDraftPR(ctx, api, owner, repo, branch, proposalPath, proposalJSON, commitMessage, title, prReceipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened pull request %s\n", pr.HTMLURL)
		return nil

	case "gitlab":
		var opts []glab.Option
		if base := orEnv(target.gitlabAPIBaseURL, "UBX_GITLAB_API_BASE_URL"); base != "" {
			opts = append(opts, glab.WithBaseURL(base))
		}
		api, err := glab.New(os.Getenv("GITLAB_TOKEN"), opts...)
		if err != nil {
			return err
		}
		if surfaceAs == "issue" {
			issue, err := glab.CreateIssue(ctx, api, target.gitlabProject, title, issueReceipt)
			if err != nil {
				return fmt.Errorf("surface drift: %w", err)
			}
			fmt.Fprintf(out, "opened issue %s\n", issue.HTMLURL)
			return nil
		}
		mr, err := glab.OpenDraftMR(ctx, api, target.gitlabProject, branch, proposalPath, proposalJSON, commitMessage, title, prReceipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened merge request %s\n", mr.HTMLURL)
		return nil

	case "azuredevops":
		parts := strings.SplitN(target.azureDevOpsProject, "/", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return fmt.Errorf("--azure-devops-project must be \"organization/project/repository\", got %q", target.azureDevOpsProject)
		}
		organization, project, repository := parts[0], parts[1], parts[2]
		var opts []adevops.Option
		if base := orEnv(target.azureDevOpsAPIBaseURL, "UBX_AZURE_DEVOPS_API_BASE_URL"); base != "" {
			opts = append(opts, adevops.WithBaseURL(base))
		}
		api := adevops.New(organization, os.Getenv("AZURE_DEVOPS_TOKEN"), opts...)
		if surfaceAs == "issue" {
			// Azure DevOps has work items, not issues, and they live in
			// Azure Boards rather than in Repos -- see
			// azuredevops.CreateWorkItem's own doc comment for why the
			// type is configurable and what happens on a Scrum or CMMI
			// project.
			wi, err := adevops.CreateWorkItem(ctx, api, project, target.azureDevOpsWorkItemType, title, issueReceipt)
			if err != nil {
				return fmt.Errorf("surface drift: %w", err)
			}
			fmt.Fprintf(out, "opened work item %s\n", wi.HTMLURL)
			return nil
		}
		pr, err := adevops.OpenDraftPR(ctx, api, project, repository, branch, proposalPath, proposalJSON, commitMessage, title, prReceipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened pull request %s\n", pr.HTMLURL)
		return nil

	case "bitbucketserver":
		if target.bitbucketServerURL == "" {
			return fmt.Errorf("--bitbucket-server-project requires --bitbucket-server-url")
		}
		key, slug, ok := strings.Cut(target.bitbucketServerProject, "/")
		if !ok || key == "" || slug == "" {
			return fmt.Errorf("--bitbucket-server-project must be \"PROJECTKEY/repository-slug\", got %q", target.bitbucketServerProject)
		}
		baseURL := target.bitbucketServerURL
		if base := os.Getenv("UBX_BITBUCKET_SERVER_API_BASE_URL"); base != "" {
			baseURL = base
		}
		api := bbserver.New(baseURL, os.Getenv("BITBUCKET_SERVER_TOKEN"))
		if surfaceAs == "issue" {
			// Refused, never silently downgraded to a pull request: see
			// bitbucketserver.ErrNoIssueTracker.
			return fmt.Errorf("surface drift: %w", bbserver.ErrNoIssueTracker)
		}
		pr, err := bbserver.OpenDraftPR(ctx, api, key, slug, branch, proposalPath, proposalJSON, commitMessage, title, prReceipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened pull request %s\n", pr.HTMLURL)
		return nil

	default: // bitbucketcloud
		workspace, slug, ok := strings.Cut(target.bitbucketCloudRepo, "/")
		if !ok || workspace == "" || slug == "" {
			return fmt.Errorf("--bitbucket-cloud-repo must be \"workspace/repo-slug\", got %q", target.bitbucketCloudRepo)
		}
		var opts []bbcloud.Option
		if base := os.Getenv("UBX_BITBUCKET_CLOUD_API_BASE_URL"); base != "" {
			opts = append(opts, bbcloud.WithBaseURL(base))
		}
		api := bbcloud.New(os.Getenv("BITBUCKET_CLOUD_TOKEN"), opts...)
		if surfaceAs == "issue" {
			issue, err := bbcloud.CreateIssue(ctx, api, workspace, slug, title, issueReceipt)
			if err != nil {
				return fmt.Errorf("surface drift: %w", err)
			}
			fmt.Fprintf(out, "opened issue %s\n", issue.HTMLURL)
			return nil
		}
		pr, err := bbcloud.OpenDraftPR(ctx, api, workspace, slug, branch, proposalPath, proposalJSON, commitMessage, title, prReceipt)
		if err != nil {
			return fmt.Errorf("surface drift: %w", err)
		}
		fmt.Fprintf(out, "opened pull request %s\n", pr.HTMLURL)
		return nil
	}
}

// driftDiffPreview computes a best-effort ".tf write-back would do this"
// preview for the receipt — if tfDir is empty, or writeback can't find/apply
// a match (no .tf files given, resource block absent, etc.), the preview
// is simply omitted rather than failing drift surfacing over an optional
// enrichment. Reuses writeback.FindAndApply exactly as `ubx writeback` does;
// nothing here requires the proposal to be accepted first, since this is
// purely a dry-run preview, never a real write (writeback.FindAndApply
// itself never touches disk — only cli/writeback.go's own --write flag
// does that).
func driftDiffPreview(p *core.Proposal, tfDir string) string {
	if tfDir == "" || len(p.Delta.Modifies) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range p.Delta.Modifies {
		path, newContent, _, err := writeback.FindAndApply(tfDir, m.Target, m)
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
