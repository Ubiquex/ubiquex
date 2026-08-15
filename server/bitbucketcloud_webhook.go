package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	bbcloud "github.com/ubiquex/ubiquex/bitbucketcloud"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
)

// ErrAmbiguousProposalFileBitbucketCloud is webhook.go's own
// ErrAmbiguousProposalFile, Bitbucket Cloud's real counterpart.
var ErrAmbiguousProposalFileBitbucketCloud = fmt.Errorf("could not identify exactly one proposals/*.json file in this pull request's own changed files")

// bitbucketCloudWebhookHandler implements http.Handler for Bitbucket
// Cloud's own real webhook delivery target -- the fifth real route this
// server exposes, and a genuinely separate integration from Bitbucket
// Server's despite the shared vendor (UBI-170).
//
// Real, load-bearing differences from bitbucketServerWebhookHandler,
// each confirmed against Bitbucket Cloud's own current, official API
// definition rather than assumed from Bitbucket Server:
//
//   - Every event key differs. Bitbucket Cloud uses pullrequest:created,
//     pullrequest:push, pullrequest:approved, pullrequest:fulfilled
//     (its own word for merged), and pullrequest:comment_created.
//     Bitbucket Server's pr:opened/pr:from_ref_updated/
//     pr:reviewer:approved/pr:merged/pr:comment:added do not exist here.
//   - Bitbucket Cloud splits a push to the source branch
//     (pullrequest:push) from every other pull request update
//     (pullrequest:updated), so no "was this really a code change"
//     disambiguation is needed the way GitLab's own folded update event
//     requires.
//   - The event key arrives in the delivery's own X-Event-Key request
//     header, not in the payload body the way Bitbucket Server's own
//     eventKey field does. This is the one detail in this file that
//     comes from Atlassian's webhook documentation rather than from the
//     machine-readable API definition, so it is deliberately handled to
//     fail CLOSED: an absent or unrecognized event key is ignored
//     entirely and nothing runs, which is a no-op rather than a wrong
//     action if Atlassian ever renames it.
//
// The payload itself is trusted for as little as possible: repository
// identity, pull request id, the acting account, and comment text. Every
// other fact (head commit, target branch, description, participants,
// changed files) is re-fetched from the API, so this handler never
// depends on payload fields the machine-readable definition does not
// describe.
type bitbucketCloudWebhookHandler struct {
	s *Server
}

func (h bitbucketCloudWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validBitbucketCloudSignature(h.s.cfg.BitbucketCloudWebhookSecret, body, r) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Same real "respond fast, do the real work after" reasoning as
	// every other platform's own ServeHTTP in this package.
	w.WriteHeader(http.StatusAccepted)

	go h.s.handleBitbucketCloudEvent(context.Background(), r.Header.Get("X-Event-Key"), body)
}

// bitbucketCloudEvent is the real, minimal slice of a Bitbucket Cloud
// webhook payload this server reads. See the handler's own doc comment
// for why it is deliberately this small.
type bitbucketCloudEvent struct {
	Actor      bbcloud.Account `json:"actor"`
	Repository struct {
		// FullName is "workspace/repo_slug", the exact pair every real
		// API path needs. Deliberately used in preference to the
		// separate name field, which is a display name and is NOT the
		// slug: a repository displayed as "Payments Infra" has a slug
		// like "payments-infra", and addressing the API with the former
		// would 404 on every call.
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		ID int64 `json:"id"`
	} `json:"pullrequest"`
	Comment struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	} `json:"comment"`
}

// identity splits the payload's own repository full name into the real
// workspace and repository slug pair.
func (e *bitbucketCloudEvent) identity() (workspace, repoSlug string, ok bool) {
	workspace, repoSlug, ok = strings.Cut(e.Repository.FullName, "/")
	return workspace, repoSlug, ok && workspace != "" && repoSlug != ""
}

func (s *Server) handleBitbucketCloudEvent(ctx context.Context, eventKey string, body []byte) {
	var e bitbucketCloudEvent
	if err := json.Unmarshal(body, &e); err != nil {
		slog.Error("ubx server: bitbucket cloud event handling failed", "error", fmt.Errorf("decode %s event: %w", eventKey, err))
		return
	}

	var err error
	switch eventKey {
	case "pullrequest:created", "pullrequest:push":
		err = s.handlePullRequestPlanBitbucketCloud(ctx, &e, eventKey)
	case "pullrequest:approved":
		err = s.handlePullRequestApprovedBitbucketCloud(ctx, &e)
	case "pullrequest:fulfilled":
		err = s.handlePullRequestMergedBitbucketCloud(ctx, &e)
	case "pullrequest:comment_created":
		err = s.handlePullRequestCommentBitbucketCloud(ctx, &e)
	default:
		return
	}
	if err != nil {
		slog.Error("ubx server: bitbucket cloud event handling failed", "error", err)
	}
}

