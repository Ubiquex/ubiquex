package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
)

// ErrAmbiguousProposalFileBitbucketServer is webhook.go's own
// ErrAmbiguousProposalFile, Bitbucket Server's real counterpart.
var ErrAmbiguousProposalFileBitbucketServer = fmt.Errorf("could not identify exactly one proposals/*.json file in this PR's own changed files")

// bitbucketServerWebhookHandler implements http.Handler for Bitbucket
// Server's own real, bundled webhook feature -- webhookHandler's
// (GitHub)/gitlabWebhookHandler's (GitLab)/azureDevOpsWebhookHandler's
// (Azure DevOps) own real counterpart. Real, structurally distinct from
// all three: Bitbucket Server gives every semantic PR change its own
// separate, real eventKey (pr:opened, pr:from_ref_updated,
// pr:reviewer:approved, pr:merged, pr:comment:added, ...) -- no folded
// event needing a query-parameter disambiguation trick the way Azure
// DevOps' single "git.pullrequest.updated" needs (see
// azureDevOpsWebhookHandler's own doc comment).
type bitbucketServerWebhookHandler struct {
	s *Server
}

func (h bitbucketServerWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validBitbucketServerSignature(h.s.cfg.BitbucketServerWebhookSecret, body, r) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var envelope struct {
		EventKey string `json:"eventKey"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "unrecognized event", http.StatusBadRequest)
		return
	}

	// Same real "respond fast, do the real work after" reasoning as
	// webhookHandler's/gitlabWebhookHandler's/
	// azureDevOpsWebhookHandler's own ServeHTTP.
	w.WriteHeader(http.StatusAccepted)

	go h.s.handleBitbucketServerEvent(context.Background(), envelope.EventKey, body)
}

func (s *Server) handleBitbucketServerEvent(ctx context.Context, eventKey string, body []byte) {
	var err error
	switch eventKey {
	case "pr:opened":
		err = s.handlePullRequestOpenedBitbucketServer(ctx, body)
	case "pr:from_ref_updated":
		err = s.handlePullRequestSourceUpdatedBitbucketServer(ctx, body)
	case "pr:reviewer:approved":
		err = s.handlePullRequestApprovedBitbucketServer(ctx, body)
	case "pr:merged":
		err = s.handlePullRequestMergedBitbucketServer(ctx, body)
	case "pr:comment:added":
		err = s.handlePullRequestCommentBitbucketServer(ctx, body)
	default:
		return
	}
	if err != nil {
		slog.Error("ubx server: bitbucket server event handling failed", "error", err)
	}
}

// prIdentityBitbucketServer is the real, minimal set of identifiers
// every Bitbucket Server PR-event handler needs, extracted once from a
// decoded bitbucketv1.PullRequest -- projectKey/repositorySlug/prID
// address the real API calls; toRef is the real target branch's own
// ref ID ("refs/heads/main"), used for the post-merge checkout, the
// same real role Azure DevOps' own targetRef field plays.
// ToRef.Repository (never FromRef.Repository) is the real, canonical
// repository identity -- Bitbucket Server's own real cross-repository
// pull request support addresses a possibly-different fork on FromRef,
// but the PR itself always belongs to ToRef's own repository.
type prIdentityBitbucketServer struct {
	projectKey     string
	repositorySlug string
	prID           int
	headSHA        string
	toRef          string
}

func prIdentityFromBitbucketServer(pr *bitbucketv1.PullRequest) (prIdentityBitbucketServer, error) {
	var id prIdentityBitbucketServer
	if pr.ToRef.Repository.Project == nil || pr.ToRef.Repository.Project.Key == "" || pr.ToRef.Repository.Slug == "" {
		return id, fmt.Errorf("pull request %d has no real project/repository reference", pr.ID)
	}
	id.projectKey = pr.ToRef.Repository.Project.Key
	id.repositorySlug = pr.ToRef.Repository.Slug
	id.prID = pr.ID
	id.headSHA = pr.FromRef.LatestCommit
	id.toRef = pr.ToRef.ID
	return id, nil
}

// handlePullRequestOpenedBitbucketServer is core-flow step 1: a real
// "pr:opened" event runs `ubx plan` automatically.
//
// UBI-166: refuses outright, loudly logged, if projectKey/
// repositorySlug has no real Config.Repos entry at all -- see
// allowlist.go's own doc comment.
func (s *Server) handlePullRequestOpenedBitbucketServer(ctx context.Context, body []byte) error {
	var payload struct {
		PullRequest bitbucketv1.PullRequest `json:"pullRequest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode pr:opened event: %w", err)
	}
	id, err := prIdentityFromBitbucketServer(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.bitbucketServerClient()

	candidates := s.matchingRepoConfigsBitbucketServer(id.projectKey, id.repositorySlug)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing pr:opened event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID)
		return nil
	}
	repoDir, err := s.checkoutForBitbucketServer(ctx, id.projectKey, id.repositorySlug, id.headSHA)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketServer(ctx, api, id, "pr:opened", err)
	}
	return s.runPlanAndCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, repoDir, ledgerDir)
}

