package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	ghapi "github.com/google/go-github/v78/github"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/vcs/github"
)

// ErrAmbiguousProposalFile means a PR's changed files contain zero, or
// more than one, file matching the real "proposals/*.json" convention
// every one of ubx's own CI/CD integration guides already documents --
// discoverProposalFile refuses to guess either way, the same "a wrong
// match here is a real security defect either direction" principle
// isAuthorizedToShip's own doc comment states for CODEOWNERS matching.
var ErrAmbiguousProposalFile = fmt.Errorf("could not identify exactly one proposals/*.json file in this PR's own changed files")

// webhookHandler implements http.Handler for the single real endpoint
// this server exposes -- GitHub's own webhook delivery target.
type webhookHandler struct {
	s *Server
}

func (h webhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload, err := ghapi.ValidatePayload(r, []byte(h.s.cfg.GitHubWebhookSecret))
	if err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := ghapi.ParseWebHook(ghapi.WebHookType(r), payload)
	if err != nil {
		http.Error(w, "unrecognized event", http.StatusBadRequest)
		return
	}

	// GitHub expects a fast 2xx response; the real work happens after
	// responding, same "near-stateless, kill and restart, re-derive"
	// framing as the rest of this package -- a delivery this process
	// happens to die mid-handling for is simply retried by GitHub's own
	// real webhook redelivery, not something this handler needs its own
	// separate durability story for.
	w.WriteHeader(http.StatusAccepted)

	go h.s.handleEvent(context.Background(), event)
}

// handleEvent dispatches a single, already-parsed webhook event to the
// real flow step it corresponds to (UBI-28's own "core flow", steps
// 1-6). Unrecognized/irrelevant event types and actions are silently
// ignored -- GitHub delivers many more event types than this server
// subscribes to caring about the payload of.
func (s *Server) handleEvent(ctx context.Context, event any) {
	var err error
	switch e := event.(type) {
	case *ghapi.PullRequestEvent:
		err = s.handlePullRequestEvent(ctx, e)
	case *ghapi.IssueCommentEvent:
		err = s.handleIssueCommentEvent(ctx, e)
	case *ghapi.PullRequestReviewEvent:
		err = s.handlePullRequestReviewEvent(ctx, e)
	default:
		return
	}
	if err != nil {
		slog.Error("ubx server: event handling failed", "error", err)
	}
}

// handlePullRequestEvent is core-flow steps 1 and 5 (the automatic
// half): opened/synchronize runs a plan; closed+merged runs a ship, if
// ShipOnMerge is on.
//
// UBI-166: refuses outright, loudly logged, if owner/repo has no real
// Config.Repos entry at all -- see allowlist.go's own doc comment for
// the real security gap this closes (before this fix, an unlisted
// repo silently defaulted to ledger_dir "." instead of being refused).
//
// UBI-167: once membership IS confirmed, the stack this event acts on
// comes from the repository's own checkout (resolveStackIn), never
// from a path declared in ubx server's own config, so the checkout
// happens here, before resolution, rather than inside each run*
// helper.
func (s *Server) handlePullRequestEvent(ctx context.Context, e *ghapi.PullRequestEvent) error {
	owner, repo := e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()
	installationID := e.GetInstallation().GetID()
	prNumber := e.GetNumber()
	action := e.GetAction()

	candidates := s.matchingRepoConfigsGitHub(owner, repo)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing pull_request event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "github", "repo", owner+"/"+repo, "action", action, "pr", prNumber)
		return nil
	}

	switch action {
	case "opened", "synchronize":
		api, err := s.installations.forInstallation(installationID)
		if err != nil {
			return err
		}
		repoDir, err := s.checkoutForGitHub(ctx, installationID, owner, repo, e.GetPullRequest().GetHead().GetSHA())
		if err != nil {
			return err
		}
		ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
			return changedFilePathsGitHub(ctx, api, owner, repo, prNumber)
		})
		if err != nil {
			return s.refuseAmbiguousStackGitHub(ctx, api, owner, repo, prNumber, "pull_request:"+action, err)
		}
		return s.runPlanAndComment(ctx, api, owner, repo, prNumber, repoDir, ledgerDir)

	case "closed":
		if !e.GetPullRequest().GetMerged() {
			return nil // closed without merging -- nothing to ship
		}
		if !s.cfg.ShipOnMerge {
			return nil
		}
		api, err := s.installations.forInstallation(installationID)
		if err != nil {
			return err
		}
		repoDir, err := s.checkoutForGitHub(ctx, installationID, owner, repo, e.GetPullRequest().GetBase().GetRef())
		if err != nil {
			return err
		}
		ledgerDir, err := resolveStackIn(repoDir, func() ([]string, error) {
			return changedFilePathsGitHub(ctx, api, owner, repo, prNumber)
		})
		if err != nil {
			return s.refuseAmbiguousStackGitHub(ctx, api, owner, repo, prNumber, "pull_request:closed(merged)", err)
		}
		return s.runAutomaticShip(ctx, api, owner, repo, prNumber, repoDir, ledgerDir)
	}
	return nil
}

