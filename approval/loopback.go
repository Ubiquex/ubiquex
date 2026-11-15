package approval

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// Loopback is the approval page `ubx mcp` serves on the loopback interface
// of its own machine (UBI-295).
//
// What it attests is presence: someone at this machine, with a browser,
// clicked approve. It records nothing about who, and the acceptance it
// writes says so (approver "loopback:presence").
//
// The page shows the stored render of the proposal and nothing the agent
// wrote outside it. Opening it stages the render (Ledger.StageRender:
// render, store, read back), and the page shows the bytes that were read
// back, so what the human sees is by construction what the ledger keeps.
// Approving submits the hash of those bytes, and core.AcceptFromLoopback
// refuses unless they are stored and are a render of this exact proposal.
//
// What it defends against, and what it does not:
//
//   - Another web page in the human's browser. Approval is a POST that must
//     carry an Origin naming this server, a same-origin Sec-Fetch-Site when
//     the browser sends one, and a token only the page itself carries: an
//     HMAC under a key generated per server process, never handed to the
//     agent. Cross-site form posts and fetches fail all three.
//   - DNS rebinding. Every request must name this listener in its Host
//     header, as an IP or as localhost.
//   - Framing. The page forbids being framed, so it cannot be clickjacked.
//   - An agent with a shell on this machine is not stopped by any of this:
//     it can fetch the page and post the form like a browser does. The
//     claim is scoped accordingly. A change made through ubx carries a
//     record of what the approver saw; whether an agent can reach around
//     ubx is a question about what else it was given.
type Loopback struct {
	srv      *http.Server
	host     string // the listener, as host:port
	port     string
	base     string // "http://" + host
	secret   []byte
	lookup   Lookup
	accepted Accepted
}

// NewLoopback starts the page on listen, which must name a loopback IP
// ("127.0.0.1:0" picks a free port). It serves until Close.
func NewLoopback(listen string, lookup Lookup, accepted Accepted) (*Loopback, error) {
	h, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("approval listen address %q: %w", listen, err)
	}
	ip := net.ParseIP(h)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("approval listen address %q is not a loopback IP: the approval page is only ever served on this machine's loopback interface", listen)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("approval page: listen on %s: %w", listen, err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		ln.Close()
		return nil, fmt.Errorf("approval page: generate key: %w", err)
	}
	host := ln.Addr().String()
	_, port, _ := net.SplitHostPort(host)
	l := &Loopback{
		host:     host,
		port:     port,
		base:     "http://" + host,
		secret:   secret,
		lookup:   lookup,
		accepted: accepted,
	}
	l.srv = &http.Server{Handler: l, ReadHeaderTimeout: 10 * time.Second}
	go l.srv.Serve(ln)
	return l, nil
}

// URL is where the page is served.
func (l *Loopback) URL() string { return l.base }

// Close stops serving.
func (l *Loopback) Close() error { return l.srv.Close() }

// Request returns the handle for one pending proposal. It checks nothing
// about whether the proposal exists: the page does that when a human
// opens it, against whatever is true then.
func (l *Loopback) Request(stack, proposalHash string) (Handle, error) {
	if stack == "" {
		return Handle{}, errors.New("approval: stack is required")
	}
	if !core.IsHash(proposalHash) {
		return Handle{}, fmt.Errorf("approval: %q is not a 64-character hex proposal hash", proposalHash)
	}
	return Handle{
		Stack:        stack,
		ProposalHash: proposalHash,
		URL:          l.base + "/approve/" + url.PathEscape(stack) + "/" + proposalHash,
		Method:       core.MethodLoopback,
	}, nil
}

func (l *Loopback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// same-origin, not no-referrer: under no-referrer a browser sends
	// "Origin: null" on the page's own form POST, and approve requires an
	// Origin naming this server. Found by approving in a real browser; a
	// test that sets Origin by hand cannot see it. Nothing leaves this
	// origin either way.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")

	if r.Host != l.host && r.Host != "localhost:"+l.port {
		l.fail(w, http.StatusMisdirectedRequest, "Wrong address", "This page is only served at "+l.base+".")
		return
	}
	stack, hash, ok := parseApprovePath(r.URL.Path)
	if !ok {
		l.fail(w, http.StatusNotFound, "Not found", "There is nothing at this address. An approval link looks like "+l.base+"/approve/<stack>/<proposal hash>.")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		l.show(w, r, stack, hash, "")
	case http.MethodPost:
		l.approve(w, r, stack, hash)
	default:
		h.Set("Allow", "GET, POST")
		l.fail(w, http.StatusMethodNotAllowed, "Not allowed", "This page accepts GET and POST only.")
	}
}