// handlePullRequestPlanBitbucketCloud is core-flow step 1: a real
// pullrequest:created or pullrequest:push event runs `ubx plan`.
//
// UBI-166: refuses outright, loudly logged, if the repository has no
// real Config.Repos entry at all. UBI-167: the stack comes from the
// repository's own checkout, so the checkout happens before resolution.
func (s *Server) handlePullRequestPlanBitbucketCloud(ctx context.Context, e *bitbucketCloudEvent, eventKey string) error {
	workspace, repoSlug, ok := e.identity()
	if !ok {
		return fmt.Errorf("%s event carries no real repository full_name", eventKey)
	}
	if len(s.matchingRepoConfigsBitbucketCloud(workspace, repoSlug)) == 0 {
		slog.Error("ubx server: refusing "+eventKey+" event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketcloud", "repo", workspace+"/"+repoSlug, "pr", e.PullRequest.ID)
		return nil
	}
	api := s.bitbucketCloudClient()

	pr, err := api.GetPullRequest(ctx, workspace, repoSlug, e.PullRequest.ID)
	if err != nil {
		return err
	}
	repoDir, err := s.checkoutForBitbucketCloud(ctx, workspace, repoSlug, pr.HeadSHA())
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, workspace, repoSlug, pr.ID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, eventKey, err)
	}
	return s.runPlanAndCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, repoDir, ledgerDir)
}

// handlePullRequestMergedBitbucketCloud is core-flow step 4's automatic
// path: a real pullrequest:fulfilled event (Bitbucket Cloud's own word
// for merged) runs `ubx ship --yes` checked out at the target branch.
func (s *Server) handlePullRequestMergedBitbucketCloud(ctx context.Context, e *bitbucketCloudEvent) error {
	if !s.cfg.ShipOnMerge {
		return nil
	}
	workspace, repoSlug, ok := e.identity()
	if !ok {
		return fmt.Errorf("pullrequest:fulfilled event carries no real repository full_name")
	}
	if len(s.matchingRepoConfigsBitbucketCloud(workspace, repoSlug)) == 0 {
		slog.Error("ubx server: refusing pullrequest:fulfilled event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketcloud", "repo", workspace+"/"+repoSlug, "pr", e.PullRequest.ID)
		return nil
	}
	api := s.bitbucketCloudClient()

	pr, err := api.GetPullRequest(ctx, workspace, repoSlug, e.PullRequest.ID)
	if err != nil {
		return err
	}
	repoDir, err := s.checkoutForBitbucketCloud(ctx, workspace, repoSlug, pr.TargetBranch())
	if err != nil {
		return err
	}
	ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, workspace, repoSlug, pr.ID)
	})
	if err != nil {
		return s.refuseAmbiguousStackBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, "pullrequest:fulfilled", err)
	}
	return s.runAutomaticShipBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, repoDir, ledgerDir)
}

// refuseAmbiguousStackBitbucketCloud is refuseAmbiguousStackGitHub's own
// Bitbucket Cloud counterpart.
func (s *Server) refuseAmbiguousStackBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, prID int64, event string, err error) error {
	slog.Error("ubx server: refusing event -- cannot resolve to exactly one discovered stack (UBI-167)",
		"platform", "bitbucketcloud", "repo", workspace+"/"+repoSlug, "pr", prID, "event", event, "error", err)
	return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, prID, s.cfg.BitbucketCloudBotAccountID, "plan",
		fmt.Sprintf("`ubx server` could not resolve this pull request to exactly one stack: %s. Fix this repository's own `.ubx/config` layout, or run `ubx plan`/`ubx ship` locally instead.", err))
}

