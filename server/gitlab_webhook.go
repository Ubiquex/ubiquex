package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
	glab "github.com/ubiquex/ubiquex/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// ErrAmbiguousProposalFileGitLab is webhook.go's own
// ErrAmbiguousProposalFile, GitLab's real counterpart.
var ErrAmbiguousProposalFileGitLab = fmt.Errorf("could not identify exactly one proposals/*.json file in this MR's own changed files")

// gitlabWebhookHandler implements http.Handler for GitLab's own real
// webhook delivery target -- webhookHandler's (GitHub) own real
// counterpart, mounted at a separate real route (server.go) since
// GitLab has no per-installation payload to dispatch on the way every
// GitHub event already carries one.
type gitlabWebhookHandler struct {
	s *Server
}

func (h gitlabWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validGitLabSignature(h.s.cfg.GitLabWebhookSecret, r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := glapi.ParseWebhook(glapi.WebhookEventType(r), body)
	if err != nil {
		http.Error(w, "unrecognized event", http.StatusBadRequest)
		return
	}

	// Same real "respond fast, do the real work after" reasoning as
	// webhookHandler's own ServeHTTP (GitHub) -- GitLab redelivers a
	// webhook its own endpoint doesn't acknowledge quickly, so this
	// process dying mid-handling isn't this handler's own durability
	// problem to solve.
	w.WriteHeader(http.StatusAccepted)

	go h.s.handleGitLabEvent(context.Background(), event)
}

func (s *Server) handleGitLabEvent(ctx context.Context, event any) {
	var err error
	switch e := event.(type) {
	case *glapi.MergeEvent:
		err = s.handleMergeEvent(ctx, e)
	case *glapi.MergeCommentEvent:
		err = s.handleMergeCommentEvent(ctx, e)
	default:
		return
	}
	if err != nil {
		slog.Error("ubx server: gitlab event handling failed", "error", err)
	}
}

// handleMergeEvent is core-flow steps 1, 4, and 5 for GitLab: open/a
// real code-change update runs a plan; approval derives acceptance;
// merge ships automatically.
func (s *Server) handleMergeEvent(ctx context.Context, e *glapi.MergeEvent) error {
	project := e.Project.PathWithNamespace
	mrIID := e.ObjectAttributes.IID
	api, err := s.gitlabClient()
	if err != nil {
		return err
	}

	switch e.ObjectAttributes.Action {
	case "open":
		return s.runPlanAndCommentGitLab(ctx, api, project, mrIID, e.ObjectAttributes.LastCommit.ID)

	case "update":
		// GitLab folds "new commit pushed" and every other real MR
		// update (title, description, labels, ...) into one real
		// "update" action -- confirmed directly against GitLab's own
		// current webhook_events docs, not assumed split the way
		// GitHub's separate synchronize/edited actions are. OldRev is
		// only populated on a real code-change update, so that's the
		// real, correct signal to re-plan on, not "update" alone.
		if e.ObjectAttributes.OldRev == "" {
			return nil
		}
		return s.runPlanAndCommentGitLab(ctx, api, project, mrIID, e.ObjectAttributes.LastCommit.ID)

	case "approval":
		// Real, deliberate choice: "approval" (an individual user's own
		// approve action) is used here, not "approved" (GitLab's own
		// separate action fired only once every required approver has
		// approved) -- the same real parity GitHub's own tier already
		// has, where any single Approve review derives acceptance,
		// never gated on a required-approvers count being satisfied
		// first. Enforcing "this needed N approvals" stays entirely
		// GitLab's own job (merge request approval rules), never
		// ubx's, the same principle every other platform's own tier
		// already applies.
		return s.handleGitLabApproval(ctx, api, project, mrIID, e.User.Username)

	case "merge":
		if !s.cfg.ShipOnMerge {
			return nil
		}
		return s.runAutomaticShipGitLab(ctx, api, project, mrIID, e.ObjectAttributes.TargetBranch)
	}
	return nil
}

// handleMergeCommentEvent is core-flow steps 3 (re-plan) and 4's manual
// path (ship via note) for GitLab.
func (s *Server) handleMergeCommentEvent(ctx context.Context, e *glapi.MergeCommentEvent) error {
	if e.ObjectAttributes.System {
		return nil // a real, GitLab-generated system note (e.g. "changed the description"), never a real command
	}

	body := strings.TrimSpace(e.ObjectAttributes.Note)
	fields := strings.Fields(body)
	if len(fields) == 0 || fields[0] != "ubx" || len(fields) < 2 {
		return nil
	}
	verb, flags := fields[1], fields[2:]

	project := e.Project.PathWithNamespace
	mrIID := e.MergeRequest.IID
	commenter := e.User.Username

	api, err := s.gitlabClient()
	if err != nil {
		return err
	}

	mr, _, err := api.API().MergeRequests.GetMergeRequest(project, mrIID, nil, glapi.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("get MR !%d on %s: %w", mrIID, project, err)
	}

	switch verb {
	case "plan":
		ok, err := isAuthorizedToReplanGitLab(ctx, api, project, s.cfg.GitLabBotUsername, mr.Author.Username, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "plan",
				fmt.Sprintf("@%s isn't authorized to re-run `ubx plan` on this MR -- only its own creator (or, for a drift-watch-opened MR, a project member with real, current Developer access or higher) can.", commenter))
		}
		_ = flags
		return s.runPlanAndCommentGitLab(ctx, api, project, mrIID, mr.SHA)

	case "ship":
		ok, err := isAuthorizedToShipGitLab(ctx, api, project, mrIID, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "ship",
				fmt.Sprintf("@%s isn't a CODEOWNERS-listed owner of any file this MR changes -- not authorized to `ubx ship` it.", commenter))
		}
		confirmDestroys := contains(flags, "--confirm-destroys")
		return s.runManualShipGitLab(ctx, api, project, mrIID, mr.SHA, confirmDestroys)
	}
	return nil
}