func parseApprovePath(p string) (stack, hash string, ok bool) {
	rest, found := strings.CutPrefix(p, "/approve/")
	if !found {
		return "", "", false
	}
	escStack, hash, found := strings.Cut(rest, "/")
	if !found || !core.IsHash(hash) {
		return "", "", false
	}
	stack, err := url.PathUnescape(escStack)
	if err != nil || stack == "" {
		return "", "", false
	}
	return stack, hash, true
}

func (l *Loopback) token(stack, hash, renderedHash string) string {
	m := hmac.New(sha256.New, l.secret)
	fmt.Fprintf(m, "approve\n%s\n%s\n%s", stack, hash, renderedHash)
	return hex.EncodeToString(m.Sum(nil))
}

// show renders the page for one proposal: the approval form while it is
// pending, the record once it is approved.
func (l *Loopback) show(w http.ResponseWriter, r *http.Request, stack, hash, notice string) {
	pend, err := l.lookup(r.Context(), stack, hash)
	if errors.Is(err, ErrNotPending) {
		l.fail(w, http.StatusNotFound, "No such plan",
			"Stack "+stack+" has no plan "+hash+" waiting for approval. It may have been superseded by a newer plan, or never existed. Ask the agent to plan the change again.")
		return
	}
	if err != nil {
		l.fail(w, http.StatusConflict, "This plan cannot be approved", err.Error())
		return
	}
	defer pend.Close()

	if a := pend.Accepted; a != nil {
		data := pageData{Stack: stack, Hash: hash, Notice: notice, Approved: true}
		if acc := a.Acceptance; acc != nil {
			data.Method = acc.Method
			data.Approvers = strings.Join(acc.Approvers, ", ")
			data.AcceptedAt = acc.AcceptedAt
			if acc.RenderedHash != "" {
				data.RenderedHash = acc.RenderedHash
				if b, rerr := pend.Ledger.ReadRendered(acc.RenderedHash); rerr == nil {
					data.Render = string(b)
				} else {
					data.RenderError = rerr.Error()
				}
			}
		}
		l.page(w, http.StatusOK, data)
		return
	}

	renderedHash, stored, err := pend.Ledger.StageRender(pend.Draft)
	if err != nil {
		l.fail(w, http.StatusInternalServerError, "This plan could not be rendered", err.Error())
		return
	}
	l.page(w, http.StatusOK, pageData{
		Stack:        stack,
		Hash:         hash,
		Notice:       notice,
		Render:       string(stored),
		RenderedHash: renderedHash,
		Token:        l.token(stack, hash, renderedHash),
		Destroys:     len(pend.Draft.Delta.Destroys),
	})
}

func (l *Loopback) approve(w http.ResponseWriter, r *http.Request, stack, hash string) {
	if origin := r.Header.Get("Origin"); origin != "http://"+r.Host {
		l.fail(w, http.StatusForbidden, "Refused", "An approval has to be submitted from this page itself, in a browser.")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		l.fail(w, http.StatusForbidden, "Refused", "An approval has to be submitted from this page itself, in a browser.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		l.fail(w, http.StatusBadRequest, "Refused", "The approval form could not be read.")
		return
	}
	renderedHash := r.PostForm.Get("rendered_hash")
	if !core.IsHash(renderedHash) || !hmac.Equal([]byte(r.PostForm.Get("token")), []byte(l.token(stack, hash, renderedHash))) {
		l.fail(w, http.StatusForbidden, "Refused", "This approval did not come from a page this server rendered. Reload the page and approve again.")
		return
	}

	pend, err := l.lookup(r.Context(), stack, hash)
	if errors.Is(err, ErrNotPending) {
		l.fail(w, http.StatusNotFound, "No such plan", "Stack "+stack+" has no plan "+hash+" waiting for approval any more.")
		return
	}
	if err != nil {
		l.fail(w, http.StatusConflict, "This plan cannot be approved", err.Error())
		return
	}
	defer pend.Close()
	if pend.Accepted != nil {
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}
	if n := len(pend.Draft.Delta.Destroys); n > 0 && r.PostForm.Get("confirm_destroys") != "yes" {
		l.show(w, r, stack, hash, fmt.Sprintf("This change destroys %d resource(s). Tick the box confirming that before approving.", n))
		return
	}

	if _, err := core.AcceptFromLoopback(pend.Ledger, pend.Draft, renderedHash); err != nil {
		switch {
		case errors.Is(err, core.ErrRenderMismatch):
			l.show(w, r, stack, hash, "This plan no longer renders to the page you approved, so nothing was recorded. Read the version below and approve again if it is right.")
		case errors.Is(err, core.ErrParentMismatch):
			l.fail(w, http.StatusConflict, "This plan is out of date",
				"Stack "+stack+" changed after this plan was made, so it can no longer be approved and nothing was recorded. Ask the agent to plan the change again.")
		default:
			l.fail(w, http.StatusInternalServerError, "Nothing was recorded", err.Error())
		}
		return
	}
	if l.accepted != nil {
		l.accepted(stack, hash)
	}
	http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
}

type pageData struct {
	Stack        string
	Hash         string
	Notice       string
	Render       string
	RenderError  string
	RenderedHash string
	Token        string
	Destroys     int

	Approved   bool
	Method     string
	Approvers  string
	AcceptedAt string

	Title   string
	Message string
}

func (l *Loopback) page(w http.ResponseWriter, status int, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, d)
}

