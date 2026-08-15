package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	adevops "github.com/ubiquex/ubiquex/azuredevops"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
)

// ErrAmbiguousProposalFileAzureDevOps is webhook.go's own
// ErrAmbiguousProposalFile, Azure DevOps' real counterpart.
var ErrAmbiguousProposalFileAzureDevOps = fmt.Errorf("could not identify exactly one proposals/*.json file in this PR's own changed files")

// azureDevOpsWebhookHandler implements http.Handler for Azure DevOps'
// own real Service Hooks delivery target -- webhookHandler's (GitHub)/
// gitlabWebhookHandler's (GitLab) own real counterpart. A real, load-
// bearing difference from both: Azure DevOps' own "git.pullrequest.
// updated" event folds push/reviewer-list/status/vote changes into one
// real eventType string, distinguished only by which of up to several
// real, separately-configured Service Hook subscriptions actually
// delivered it (a subscription-time "notificationType" filter, never a
// payload field -- confirmed directly against Microsoft's own current
// docs, not assumed inspectable at delivery time the way GitLab's own
// per-delivery "action" field is). This server asks the operator to
// encode that real distinction in the subscription's own target URL
// via a "notification" query parameter (?notification=vote,
// ?notification=push) -- the only real, deterministic way to recover
// it server-side, since Azure DevOps itself gives no other way.
type azureDevOpsWebhookHandler struct {
	s *Server
}