// handlePullRequestCommentBitbucketCloud is core-flow steps 2 (manual
// re-plan) and 4's manual path (ship via comment).
//
// UBI-168, applied here from the start rather than inherited: the real,
// live authorization check runs BEFORE any clone or fetch. On the four
// older platforms the stack resolution (and therefore the checkout)
// happens first, which means an unauthorized commenter on an allowlisted
// repository can still make the server do real work. That ordering is a
// known defect tracked separately; this platform does not repeat it.
func (s *Server) handlePullRequestCommentBitbucketCloud(ctx context.Context, e *bitbucketCloudEvent) error {
	fields := strings.Fields(strings.TrimSpace(e.Comment.Content.Raw))
	if len(fields) == 0 || fields[0] != "ubx" || len(fields) < 2 {
		return nil
	}
	verb, flags := fields[1], fields[2:]

	workspace, repoSlug, ok := e.identity()
	if !ok {
		return fmt.Errorf("pullrequest:comment_created event carries no real repository full_name")
	}
	if len(s.matchingRepoConfigsBitbucketCloud(workspace, repoSlug)) == 0 {
		slog.Error("ubx server: refusing pullrequest:comment_created event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketcloud", "repo", workspace+"/"+repoSlug, "pr", e.PullRequest.ID, "verb", verb)
		return nil
	}
	api := s.bitbucketCloudClient()
	commenter := e.Actor

	// Always re-fetch the pull request fresh: the payload's own copy is
	// real but not necessarily current by the time this handler runs,
	// the identical reasoning every other platform's own comment
	// handler already applies.
	pr, err := api.GetPullRequest(ctx, workspace, repoSlug, e.PullRequest.ID)
	if err != nil {
		return err
	}

	switch verb {
	case "plan":
		ok, err := isAuthorizedToReplanBitbucketCloud(ctx, api, workspace, repoSlug, s.cfg.BitbucketCloudBotAccountID, pr.Author, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "plan",
				fmt.Sprintf("@%s isn't authorized to re-run `ubx plan` on this pull request -- only its own creator (or, for a drift-watch-opened pull request, a user with real, current write-or-above access) can.", commenter.Label()))
		}
		_ = flags
		repoDir, ledgerDir, err := s.prepareStackBitbucketCloud(ctx, api, workspace, repoSlug, pr, "comment:"+verb, pr.HeadSHA())
		if err != nil || repoDir == "" {
			return err
		}
		return s.runPlanAndCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, repoDir, ledgerDir)

	case "ship":
		ok, err := isAuthorizedToShipBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, pr.HeadSHA(), commenter)
		if err != nil {
			// A CODEOWNERS file ubx cannot resolve is a real,
			// actionable defect in the repository, so it is reported
			// back rather than surfacing as a plain "not authorized".
			return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "ship",
				fmt.Sprintf("`ubx server` could not decide `ubx ship` authorization for this pull request: %s", err))
		}
		if !ok {
			return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "ship",
				fmt.Sprintf("@%s isn't a CODEOWNERS-listed owner of any file this pull request changes -- not authorized to `ubx ship` it.", commenter.Label()))
		}
		confirmDestroys := contains(flags, "--confirm-destroys")
		repoDir, ledgerDir, err := s.prepareStackBitbucketCloud(ctx, api, workspace, repoSlug, pr, "comment:"+verb, pr.HeadSHA())
		if err != nil || repoDir == "" {
			return err
		}
		return s.runManualShipBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, repoDir, ledgerDir, confirmDestroys)
	}
	return nil
}

// prepareStackBitbucketCloud checks the repository out at ref and
// resolves the one real stack the event acts on. Returns an empty
// repoDir (with a nil error) when the refusal has already been posted,
// so a caller can simply stop.
func (s *Server) prepareStackBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, pr *bbcloud.PullRequest, event, ref string) (repoDir, ledgerDir string, err error) {
	repoDir, err = s.checkoutForBitbucketCloud(ctx, workspace, repoSlug, ref)
	if err != nil {
		return "", "", err
	}
	ledgerDir, err = resolveStackIn(repoDir, func() ([]string, error) {
		return api.ListPullRequestChangedFiles(ctx, workspace, repoSlug, pr.ID)
	})
	if err != nil {
		return "", "", s.refuseAmbiguousStackBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, event, err)
	}
	return repoDir, ledgerDir, nil
}