func (l *Loopback) fail(w http.ResponseWriter, status int, title, message string) {
	l.page(w, status, pageData{Title: title, Message: message})
}

var pageTemplate = template.Must(template.New("approve").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Title}}{{.Title}}{{else if .Approved}}Approved: {{.Stack}}{{else}}Approve a change to {{.Stack}}{{end}} · ubx</title>
<style>
  :root { color-scheme: light dark; --fg: #1a1a1a; --bg: #fafafa; --muted: #5a5a5a; --line: #d6d6d6; --warn: #8a3b00; --ok: #1d5c2a; }
  @media (prefers-color-scheme: dark) { :root { --fg: #e8e8e8; --bg: #161616; --muted: #a0a0a0; --line: #3a3a3a; --warn: #f0a060; --ok: #7fd18f; } }
  body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
  main { max-width: 60rem; margin: 0 auto; padding: 1.5rem 1rem 3rem; }
  h1 { font-size: 1.4rem; margin: 0 0 .5rem; }
  p { margin: .5rem 0; }
  .muted { color: var(--muted); }
  .notice { border-left: 4px solid var(--warn); padding: .5rem .75rem; margin: 1rem 0; }
  .ok { border-left: 4px solid var(--ok); padding: .5rem .75rem; margin: 1rem 0; }
  pre { border: 1px solid var(--line); padding: 1rem; overflow-x: auto; font: 13px/1.45 ui-monospace, Menlo, monospace; white-space: pre; }
  code { font: 13px ui-monospace, Menlo, monospace; word-break: break-all; }
  form { margin: 1.25rem 0; }
  label { display: block; margin: .75rem 0; }
  button { font: inherit; padding: .5rem 1.25rem; cursor: pointer; }
</style>
</head>
<body><main>
{{if .Title}}
  <h1>{{.Title}}</h1>
  <p>{{.Message}}</p>
{{else if .Approved}}
  <h1>Approved: a change to {{.Stack}}</h1>
  <div class="ok"><p>This proposal is in the ledger as approved. The agent can now apply it: tell it to call <code>apply</code> with this proposal hash.</p></div>
  <p class="muted">Proposal <code>{{.Hash}}</code><br>
  Approved via {{.Method}} by {{.Approvers}} at {{.AcceptedAt}}</p>
  {{if .RenderedHash}}<p class="muted">What was approved, exactly as shown at the time (rendered hash <code>{{.RenderedHash}}</code>):</p>{{end}}
  {{if .RenderError}}<div class="notice"><p>The stored page could not be read: {{.RenderError}}</p></div>{{end}}
  {{if .Render}}<pre>{{.Render}}</pre>{{end}}
{{else}}
  <h1>Approve a change to {{.Stack}}</h1>
  <p>An agent proposed the change below. Nothing has been changed yet. If you approve, the ledger records that someone at this machine approved it, and what this page showed. It does not record who.</p>
  <p class="muted">Everything under "THE AUTHOR'S ACCOUNT, NOT CHECKED" was written by the agent. The sections above it are the change itself.</p>
  {{if .Notice}}<div class="notice"><p>{{.Notice}}</p></div>{{end}}
  <pre>{{.Render}}</pre>
  <form method="post">
    <input type="hidden" name="rendered_hash" value="{{.RenderedHash}}">
    <input type="hidden" name="token" value="{{.Token}}">
    {{if .Destroys}}<label><input type="checkbox" name="confirm_destroys" value="yes" required> This change destroys {{.Destroys}} resource(s), and I mean that.</label>{{end}}
    <button type="submit">Approve this change</button>
  </form>
  <p class="muted">Proposal <code>{{.Hash}}</code><br>Rendered hash <code>{{.RenderedHash}}</code></p>
{{end}}
</main></body>
</html>
`))