func (h azureDevOpsWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validAzureDevOpsSignature(h.s.cfg.AzureDevOpsWebhookSecretHeader, h.s.cfg.AzureDevOpsWebhookSecret, r) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var envelope struct {
		EventType string          `json:"eventType"`
		Resource  json.RawMessage `json:"resource"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "unrecognized event", http.StatusBadRequest)
		return
	}

	// Same real "respond fast, do the real work after" reasoning as
	// webhookHandler's/gitlabWebhookHandler's own ServeHTTP.
	w.WriteHeader(http.StatusAccepted)

	notification := r.URL.Query().Get("notification")
	go h.s.handleAzureDevOpsEvent(context.Background(), envelope.EventType, envelope.Resource, notification)
}

func (s *Server) handleAzureDevOpsEvent(ctx context.Context, eventType string, resource json.RawMessage, notification string) {
	var err error
	switch eventType {
	case "git.pullrequest.created":
		err = s.handlePullRequestCreatedAzureDevOps(ctx, resource)
	case "git.pullrequest.updated":
		err = s.handlePullRequestUpdatedAzureDevOps(ctx, resource, notification)
	case "git.pullrequest.merged":
		err = s.handlePullRequestMergedAzureDevOps(ctx, resource)
	case "ms.vss-code.git-pullrequest-comment-event":
		err = s.handlePullRequestCommentAzureDevOps(ctx, resource)
	default:
		return
	}
	if err != nil {
		slog.Error("ubx server: azure devops event handling failed", "error", err)
	}
}

// prIdentity is the real, minimal set of identifiers every Azure
// DevOps PR-event handler needs, extracted once from a decoded
// git.GitPullRequest -- project/repositoryID/prID address the real
// API calls (three real identifiers, never two, see server/config.go's
// own RepoConfig doc comment); matching them against Config.Repos is
// what decides whether this event is allowed to be acted on at all.
type prIdentityAzureDevOps struct {
	project      string
	repositoryID string
	prID         int
	headSHA      string
	targetRef    string
}

func prIdentityFromAzureDevOps(pr *git.GitPullRequest) (prIdentityAzureDevOps, error) {
	var id prIdentityAzureDevOps
	if pr.PullRequestId == nil {
		return id, fmt.Errorf("pull request has no pullRequestId")
	}
	id.prID = *pr.PullRequestId
	if pr.Repository == nil || pr.Repository.Id == nil || pr.Repository.Project == nil || pr.Repository.Project.Name == nil {
		return id, fmt.Errorf("pull request %d has no real repository/project reference", id.prID)
	}
	id.repositoryID = pr.Repository.Id.String()
	id.project = *pr.Repository.Project.Name
	if pr.LastMergeSourceCommit != nil && pr.LastMergeSourceCommit.CommitId != nil {
		id.headSHA = *pr.LastMergeSourceCommit.CommitId
	}
	if pr.TargetRefName != nil {
		id.targetRef = *pr.TargetRefName
	}
	return id, nil
}

// handlePullRequestCreatedAzureDevOps is core-flow step 1: a real
// "git.pullrequest.created" event runs `ubx plan` automatically.
//
// UBI-166: refuses outright, loudly logged, if project/repositoryID
// has no real Config.Repos entry at all -- see allowlist.go's own doc
// comment.
func (s *Server) handlePullRequestCreatedAzureDevOps(ctx context.Context, resource json.RawMessage) error {
	var pr git.GitPullRequest
	if err := json.Unmarshal(resource, &pr); err != nil {
		return fmt.Errorf("decode PR created event: %w", err)
	}
	id, err := prIdentityFromAzureDevOps(&pr)
	if err != nil {
		return err
	}
	api := s.azureDevOpsClient()

	candidates := s.matchingRepoConfigsAzureDevOps(id.project, id.repositoryID)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing git.pullrequest.created event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "azuredevops", "repo", id.project+"/"+id.repositoryID, "pr", id.prID)
		return nil
	}
	repoDir, err := s.checkoutForAzureDevOps(ctx, id.project, id.repositoryID, id.headSHA)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.project, id.repositoryID, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackAzureDevOps(ctx, api, id, "git.pullrequest.created", err)
	}
	return s.runPlanAndCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, repoDir, ledgerDir)
}

// handlePullRequestUpdatedAzureDevOps is core-flow steps 3 (re-plan on
// push) and 4 (approval derives acceptance), disambiguated by
// notification -- see azureDevOpsWebhookHandler's own doc comment for
// why this can't be told apart from the payload alone. An empty or
// unrecognized notification value is silently ignored, never guessed.
//
// UBI-166: the allowlist check below covers both the "push" and
// "vote" branches -- handleAzureDevOpsApproval never consumes a
// resolved ledger_dir at all (it calls core.AcceptFromReview directly
// against repoDir, the same real scoping
// refuseAmbiguousStackGitHub's own doc comment explains for GitHub's
// own approval flow), so only membership matters for that branch.
func (s *Server) handlePullRequestUpdatedAzureDevOps(ctx context.Context, resource json.RawMessage, notification string) error {
	var pr git.GitPullRequest
	if err := json.Unmarshal(resource, &pr); err != nil {
		return fmt.Errorf("decode PR updated event: %w", err)
	}
	id, err := prIdentityFromAzureDevOps(&pr)
	if err != nil {
		return err
	}
	api := s.azureDevOpsClient()

	candidates := s.matchingRepoConfigsAzureDevOps(id.project, id.repositoryID)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing git.pullrequest.updated event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "azuredevops", "repo", id.project+"/"+id.repositoryID, "pr", id.prID, "notification", notification)
		return nil
	}

	switch notification {
	case "push":
		repoDir, err := s.checkoutForAzureDevOps(ctx, id.project, id.repositoryID, id.headSHA)
		if err != nil {
			return err
		}
		ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
			return api.ListPullRequestChangedFiles(ctx, id.project, id.repositoryID, id.prID)
		})
		if err != nil {
			return s.refuseAmbiguousStackAzureDevOps(ctx, api, id, "git.pullrequest.updated:push", err)
		}
		return s.runPlanAndCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, repoDir, ledgerDir)
	case "vote":
		var approver *git.IdentityRefWithVote
		if pr.Reviewers != nil {
			for i, r := range *pr.Reviewers {
				if r.Vote != nil && *r.Vote == 10 {
					approver = &(*pr.Reviewers)[i]
				}
			}
		}
		if approver == nil {
			return nil // this specific delivery isn't a real approve vote (e.g. a rejection/waiting vote) -- nothing to derive
		}
		return s.handleAzureDevOpsApproval(ctx, api, id, approver)
	}
	return nil
}

// handlePullRequestMergedAzureDevOps is core-flow step 5's automatic
// path: a real "git.pullrequest.merged" event (fired regardless of
// success -- confirmed directly against Microsoft's own current docs;
// mergeStatus on the resource distinguishes them) runs `ubx ship --yes`
// checked out at the target branch, only when the merge actually
// succeeded.
func (s *Server) handlePullRequestMergedAzureDevOps(ctx context.Context, resource json.RawMessage) error {
	if !s.cfg.ShipOnMerge {
		return nil
	}
	var pr git.GitPullRequest
	if err := json.Unmarshal(resource, &pr); err != nil {
		return fmt.Errorf("decode PR merged event: %w", err)
	}
	if pr.MergeStatus == nil || !strings.EqualFold(string(*pr.MergeStatus), string(git.PullRequestAsyncStatusValues.Succeeded)) {
		return nil
	}
	id, err := prIdentityFromAzureDevOps(&pr)
	if err != nil {
		return err
	}
	api := s.azureDevOpsClient()

	candidates := s.matchingRepoConfigsAzureDevOps(id.project, id.repositoryID)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing git.pullrequest.merged event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "azuredevops", "repo", id.project+"/"+id.repositoryID, "pr", id.prID)
		return nil
	}
	repoDir, err := s.checkoutForAzureDevOps(ctx, id.project, id.repositoryID, id.targetRef)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.project, id.repositoryID, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackAzureDevOps(ctx, api, id, "git.pullrequest.merged", err)
	}
	return s.runAutomaticShipAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, repoDir, ledgerDir)
}

// refuseAmbiguousStackAzureDevOps is refuseAmbiguousStackGitHub's own
// Azure DevOps counterpart.
func (s *Server) refuseAmbiguousStackAzureDevOps(ctx context.Context, api *adevops.Client, id prIdentityAzureDevOps, event string, err error) error {
	slog.Error("ubx server: refusing event -- cannot resolve to exactly one discovered stack (UBI-167)",
		"platform", "azuredevops", "repo", id.project+"/"+id.repositoryID, "pr", id.prID, "event", event, "error", err)
	return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "plan",
		fmt.Sprintf("`ubx server` could not resolve this PR to exactly one stack: %s. Fix this repository's own `.ubx/config` layout, or run `ubx plan`/`ubx ship` locally instead.", err))
}

// handlePullRequestCommentAzureDevOps is core-flow steps 3 (manual
// re-plan) and 4's manual path (ship via comment). Always re-fetches
// the PR fresh via GetPullRequest -- the comment event's own
// "pullRequest" resource field is confirmed real but not necessarily
// current by the time this handler actually runs, the identical real
// reasoning handleIssueCommentEvent/handleMergeCommentEvent already
// apply for GitHub/GitLab.
func (s *Server) handlePullRequestCommentAzureDevOps(ctx context.Context, resource json.RawMessage) error {
	var payload struct {
		Comment     git.Comment        `json:"comment"`
		PullRequest git.GitPullRequest `json:"pullRequest"`
	}
	if err := json.Unmarshal(resource, &payload); err != nil {
		return fmt.Errorf("decode PR comment event: %w", err)
	}
	if payload.Comment.CommentType != nil && string(*payload.Comment.CommentType) == "system" {
		return nil // a real, Azure-DevOps-generated system comment, never a real command
	}
	if payload.Comment.Content == nil {
		return nil
	}

	body := strings.TrimSpace(*payload.Comment.Content)
	fields := strings.Fields(body)
	if len(fields) == 0 || fields[0] != "ubx" || len(fields) < 2 {
		return nil
	}
	verb, flags := fields[1], fields[2:]

	var commenterDisplayName, commenterDescriptor string
	if payload.Comment.Author != nil {
		if payload.Comment.Author.DisplayName != nil {
			commenterDisplayName = *payload.Comment.Author.DisplayName
		}
		if payload.Comment.Author.Descriptor != nil {
			commenterDescriptor = *payload.Comment.Author.Descriptor
		}
	}

	partialID, err := prIdentityFromAzureDevOps(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.azureDevOpsClient()

	pr, err := api.GetPullRequest(ctx, partialID.project, partialID.repositoryID, partialID.prID)
	if err != nil {
		return fmt.Errorf("get PR %d: %w", partialID.prID, err)
	}
	id, err := prIdentityFromAzureDevOps(pr)
	if err != nil {
		return err
	}

	candidates := s.matchingRepoConfigsAzureDevOps(id.project, id.repositoryID)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing ms.vss-code.git-pullrequest-comment-event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "azuredevops", "repo", id.project+"/"+id.repositoryID, "pr", id.prID, "verb", verb)
		return nil
	}
	repoDir, err := s.checkoutForAzureDevOps(ctx, id.project, id.repositoryID, id.headSHA)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.project, id.repositoryID, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackAzureDevOps(ctx, api, id, "comment:"+verb, err)
	}

	var creator *git.IdentityRefWithVote
	if pr.CreatedBy != nil {
		creator = &git.IdentityRefWithVote{DisplayName: pr.CreatedBy.DisplayName, Descriptor: pr.CreatedBy.Descriptor}
	}
	commenter := &git.IdentityRefWithVote{DisplayName: &commenterDisplayName, Descriptor: &commenterDescriptor}

	switch verb {
	case "plan":
		ok, err := isAuthorizedToReplanAzureDevOps(ctx, api, id.project, s.cfg.AzureDevOpsBotDisplayName, creator, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "plan",
				fmt.Sprintf("@%s isn't authorized to re-run `ubx plan` on this PR -- only its own creator (or, for a drift-watch-opened PR, a project member with real, current Contributors-group access) can.", commenterDisplayName))
		}
		_ = flags
		return s.runPlanAndCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, repoDir, ledgerDir)

	case "ship":
		ok, err := isAuthorizedToShipAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, commenterDisplayName, commenterDescriptor)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "ship",
				fmt.Sprintf("@%s isn't a CODEOWNERS-listed owner of any file this PR changes -- not authorized to `ubx ship` it.", commenterDisplayName))
		}
		confirmDestroys := contains(flags, "--confirm-destroys")
		return s.runManualShipAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, repoDir, ledgerDir, confirmDestroys)
	}
	return nil
}

// handleAzureDevOpsApproval is core-flow step 4's own trigger: a real
// vote=10 ("approved") change, never a comment.
//
// Real, Azure-DevOps-specific TOCTOU safety gate, structurally
// different from both GitHub's (a specific review.commit_id) and
// GitLab's (one project-wide ResetApprovalsOnPush setting): Azure
// DevOps' own vote objects carry no per-vote commit/iteration
// reference at all (confirmed directly against the real, downloaded
// SDK's own IdentityRefWithVote struct), so this checks the real,
// current "Minimum approval count" branch policy's own
// resetOnSourcePush setting, scoped to this PR's own target repository
// and branch (azuredevops.Client.ResetOnSourcePush's own doc comment)
// -- refusing outright, with a real, explanatory posted comment, if no
// such enabled policy exists at all, or it exists but doesn't reset
// votes on push.
func (s *Server) handleAzureDevOpsApproval(ctx context.Context, api *adevops.Client, id prIdentityAzureDevOps, approver *git.IdentityRefWithVote) error {
	approverDisplayName, _ := identityRefWithVoteFields(approver)

	found, resetOnPush, err := api.ResetOnSourcePush(ctx, id.project, id.repositoryID, id.targetRef)
	if err != nil {
		return fmt.Errorf("check reset-on-source-push policy: %w", err)
	}
	if !found {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			"Approval recorded by Azure DevOps, but this branch has no real, enabled \"Minimum number of reviewers\" policy at all -- ubx server can't safely correlate an approval with a specific commit here. Configure that branch policy with \"Reset code reviewer votes when there are new changes\" on, or accept this proposal locally with `ubx accept` instead.")
	}
	if !resetOnPush {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			"Approval recorded by Azure DevOps, but this branch's own \"Minimum number of reviewers\" policy doesn't reset votes on new changes -- ubx server can't safely correlate an approval with a specific commit here. Turn \"Reset code reviewer votes when there are new changes\" on, or accept this proposal locally with `ubx accept` instead.")
	}

	pr, err := api.GetPullRequest(ctx, id.project, id.repositoryID, id.prID)
	if err != nil {
		return fmt.Errorf("get PR %d: %w", id.prID, err)
	}
	headSHA := ""
	if pr.LastMergeSourceCommit != nil && pr.LastMergeSourceCommit.CommitId != nil {
		headSHA = *pr.LastMergeSourceCommit.CommitId
	}
	if headSHA == "" {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			"Approval recorded by Azure DevOps, but `ubx` could not resolve this PR's own current head commit.")
	}

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, id.project, id.repositoryID, id.prID)
	if err != nil {
		return fmt.Errorf("list changed files for PR %d: %w", id.prID, err)
	}
	proposalFile, err := discoverProposalFileAzureDevOps(changedFiles)
	if err != nil {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			fmt.Sprintf("Approval recorded by Azure DevOps, but `ubx` could not derive acceptance from it: %s. Accept this proposal locally with `ubx accept` instead.", err))
	}

	repoDir, err := s.checkoutForAzureDevOps(ctx, id.project, id.repositoryID, headSHA)
	if err != nil {
		return err
	}

	body, err := ghub.FileAtCommit(ctx, repoDir, headSHA, proposalFile)
	if err != nil {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			fmt.Sprintf("Approval recorded by Azure DevOps, but `ubx` could not read %s at the reviewed commit: %s", proposalFile, err))
	}

	var description string
	if pr.Description != nil {
		description = *pr.Description
	}
	claimedHash, ok := ghub.ParseProposalTrailer(description)
	if !ok {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			"Approval recorded by Azure DevOps, but this PR's own description carries no `ubx-proposal: <hash>` trailer to derive acceptance from.")
	}

	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			fmt.Sprintf("Approval recorded by Azure DevOps, but %s does not parse as a real proposal file: %s", proposalFile, err))
	}

	// A destructive proposal is never accepted from an approval, full
	// stop -- the identical real safety fix UBI-28 Phase 1/2 made for
	// GitHub/GitLab, applied here identically.
	if p.BlastRadius.Destroys > 0 {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			fmt.Sprintf("Approval recorded by Azure DevOps, but %s has blast_radius.destroys > 0 -- `ubx server` never derives acceptance for a destroy from an approval, regardless of configuration. Accept it locally with `ubx accept --confirm-destroys` instead.", proposalFile))
	}

	ledger := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "azuredevops",
		PRNumber:     int64(id.prID),
		ProposalFile: proposalFile,
		Approver:     approverDisplayName,
		// Azure DevOps' own real Approve vote carries no comment field
		// at all, the same real structural gap GitLab's own approval
		// has -- approving and leaving a comment are two separate real
		// actions here too.
		Comment: "",
	})
	if err != nil {
		return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
			fmt.Sprintf("Approval recorded by Azure DevOps, but `ubx accept` refused it: %s", err))
	}

	return postOrEditCommentAzureDevOps(ctx, api, id.project, id.repositoryID, id.prID, s.cfg.AzureDevOpsBotDisplayName, "accept",
		fmt.Sprintf("accepted %s (stack %s) via PR #%d, approved by @%s", accepted.ID, accepted.Stack, id.prID, approverDisplayName))
}

// discoverProposalFileAzureDevOps is discoverProposalFile's (GitHub)
// own real counterpart, against the real, flat path list
// ListPullRequestChangedFiles already returns.
func discoverProposalFileAzureDevOps(changedFiles []string) (string, error) {
	var match string
	for _, path := range changedFiles {
		p := strings.TrimPrefix(path, "/")
		if strings.HasPrefix(p, "proposals/") && strings.HasSuffix(p, ".json") {
			if match != "" {
				return "", ErrAmbiguousProposalFileAzureDevOps
			}
			match = p
		}
	}
	if match == "" {
		return "", ErrAmbiguousProposalFileAzureDevOps
	}
	return match, nil
}

// azureDevOpsClient returns the server's own single, real, static
// Azure DevOps client -- built once at New (server.go).
func (s *Server) azureDevOpsClient() *adevops.Client {
	return s.azuredevops
}