// refuseAmbiguousStackGitHub is handlePullRequestEvent's/
// handleIssueCommentEvent's own shared real refusal path for
// ErrAmbiguousStack/ErrNoStackDiscovered (UBI-166, UBI-167): a loud,
// structured server-side log (the same discipline the plain
// unlisted-repo refusal above already has) AND a real, posted PR
// comment -- unlike an unlisted repo (never acted on, never commented
// on, since it was never opted into ubx server automation at all),
// this repo IS explicitly allowlisted; a human watching it deserves
// the same real, actionable feedback discoverProposalFile's own
// ambiguous-match refusal already gives. The fix now lives in the
// repository itself (add or disambiguate its own .ubx/config), not in
// ubx server's config, so the comment says that.
func (s *Server) refuseAmbiguousStackGitHub(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, event string, err error) error {
	slog.Error("ubx server: refusing event -- cannot resolve to exactly one discovered stack (UBI-167)",
		"platform", "github", "repo", owner+"/"+repo, "pr", prNumber, "event", event, "error", err)
	return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "plan",
		fmt.Sprintf("`ubx server` could not resolve this PR to exactly one stack: %s. Fix this repository's own `.ubx/config` layout, or run `ubx plan`/`ubx ship` locally instead.", err))
}

// handleIssueCommentEvent is core-flow steps 3 (re-plan) and 4's manual
// path (ship via comment) -- both require the real, live authorization
// checks auth.go implements, never inferred from the app's own
// installation scope.
//
// UBI-168: the authorization check runs BEFORE any clone or fetch. It
// used to run after, which meant an unauthorized commenter on an
// allowlisted repository could still make this server do the real work
// of cloning or fetching that repository, on every comment, before
// being told no. Nothing else about the flow changed: the same checks
// run against the same inputs and produce the same refusal comments,
// only the checkout now happens on the authorized path instead of
// ahead of the decision.
func (s *Server) handleIssueCommentEvent(ctx context.Context, e *ghapi.IssueCommentEvent) error {
	if e.GetAction() != "created" {
		return nil
	}
	if !e.GetIssue().IsPullRequest() {
		return nil // a plain issue comment, not a PR -- out of scope
	}

	body := strings.TrimSpace(e.GetComment().GetBody())
	fields := strings.Fields(body)
	if len(fields) == 0 || fields[0] != "ubx" || len(fields) < 2 {
		return nil // not a real ubx command comment at all
	}
	verb, flags := fields[1], fields[2:]

	owner, repo := e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()
	prNumber := e.GetIssue().GetNumber()
	commenter := e.GetComment().GetUser().GetLogin()
	installationID := e.GetInstallation().GetID()

	candidates := s.matchingRepoConfigsGitHub(owner, repo)
	if len(candidates) == 0 {
		slog.Error("ubx server: refusing issue_comment event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "github", "repo", owner+"/"+repo, "pr", prNumber, "verb", verb)
		return nil
	}

	api, err := s.installations.forInstallation(installationID)
	if err != nil {
		return err
	}

	pr, _, err := api.API().PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("get PR #%d on %s/%s: %w", prNumber, owner, repo, err)
	}

	switch verb {
	case "plan":
		ok, err := isAuthorizedToReplan(ctx, api, owner, repo, s.botLogin, pr.GetUser().GetLogin(), commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "plan",
				fmt.Sprintf("@%s isn't authorized to re-run `ubx plan` on this PR -- only its own creator (or, for a drift-watch-opened PR, a repository collaborator with write access) can.", commenter))
		}
		_ = flags // real, forwarded verbatim to the subprocess -- see runPlanAndComment
		repoDir, ledgerDir, err := s.prepareStackGitHub(ctx, api, installationID, owner, repo, prNumber, pr.GetHead().GetSHA(), "issue_comment:"+verb)
		if err != nil || repoDir == "" {
			return err
		}
		return s.runPlanAndComment(ctx, api, owner, repo, prNumber, repoDir, ledgerDir)

	case "ship":
		ok, err := isAuthorizedToShip(ctx, api, owner, repo, prNumber, commenter)
		if err != nil {
			return err
		}
		if !ok {
			return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "ship",
				fmt.Sprintf("@%s isn't a CODEOWNERS-listed owner of any file this PR changes -- not authorized to `ubx ship` it.", commenter))
		}
		confirmDestroys := contains(flags, "--confirm-destroys")
		repoDir, ledgerDir, err := s.prepareStackGitHub(ctx, api, installationID, owner, repo, prNumber, pr.GetHead().GetSHA(), "issue_comment:"+verb)
		if err != nil || repoDir == "" {
			return err
		}
		return s.runManualShip(ctx, api, owner, repo, prNumber, repoDir, ledgerDir, confirmDestroys)
	}
	return nil
}