// handleGitLabApproval is core-flow step 4's own trigger for GitLab:
// a real Approve action, never a comment.
//
// Real, GitLab-specific TOCTOU safety gate, structurally different
// from GitHub's own (which reads the proposal at the review's own
// commit_id specifically): GitLab's approvals API carries no per-
// approval commit reference at all (confirmed directly against the
// real, downloaded go-gitlab module -- MergeRequestApprovals has no
// such field). Instead, GitLab's own real default behavior -- clearing
// every existing approval outright when a new commit lands on the MR's
// source branch (reset_approvals_on_push) -- already guarantees a
// currently-recorded approval corresponds to the MR's own current head
// commit, as long as that project setting is genuinely on. This
// function checks that setting fresh, every time, and refuses to
// derive acceptance at all if it's off -- there would be no real way
// left to correlate the approval with any specific commit.
func (s *Server) handleGitLabApproval(ctx context.Context, api *glab.Client, project string, mrIID int64, approverUsername string) error {
	approvalCfg, _, err := api.API().Projects.GetApprovalConfiguration(project, glapi.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("get approval configuration for %s: %w", project, err)
	}
	if !approvalCfg.ResetApprovalsOnPush {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			"Approval recorded by GitLab, but this project's own reset_approvals_on_push setting is off -- ubx server can't safely correlate an approval with a specific commit here. Turn it on, or accept this proposal locally with `ubx accept` instead.")
	}

	mr, _, err := api.API().MergeRequests.GetMergeRequest(project, mrIID, nil, glapi.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("get MR !%d on %s: %w", mrIID, project, err)
	}
	headSHA := mr.SHA

	diffs, _, err := api.API().MergeRequests.ListMergeRequestDiffs(project, mrIID, nil, glapi.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("list changed files for MR !%d: %w", mrIID, err)
	}
	proposalFile, err := discoverProposalFileGitLab(diffs)
	if err != nil {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			fmt.Sprintf("Approval recorded by GitLab, but `ubx` could not derive acceptance from it: %s. Accept this proposal locally with `ubx accept` instead.", err))
	}

	repoDir, err := s.repoDirForGitLab(ctx, project)
	if err != nil {
		return err
	}
	if err := checkoutRef(ctx, repoDir, headSHA); err != nil {
		return err
	}

	body, err := ghub.FileAtCommit(ctx, repoDir, headSHA, proposalFile)
	if err != nil {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			fmt.Sprintf("Approval recorded by GitLab, but `ubx` could not read %s at the reviewed commit: %s", proposalFile, err))
	}

	claimedHash, ok := ghub.ParseProposalTrailer(mr.Description)
	if !ok {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			"Approval recorded by GitLab, but this MR's own description carries no `ubx-proposal: <hash>` trailer to derive acceptance from.")
	}

	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			fmt.Sprintf("Approval recorded by GitLab, but %s does not parse as a real proposal file: %s", proposalFile, err))
	}

	// A destructive proposal is never accepted from an approval, full
	// stop -- the identical real safety fix UBI-28 Phase 1 made for
	// GitHub (found during that phase's own adversarial review:
	// core.AcceptFromReview enforces no confirm-destroys invariant of
	// its own; that check has only ever lived in cli/accept.go, which
	// a direct core caller like this handler bypasses entirely unless
	// it adds the same real check itself, applied here identically).
	if p.BlastRadius.Destroys > 0 {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			fmt.Sprintf("Approval recorded by GitLab, but %s has blast_radius.destroys > 0 -- `ubx server` never derives acceptance for a destroy from an approval, regardless of configuration. Accept it locally with `ubx accept --confirm-destroys` instead.", proposalFile))
	}

	repo := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(repo, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "gitlab",
		PRNumber:     mrIID,
		ProposalFile: proposalFile,
		Approver:     approverUsername,
		// GitLab's own real Approve action carries no comment field at
		// all -- structurally different from GitHub's review, which
		// can carry approval state and a body together in one action.
		// Approving and leaving a note are two separate real actions
		// in GitLab, confirmed directly against its own current
		// Merge Request Approvals API, not assumed symmetric with
		// GitHub.
		Comment: "",
	})
	if err != nil {
		return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
			fmt.Sprintf("Approval recorded by GitLab, but `ubx accept` refused it: %s", err))
	}

	return postOrEditCommentGitLab(ctx, api, project, mrIID, s.cfg.GitLabBotUsername, "accept",
		fmt.Sprintf("accepted %s (stack %s) via MR !%d, approved by @%s", accepted.ID, accepted.Stack, mrIID, approverUsername))
}

// discoverProposalFileGitLab is discoverProposalFile's (GitHub) own
// real counterpart, against GitLab's own real MergeRequestDiff shape.
func discoverProposalFileGitLab(diffs []*glapi.MergeRequestDiff) (string, error) {
	var match string
	for _, d := range diffs {
		path := d.NewPath
		if path == "" {
			path = d.OldPath
		}
		if strings.HasPrefix(path, "proposals/") && strings.HasSuffix(path, ".json") {
			if match != "" {
				return "", ErrAmbiguousProposalFileGitLab
			}
			match = path
		}
	}
	if match == "" {
		return "", ErrAmbiguousProposalFileGitLab
	}
	return match, nil
}

// gitlabClient returns the server's own single, real, static GitLab
// client -- built once at New (server.go), never rebuilt per event
// the way GitHub's own per-installation client is, since GitLab has no
// per-installation token of its own to resolve.
func (s *Server) gitlabClient() (*glab.Client, error) {
	if s.gitlab == nil {
		return nil, fmt.Errorf("gitlab support is not configured (missing --gitlab-token)")
	}
	return s.gitlab, nil
}