// handlePullRequestSourceUpdatedBitbucketServer is core-flow step 3's
// automatic path: a real "pr:from_ref_updated" event (a new push to the
// PR's own source branch) re-runs `ubx plan`.
func (s *Server) handlePullRequestSourceUpdatedBitbucketServer(ctx context.Context, body []byte) error {
	var payload struct {
		PullRequest bitbucketv1.PullRequest `json:"pullRequest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode pr:from_ref_updated event: %w", err)
	}
	id, err := prIdentityFromBitbucketServer(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.bitbucketServerClient()

	candidates := s.matchingRepoConfigsBitbucketServer(id.projectKey, id.repositorySlug)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing pr:from_ref_updated event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID)
		return nil
	}
	repoDir, err := s.checkoutForBitbucketServer(ctx, id.projectKey, id.repositorySlug, id.headSHA)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketServer(ctx, api, id, "pr:from_ref_updated", err)
	}
	return s.runPlanAndCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, repoDir, ledgerDir)
}

// handlePullRequestMergedBitbucketServer is core-flow step 5's
// automatic path: a real "pr:merged" event -- a real, distinct eventKey
// Bitbucket Server only fires on an actual, completed merge (unlike
// Azure DevOps' own folded "git.pullrequest.merged", which fires
// regardless of outcome and needs a separate mergeStatus check, see
// handlePullRequestMergedAzureDevOps' own doc comment) -- runs `ubx
// ship --yes` checked out at the target branch.
func (s *Server) handlePullRequestMergedBitbucketServer(ctx context.Context, body []byte) error {
	if !s.cfg.ShipOnMerge {
		return nil
	}
	var payload struct {
		PullRequest bitbucketv1.PullRequest `json:"pullRequest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode pr:merged event: %w", err)
	}
	id, err := prIdentityFromBitbucketServer(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.bitbucketServerClient()

	candidates := s.matchingRepoConfigsBitbucketServer(id.projectKey, id.repositorySlug)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing pr:merged event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID)
		return nil
	}
	repoDir, err := s.checkoutForBitbucketServer(ctx, id.projectKey, id.repositorySlug, id.toRef)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketServer(ctx, api, id, "pr:merged", err)
	}
	return s.runAutomaticShipBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, repoDir, ledgerDir)
}

// refuseAmbiguousStackBitbucketServer is refuseAmbiguousStackGitHub's
// own Bitbucket Server counterpart.
func (s *Server) refuseAmbiguousStackBitbucketServer(ctx context.Context, api *bbserver.Client, id prIdentityBitbucketServer, event string, err error) error {
	slog.Error("ubx server: refusing event -- cannot resolve to exactly one discovered stack (UBI-167)",
		"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID, "event", event, "error", err)
	return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "plan",
		fmt.Sprintf("`ubx server` could not resolve this PR to exactly one stack: %s. Fix this repository's own `.ubx/config` layout, or run `ubx plan`/`ubx ship` locally instead.", err))
}

// bitbucketServerWebhookComment is the real, minimal shape of the
// "comment" field on a real "pr:comment:added" webhook payload --
// confirmed directly against Atlassian's own current docs' own real
// example JSON (id/version/text/author, the same real fields
// bitbucketserver.prComment already decodes from the REST API's own
// comment-list response, duplicated here rather than exported across
// the package boundary since this is a webhook-payload-specific
// decode, the same "each package owns its own wire-decode shape"
// convention azuredevops_webhook.go's own local structs already use).
type bitbucketServerWebhookComment struct {
	ID     int                       `json:"id"`
	Text   string                    `json:"text"`
	Author bitbucketv1.UserWithLinks `json:"author"`
}

// handlePullRequestCommentBitbucketServer is core-flow steps 3 (manual
// re-plan) and 4's manual path (ship via comment). Always re-fetches
// the PR fresh via GetPullRequest -- the comment event's own
// "pullRequest" resource field is confirmed real but not necessarily
// current by the time this handler actually runs, the identical real
// reasoning handleIssueCommentEvent/handleMergeCommentEvent/
// handlePullRequestCommentAzureDevOps already apply.
func (s *Server) handlePullRequestCommentBitbucketServer(ctx context.Context, body []byte) error {
	var payload struct {
		Comment     bitbucketServerWebhookComment `json:"comment"`
		PullRequest bitbucketv1.PullRequest       `json:"pullRequest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode pr:comment:added event: %w", err)
	}

	fields := strings.Fields(strings.TrimSpace(payload.Comment.Text))
	if len(fields) == 0 || fields[0] != "ubx" || len(fields) < 2 {
		return nil
	}
	verb, flags := fields[1], fields[2:]

	partialID, err := prIdentityFromBitbucketServer(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.bitbucketServerClient()

	pr, err := api.GetPullRequest(ctx, partialID.projectKey, partialID.repositorySlug, partialID.prID)
	if err != nil {
		return fmt.Errorf("get PR %d: %w", partialID.prID, err)
	}
	id, err := prIdentityFromBitbucketServer(pr)
	if err != nil {
		return err
	}

	candidates := s.matchingRepoConfigsBitbucketServer(id.projectKey, id.repositorySlug)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing pr:comment:added event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID, "verb", verb)
		return nil
	}
	repoDir, err := s.checkoutForBitbucketServer(ctx, id.projectKey, id.repositorySlug, id.headSHA)
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketServer(ctx, api, id, "comment:"+verb, err)
	}

	var creator bitbucketv1.UserWithLinks
	if pr.Author != nil {
		creator = pr.Author.User
	}
	commenter := payload.Comment.Author

	switch verb {
	case "plan":
		ok, err := isAuthorizedToReplanBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, s.cfg.BitbucketServerBotName, creator, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "plan",
				fmt.Sprintf("@%s isn't authorized to re-run `ubx plan` on this PR -- only its own creator (or, for a drift-watch-opened PR, a user with real, current REPO_WRITE-or-above access) can.", commenter.Name))
		}
		_ = flags
		return s.runPlanAndCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, repoDir, ledgerDir)

	case "ship":
		ok, err := isAuthorizedToShipBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "ship",
				fmt.Sprintf("@%s isn't a CODEOWNERS-listed owner of any file this PR changes -- not authorized to `ubx ship` it.", commenter.Name))
		}
		confirmDestroys := contains(flags, "--confirm-destroys")
		return s.runManualShipBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, repoDir, ledgerDir, confirmDestroys)
	}
	return nil
}