// prepareStackGitHub checks the repository out at headSHA and resolves
// the one real stack the event acts on -- handleIssueCommentEvent's own
// post-authorization step (UBI-168), structurally identical to
// prepareStackBitbucketCloud, which was built this way from the start.
//
// Returns an empty repoDir with a nil error when the refusal has already
// been posted as a PR comment, so a caller can simply stop.
func (s *Server) prepareStackGitHub(ctx context.Context, api *ghub.Client, installationID int64, owner, repo string, prNumber int, headSHA, event string) (repoDir, ledgerDir string, err error) {
	repoDir, err = s.checkoutForGitHub(ctx, installationID, owner, repo, headSHA)
	if err != nil {
		return "", "", err
	}
	ledgerDir, err = resolveStackIn(repoDir, func() ([]string, error) {
		return changedFilePathsGitHub(ctx, api, owner, repo, prNumber)
	})
	if err != nil {
		return "", "", s.refuseAmbiguousStackGitHub(ctx, api, owner, repo, prNumber, event, err)
	}
	return repoDir, ledgerDir, nil
}

// handlePullRequestReviewEvent is core-flow step 4's own trigger: a real
// native Approve review, never a comment command (preserving the
// "derived signature, not a comment command" principle stated in this
// package's own doc comment).
func (s *Server) handlePullRequestReviewEvent(ctx context.Context, e *ghapi.PullRequestReviewEvent) error {
	if strings.ToLower(e.GetReview().GetState()) != "approved" {
		return nil
	}

	owner, repo := e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()
	prNumber := e.GetPullRequest().GetNumber()
	commitID := e.GetReview().GetCommitID()
	installationID := e.GetInstallation().GetID()

	// UBI-166: same real allowlist gate as handlePullRequestEvent/
	// handleIssueCommentEvent -- an Approve review on an unlisted repo
	// must never derive acceptance either. This particular flow calls
	// core.AcceptFromReview directly against repoDir (never against a
	// resolved --ledger-dir the way the plan/ship subprocess paths
	// do), so only the allowlist membership check applies here, not
	// the multi-stack resolution over auto-discovered stacks.
	if len(s.matchingRepoConfigsGitHub(owner, repo)) == 0 {
		slog.Error("ubx server: refusing pull_request_review event -- repository not in Config.Repos allowlist (UBI-166)",
			"platform", "github", "repo", owner+"/"+repo, "pr", prNumber)
		return nil
	}

	api, err := s.installations.forInstallation(installationID)
	if err != nil {
		return err
	}

	files, _, err := api.API().PullRequests.ListFiles(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("list changed files for PR #%d: %w", prNumber, err)
	}
	proposalFile, err := discoverProposalFile(files)
	if err != nil {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			fmt.Sprintf("Approval recorded by GitHub, but `ubx` could not derive acceptance from it: %s. Accept this proposal locally with `ubx accept` instead.", err))
	}

	repoDir, err := s.checkoutForGitHub(ctx, installationID, owner, repo, commitID)
	if err != nil {
		return err
	}

	body, err := ghub.FileAtCommit(ctx, repoDir, commitID, proposalFile)
	if err != nil {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			fmt.Sprintf("Approval recorded by GitHub, but `ubx` could not read %s at the reviewed commit: %s", proposalFile, err))
	}

	pr, _, err := api.API().PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("get PR #%d on %s/%s: %w", prNumber, owner, repo, err)
	}
	claimedHash, ok := ghub.ParseProposalTrailer(pr.GetBody())
	if !ok {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			"Approval recorded by GitHub, but this PR's own body carries no `ubx-proposal: <hash>` trailer to derive acceptance from.")
	}

	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			fmt.Sprintf("Approval recorded by GitHub, but %s does not parse as a real proposal file: %s", proposalFile, err))
	}

	// A destructive proposal is never accepted from a review, full stop --
	// not gated by Config.AllowDestroy, not satisfiable by any comment
	// flag the way runManualShip's own ship-time gate is. UBI-28's own
	// "approval is never sufficient by itself for a destroy" core safety
	// property applies at the earliest point it can: acceptance itself.
	// A human accepts a real destroy locally with `ubx accept
	// --confirm-destroys` (the same real, pre-existing acceptance-time
	// invariant `cli/accept.go`'s own checkDestroysConfirmed enforces for
	// the local and pr_merge tiers, docs/schema.md) -- never derived from
	// a review click here.
	if p.BlastRadius.Destroys > 0 {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			fmt.Sprintf("Approval recorded by GitHub, but %s has blast_radius.destroys > 0 -- `ubx server` never derives acceptance for a destroy from a review, regardless of configuration. Accept it locally with `ubx accept --confirm-destroys` instead.", proposalFile))
	}

	ledger := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "github",
		PRNumber:     int64(prNumber),
		ProposalFile: proposalFile,
		Approver:     e.GetReview().GetUser().GetLogin(),
		Comment:      e.GetReview().GetBody(),
	})
	if err != nil {
		return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
			fmt.Sprintf("Approval recorded by GitHub, but `ubx accept` refused it: %s", err))
	}

	return postOrEditComment(ctx, api, owner, repo, prNumber, s.botLogin, "accept",
		fmt.Sprintf("accepted %s (stack %s) via PR #%d, approved by @%s", accepted.ID, accepted.Stack, prNumber, e.GetReview().GetUser().GetLogin()))
}

// discoverProposalFile finds the one real proposal file among a PR's
// changed files -- files under a `proposals/` directory with a `.json`
// extension, the real, existing path convention every one of ubx's own
// CI/CD integration guides already documents (bamboo.mdx, azure-devops.
// mdx, circleci.mdx, etc. all use "proposals/db-replica.json"). Zero or
// more than one match is refused outright, never guessed at either way.
func discoverProposalFile(files []*ghapi.CommitFile) (string, error) {
	var match string
	for _, f := range files {
		path := f.GetFilename()
		if strings.HasPrefix(path, "proposals/") && strings.HasSuffix(path, ".json") {
			if match != "" {
				return "", ErrAmbiguousProposalFile
			}
			match = path
		}
	}
	if match == "" {
		return "", ErrAmbiguousProposalFile
	}
	return match, nil
}

func contains(fields []string, target string) bool {
	for _, f := range fields {
		if f == target {
			return true
		}
	}
	return false
}