// handlePullRequestApprovedBitbucketCloud is core-flow step 3's own
// trigger: a real pullrequest:approved event, never a comment.
//
// Real, Bitbucket-Cloud-specific TOCTOU safety gate, and structurally
// the GitLab/Azure DevOps pattern rather than Bitbucket Server's. A
// Bitbucket Cloud participant object carries NO commit reference at all
// (confirmed against both the official schema and the live API), so
// there is nothing to compare a head commit against the way Bitbucket
// Server's own per-approval lastReviewedCommit allows. Instead this
// checks the repository's own real branch restrictions for a rule that
// clears approvals when new changes land on the pull request, scoped to
// this pull request's own target branch, and refuses to derive
// acceptance at all when no such rule applies: without it, a
// currently-recorded approval cannot be correlated with any particular
// commit.
func (s *Server) handlePullRequestApprovedBitbucketCloud(ctx context.Context, e *bitbucketCloudEvent) error {
	workspace, repoSlug, ok := e.identity()
	if !ok {
		return fmt.Errorf("pullrequest:approved event carries no real repository full_name")
	}
	// UBI-166: same real allowlist gate as every other event handler.
	// This flow calls core.AcceptFromReview directly against repoDir
	// (never against a resolved --ledger-dir), so only the allowlist
	// membership check applies here, the same real scoping
	// refuseAmbiguousStackGitHub's own doc comment explains.
	if len(s.matchingRepoConfigsBitbucketCloud(workspace, repoSlug)) == 0 {
		slog.Error("ubx server: refusing pullrequest:approved event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "bitbucketcloud", "repo", workspace+"/"+repoSlug, "pr", e.PullRequest.ID)
		return nil
	}
	api := s.bitbucketCloudClient()
	approver := e.Actor

	pr, err := api.GetPullRequest(ctx, workspace, repoSlug, e.PullRequest.ID)
	if err != nil {
		return err
	}

	resets, err := api.ResetApprovalsOnChange(ctx, workspace, repoSlug, pr.TargetBranch())
	if err != nil {
		return fmt.Errorf("check reset-approvals-on-change branch restriction: %w", err)
	}
	if !resets {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			"Approval recorded by Bitbucket Cloud, but this branch has no real, current branch restriction that resets pull request approvals when new changes land, and a Bitbucket Cloud approval carries no commit reference of its own -- so `ubx server` can't safely correlate this approval with a specific commit. Add a \"Reset pull request approvals when the source branch is modified\" branch restriction, or accept this proposal locally with `ubx accept` instead.")
	}

	headSHA := pr.HeadSHA()
	if headSHA == "" {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			"Approval recorded by Bitbucket Cloud, but `ubx` could not resolve this pull request's own current head commit.")
	}

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, workspace, repoSlug, pr.ID)
	if err != nil {
		return fmt.Errorf("list changed files for pull request %d: %w", pr.ID, err)
	}
	proposalFile, err := discoverProposalFileBitbucketCloud(changedFiles)
	if err != nil {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Cloud, but `ubx` could not derive acceptance from it: %s. Accept this proposal locally with `ubx accept` instead.", err))
	}

	repoDir, err := s.checkoutForBitbucketCloud(ctx, workspace, repoSlug, headSHA)
	if err != nil {
		return err
	}

	fileBody, err := ghub.FileAtCommit(ctx, repoDir, headSHA, proposalFile)
	if err != nil {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Cloud, but `ubx` could not read %s at the reviewed commit: %s", proposalFile, err))
	}

	claimedHash, ok := ghub.ParseProposalTrailer(pr.Description)
	if !ok {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			"Approval recorded by Bitbucket Cloud, but this pull request's own description carries no `ubx-proposal: <hash>` trailer to derive acceptance from.")
	}

	var p core.Proposal
	if err := json.Unmarshal(fileBody, &p); err != nil {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Cloud, but %s does not parse as a real proposal file: %s", proposalFile, err))
	}

	// A destructive proposal is never accepted from an approval, full
	// stop -- the identical real safety property UBI-28 Phases 1 to 4
	// made for GitHub/GitLab/Azure DevOps/Bitbucket Server, applied here
	// identically.
	if p.BlastRadius.Destroys > 0 {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Cloud, but %s has blast_radius.destroys > 0 -- `ubx server` never derives acceptance for a destroy from an approval, regardless of configuration. Accept it locally with `ubx accept --confirm-destroys` instead.", proposalFile))
	}

	ledger := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "bitbucketcloud",
		PRNumber:     pr.ID,
		ProposalFile: proposalFile,
		// The approver is recorded by account_id, the one stable
		// identifier Bitbucket Cloud has. nickname and display_name are
		// both user-changeable, and an append-only ledger entry naming
		// a mutable label would stop meaning anything the moment its
		// owner renamed themselves.
		Approver: approver.Identity(),
		// A Bitbucket Cloud approval carries no comment field at all,
		// the same real structural gap GitLab's/Azure DevOps'/Bitbucket
		// Server's own approvals have.
		Comment: "",
	})
	if err != nil {
		return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
			fmt.Sprintf("Approval recorded by Bitbucket Cloud, but `ubx accept` refused it: %s", err))
	}

	return postOrEditCommentBitbucketCloud(ctx, api, workspace, repoSlug, pr.ID, s.cfg.BitbucketCloudBotAccountID, "accept",
		fmt.Sprintf("accepted %s (stack %s) via pull request #%d, approved by @%s", accepted.ID, accepted.Stack, pr.ID, approver.Label()))
}

// discoverProposalFileBitbucketCloud is discoverProposalFile's (GitHub)
// own real counterpart, against the real, flat path list
// ListPullRequestChangedFiles already returns.
func discoverProposalFileBitbucketCloud(changedFiles []string) (string, error) {
	var match string
	for _, path := range changedFiles {
		p := strings.TrimPrefix(path, "/")
		if strings.HasPrefix(p, "proposals/") && strings.HasSuffix(p, ".json") {
			if match != "" {
				return "", ErrAmbiguousProposalFileBitbucketCloud
			}
			match = p
		}
	}
	if match == "" {
		return "", ErrAmbiguousProposalFileBitbucketCloud
	}
	return match, nil
}

// bitbucketCloudClient returns the server's own single, real, static
// Bitbucket Cloud client -- built once at New (server.go).
func (s *Server) bitbucketCloudClient() *bbcloud.Client {
	return s.bitbucketcloud
}