// handlePullRequestApprovedBitbucketServer is core-flow step 4's own
// trigger: a real "pr:reviewer:approved" event.
//
// Real, Bitbucket-Server-specific TOCTOU safety gate, structurally
// distinct from all three prior platforms: unlike GitHub's own
// review.commit_id (a separate REST field) or GitLab's project-wide
// ResetApprovalsOnPush setting or Azure DevOps' branch-policy fallback
// (no per-vote commit reference exists there at all), Bitbucket
// Server's own real "participant" object -- confirmed directly against
// the real, locally-downloaded go-bitbucket-v1 SDK's own
// UserWithMetadata.LastReviewedCommit field AND independently against
// Atlassian's own current real webhook payload example -- carries the
// exact commit SHA the approving reviewer actually reviewed, delivered
// in the SAME webhook payload as the PR's own current
// fromRef.latestCommit. No separate live query is needed at all: if
// the two don't match, a push landed after (or concurrently with) the
// approval, and this refuses outright with a real, explanatory posted
// comment -- the same "never trust a possibly-stale approval" property
// GitHub's own review.commit_id check already gives, achieved here
// with a genuinely different, Bitbucket-Server-specific real mechanism.
func (s *Server) handlePullRequestApprovedBitbucketServer(ctx context.Context, body []byte) error {
	var payload struct {
		PullRequest bitbucketv1.PullRequest      `json:"pullRequest"`
		Participant bitbucketv1.UserWithMetadata `json:"participant"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode pr:reviewer:approved event: %w", err)
	}
	id, err := prIdentityFromBitbucketServer(&payload.PullRequest)
	if err != nil {
		return err
	}
	api := s.bitbucketServerClient()
	approverName := payload.Participant.User.Name

	// UBI-166: same real allowlist gate as every other event handler
	// -- an approval on an unlisted repo must never derive acceptance
	// either. This flow calls core.AcceptFromReview directly against
	// repoDir (never against a resolved --ledger-dir), so only the
	// allowlist membership check applies here, the same real scoping
	// refuseAmbiguousStackGitHub's own doc comment explains for
	// GitHub's own approval flow.
	if len(s.matchingRepoConfigsBitbucketServer(id.projectKey, id.repositorySlug)) == 0 {
		slog.Error("ubx server: refusing pr:reviewer:approved event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketserver", "repo", id.projectKey+"/"+id.repositorySlug, "pr", id.prID)
		return nil
	}

	if payload.Participant.LastReviewedCommit == "" || id.headSHA == "" || payload.Participant.LastReviewedCommit != id.headSHA {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but @%s's own reviewed commit doesn't match this PR's own current head -- a new commit landed after the approval, so `ubx server` can't safely correlate it. Accept this proposal locally with `ubx accept` instead.", approverName))
	}

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	if err != nil {
		return fmt.Errorf("list changed files for PR %d: %w", id.prID, err)
	}
	proposalFile, err := discoverProposalFileBitbucketServer(changedFiles)
	if err != nil {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but `ubx` could not derive acceptance from it: %s. Accept this proposal locally with `ubx accept` instead.", err))
	}

	repoDir, err := s.repoDirForBitbucketServer(ctx, id.projectKey, id.repositorySlug)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, id.headSHA); err != nil {
		return err
	}

	fileBody, err := ghub.FileAtCommit(ctx, repoDir, id.headSHA, proposalFile)
	if err != nil {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but `ubx` could not read %s at the reviewed commit: %s", proposalFile, err))
	}

	claimedHash, ok := ghub.ParseProposalTrailer(payload.PullRequest.Description)
	if !ok {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			"Approval recorded by Bitbucket Server, but this PR's own description carries no `ubx-proposal: <hash>` trailer to derive acceptance from.")
	}

	var p core.Proposal
	if err := json.Unmarshal(fileBody, &p); err != nil {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but %s does not parse as a real proposal file: %s", proposalFile, err))
	}

	// A destructive proposal is never accepted from an approval, full
	// stop -- the identical real safety fix UBI-28 Phase 1/2/3 made for
	// GitHub/GitLab/Azure DevOps, applied here identically.
	if p.BlastRadius.Destroys > 0 {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but %s has blast_radius.destroys > 0 -- `ubx server` never derives acceptance for a destroy from an approval, regardless of configuration. Accept it locally with `ubx accept --confirm-destroys` instead.", proposalFile))
	}

	ledger := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "bitbucketserver",
		PRNumber:     int64(id.prID),
		ProposalFile: proposalFile,
		Approver:     approverName,
		// A Bitbucket Server approval carries no comment field at all,
		// the same real structural gap GitLab's/Azure DevOps' own
		// approvals have -- approving and leaving a comment are two
		// separate real actions here too.
		Comment: "",
	})
	if err != nil {
		return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Server, but `ubx accept` refused it: %s", err))
	}

	return postOrEditCommentBitbucketServer(ctx, api, id.projectKey, id.repositorySlug, id.prID, s.cfg.BitbucketServerBotName, "accept",
		fmt.Sprintf("accepted %s (stack %s) via PR #%d, approved by @%s", accepted.ID, accepted.Stack, id.prID, approverName))
}

// discoverProposalFileBitbucketServer is discoverProposalFile's (GitHub)
// own real counterpart, against the real, flat path list
// ListPullRequestChangedFiles already returns.
func discoverProposalFileBitbucketServer(changedFiles []string) (string, error) {
	var match string
	for _, path := range changedFiles {
		p := strings.TrimPrefix(path, "/")
		if strings.HasPrefix(p, "proposals/") && strings.HasSuffix(p, ".json") {
			if match != "" {
				return "", ErrAmbiguousProposalFileBitbucketServer
			}
			match = p
		}
	}
	if match == "" {
		return "", ErrAmbiguousProposalFileBitbucketServer
	}
	return match, nil
}

// bitbucketServerClient returns the server's own single, real, static
// Bitbucket Server client -- built once at New (server.go).
func (s *Server) bitbucketServerClient() *bbserver.Client {
	return s.bitbucketserver
}
