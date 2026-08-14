package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	ghub "github.com/ubiquex/ubiquex/github"
)

// installationEntry pairs one installation's *ghub.Client with its own
// underlying ghinstallation.Transport -- the client is what every other
// file in this package calls the real GitHub API through; the transport
// is kept alongside it because it's also the one thing that can hand
// back the current, real installation token as a plain string
// (Transport.Token), which repo.go's own git-over-HTTPS clone/fetch
// needs as a real credential, not an *http.Client.
type installationEntry struct {
	client    *ghub.Client
	transport *ghinstallation.Transport
}

// installationClients caches one installationEntry per GitHub App
// installation ID, each backed by its own ghinstallation.Transport (real,
// short-lived installation tokens, auto-refreshed before each expires --
// UBI-28's own "short-lived installation tokens" requirement, not a
// hand-rolled JWT-signing loop). Every webhook payload already carries
// its own Installation.ID directly (confirmed against go-github's own
// PullRequestEvent/IssueCommentEvent/PullRequestReviewEvent field docs),
// so the webhook path never needs a separate installation-listing call;
// the drift-watch loop (drift.go), which has no webhook payload to read
// one from, resolves it via appClient.FindRepositoryInstallation
// instead, the real GitHub API for exactly that lookup.
type installationClients struct {
	appID      int64
	privateKey []byte
	baseURL    string // test-only; empty means the real api.github.com

	// appClient is authenticated with the App-level JWT (ghinstallation.
	// AppsTransport), never an installation token -- the only real,
	// correct credential for an App-level endpoint like "find this
	// repo's own installation," which by definition happens before any
	// installation-scoped token exists to use instead.
	appClient *ghub.Client

	mu      sync.Mutex
	entries map[int64]installationEntry
}

func newInstallationClients(appID int64, privateKeyPath, baseURL string) (*installationClients, error) {
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read github app private key %s: %w", privateKeyPath, err)
	}

	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, key)
	if err != nil {
		return nil, fmt.Errorf("github app transport: %w", err)
	}
	var appOpts []ghub.Option
	if baseURL != "" {
		atr.BaseURL = baseURL
		appOpts = append(appOpts, ghub.WithBaseURL(baseURL))
	}

	return &installationClients{
		appID:      appID,
		privateKey: key,
		baseURL:    baseURL,
		appClient:  ghub.NewWithHTTPClient(&http.Client{Transport: atr}, appOpts...),
		entries:    make(map[int64]installationEntry),
	}, nil
}

// forInstallation returns the (cached, or newly built) client for
// installationID. Building one is cheap enough to not warrant a more
// elaborate cache (no eviction, no TTL) -- ghinstallation.Transport
// itself owns the real token refresh, this map only avoids re-signing a
// fresh App JWT on every single webhook delivery for the same
// installation.
func (ic *installationClients) forInstallation(installationID int64) (*ghub.Client, error) {
	e, err := ic.entryFor(installationID)
	if err != nil {
		return nil, err
	}
	return e.client, nil
}

func (ic *installationClients) entryFor(installationID int64) (installationEntry, error) {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if e, ok := ic.entries[installationID]; ok {
		return e, nil
	}

	itr, err := ghinstallation.New(http.DefaultTransport, ic.appID, installationID, ic.privateKey)
	if err != nil {
		return installationEntry{}, fmt.Errorf("github app installation transport (installation %d): %w", installationID, err)
	}
	if ic.baseURL != "" {
		itr.BaseURL = ic.baseURL
	}

	var opts []ghub.Option
	if ic.baseURL != "" {
		opts = append(opts, ghub.WithBaseURL(ic.baseURL))
	}
	e := installationEntry{
		client:    ghub.NewWithHTTPClient(&http.Client{Transport: itr}, opts...),
		transport: itr,
	}
	ic.entries[installationID] = e
	return e, nil
}

// installationIDFor resolves owner/repo's own real, current installation
// ID via the App-level API -- the drift-watch loop's own real need (see
// this type's own doc comment); the webhook path never calls this.
func (ic *installationClients) installationIDFor(ctx context.Context, owner, repo string) (int64, error) {
	inst, _, err := ic.appClient.API().Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return 0, fmt.Errorf("find installation for %s/%s: %w", owner, repo, err)
	}
	return inst.GetID(), nil
}

// installationToken returns installationID's own current, real,
// short-lived installation token as a plain string -- repo.go's own git
// credential for cloning/fetching over HTTPS (a git remote URL has no
// concept of an http.Client, only a literal token string).
func (ic *installationClients) installationToken(ctx context.Context, installationID int64) (string, error) {
	e, err := ic.entryFor(installationID)
	if err != nil {
		return "", err
	}
	return e.transport.Token(ctx)
}
