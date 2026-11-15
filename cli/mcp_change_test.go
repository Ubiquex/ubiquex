package cli

import (
	"bytes"
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ubiquex/ubiquex/approval"
	"github.com/ubiquex/ubiquex/core"
)

// changeHarness is a served stack with a real loopback approval page and
// an MCP client connected to a server using it.
type changeHarness struct {
	root    string
	addr    string
	page    *approval.Loopback
	session *mcp.ClientSession
}

func newChangeHarness(t *testing.T) *changeHarness {
	t.Helper()
	root, addr := servedFixtureStack(t)
	ensureFakeProviderStateDir(t)
	served, err := loadServedStacks([]string{root}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := approval.NewLoopback("127.0.0.1:0", pendingLookup(served), servedPlanConsumed(served))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { page.Close() })

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(served, page).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "mcp-test", Version: "v0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &changeHarness{root: root, addr: addr, page: page, session: session}
}

func (h *changeHarness) plan(t *testing.T, intent map[string]any) planJSON {
	t.Helper()
	var out planJSON
	callToolJSON(t, h.session, "plan", map[string]any{"stack": "payments", "intent": intent}, &out)
	return out
}

var createWidgetB = map[string]any{
	"intent": map[string]any{"summary": "add widget b"},
	"resources": []any{
		map[string]any{"type": "fake_widget", "name": "b", "op": "create", "config": map[string]any{"name": "widget-b"}},
	},
}

// noRedirect keeps the client on the response the page actually sent.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func getPage(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := noRedirect.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var (
	renderedHashField = regexp.MustCompile(`name="rendered_hash" value="([0-9a-f]{64})"`)
	tokenField        = regexp.MustCompile(`name="token" value="([0-9a-f]{64})"`)
	preBlock          = regexp.MustCompile(`(?s)<pre>(.*?)</pre>`)
)

// formOf reads the approval form off a rendered page.
func formOf(t *testing.T, body string) (renderedHash, token, shown string) {
	t.Helper()
	rh := renderedHashField.FindStringSubmatch(body)
	tok := tokenField.FindStringSubmatch(body)
	pre := preBlock.FindStringSubmatch(body)
	if rh == nil || tok == nil || pre == nil {
		t.Fatalf("approval form not found on page:\n%s", body)
	}
	return rh[1], tok[1], html.UnescapeString(pre[1])
}

// postApproval submits the form the way a browser does from the page.
func postApproval(t *testing.T, pageURL string, form url.Values, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, pageURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, _ := url.Parse(pageURL)
	req.Header.Set("Origin", "http://"+u.Host)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestMCPChange_PlanRefuseApproveApply is the slice's own sequence: an
// agent plans, apply is refused with an instruction naming the approval
// page, a human approves there, apply ships, and the ledger holds what
// that human saw, which ubx verify then re-checks.
func TestMCPChange_PlanRefuseApproveApply(t *testing.T) {
	h := newChangeHarness(t)

	p := h.plan(t, createWidgetB)
	if !core.IsHash(p.ProposalHash) || !strings.HasPrefix(p.ApprovalURL, h.page.URL()+"/approve/payments/"+p.ProposalHash) {
		t.Fatalf("unexpected plan result: %+v", p)
	}
	if !strings.HasPrefix(p.Render, core.RendererVersion+"\n") || !strings.Contains(p.Render, "+ create payments.fake_widget.b") {
		t.Fatalf("plan render is not the proposal render:\n%s", p.Render)
	}
	if _, err := os.Stat(planFilePath(h.root, p.ProposalHash)); err != nil {
		t.Fatalf("plan file not written: %v", err)
	}

	refusal := callToolError(t, h.session, "apply", map[string]any{"stack": "payments", "proposal_hash": p.ProposalHash})
	for _, want := range []string{"nobody has approved", p.ApprovalURL, "cannot approve it yourself", "call apply again"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("apply refusal does not say %q:\n%s", want, refusal)
		}
	}
	if _, err := core.Open(h.root).Read(p.ProposalHash); err == nil {
		t.Fatal("a refused apply put the proposal in the ledger")
	}

	status, body := getPage(t, p.ApprovalURL)
	if status != http.StatusOK {
		t.Fatalf("approval page status %d:\n%s", status, body)
	}
	rh, token, shown := formOf(t, body)
	if shown != p.Render {
		t.Fatalf("the page does not show the render plan returned:\npage:\n%s\nplan:\n%s", shown, p.Render)
	}
	if got := core.HashRendered([]byte(shown)); got != rh {
		t.Fatalf("page shows bytes hashing to %s but submits %s", got, rh)
	}

	status, _ = postApproval(t, p.ApprovalURL, url.Values{"rendered_hash": {rh}, "token": {token}}, nil)
	if status != http.StatusSeeOther {
		t.Fatalf("approval POST status %d, want 303", status)
	}
	status, body = getPage(t, p.ApprovalURL)
	if status != http.StatusOK || !strings.Contains(body, "Approved: a change to payments") || !strings.Contains(body, "loopback:presence") {
		t.Fatalf("page after approval:\n%s", body)
	}
	if _, err := os.Stat(planFilePath(h.root, p.ProposalHash)); !os.IsNotExist(err) {
		t.Fatalf("plan file not removed after approval: %v", err)
	}

	accepted, err := core.Open(h.root).Read(p.ProposalHash)
	if err != nil {
		t.Fatalf("approved proposal not in the ledger: %v", err)
	}
	a := accepted.Acceptance
	if a.Method != core.MethodLoopback || len(a.Approvers) != 1 || a.Approvers[0] != core.LoopbackApprover || a.RenderedHash != rh {
		t.Fatalf("acceptance = %+v", a)
	}
	stored, err := core.Open(h.root).ReadRendered(rh)
	if err != nil || string(stored) != shown {
		t.Fatalf("stored render is not what the page showed (err %v)", err)
	}

	var applied applyJSON
	callToolJSON(t, h.session, "apply", map[string]any{"stack": "payments", "proposal_hash": p.ProposalHash}, &applied)
	if applied.Outcome != "applied" || applied.ApplyRecord == nil || applied.Acceptance == nil || applied.Acceptance.RenderedHash != rh {
		t.Fatalf("apply result: %+v", applied)
	}

	var again applyJSON
	callToolJSON(t, h.session, "apply", map[string]any{"stack": "payments", "proposal_hash": p.ProposalHash}, &again)
	if again.Outcome != "already_applied" || len(again.Applies) == 0 {
		t.Fatalf("second apply: %+v", again)
	}

	var st stateJSON
	callToolJSON(t, h.session, "state", map[string]any{"stack": "payments", "address": "payments.fake_widget.b"}, &st)
	if !strings.Contains(string(st.State), "widget-b") {
		t.Fatalf("applied resource not in state: %s", st.State)
	}

	out, err := runUbx(t, nil, "verify", "--ledger-dir", h.root)
	if err != nil || !strings.Contains(out, "(loopback): VERIFIED") {
		t.Fatalf("ubx verify on the result: %v\n%s", err, out)
	}
}

// TestMCPChange_PageRefusesForgedApprovals: an approval has to come from
// the page itself. Each forgery leaves the ledger untouched.
func TestMCPChange_PageRefusesForgedApprovals(t *testing.T) {
	h := newChangeHarness(t)
	p := h.plan(t, createWidgetB)
	_, body := getPage(t, p.ApprovalURL)
	rh, token, _ := formOf(t, body)
	good := url.Values{"rendered_hash": {rh}, "token": {token}}

	cases := []struct {
		name    string
		form    url.Values
		headers map[string]string
		want    int
	}{
		{"no origin", good, map[string]string{"Origin": ""}, http.StatusForbidden},
		{"cross-site origin", good, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site fetch", good, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"no token", url.Values{"rendered_hash": {rh}}, nil, http.StatusForbidden},
		{"token for other bytes", url.Values{"rendered_hash": {strings.Repeat("a", 64)}, "token": {token}}, nil, http.StatusForbidden},
	}
	for _, c := range cases {
		if status, _ := postApproval(t, p.ApprovalURL, c.form, c.headers); status != c.want {
			t.Errorf("%s: status %d, want %d", c.name, status, c.want)
		}
	}

	// DNS rebinding: a request naming another host is refused outright.
	req, _ := http.NewRequest(http.MethodGet, p.ApprovalURL, nil)
	req.Host = "attacker.example"
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("foreign Host: status %d, want 421", resp.StatusCode)
	}

	if _, err := core.Open(h.root).Read(p.ProposalHash); err == nil {
		t.Fatal("a forged approval reached the ledger")
	}
	if resp, err := noRedirect.Get(p.ApprovalURL); err == nil {
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("CSP = %q", csp)
		}
		resp.Body.Close()
	}
}

// TestMCPChange_DestroyNeedsConfirmation: a destroy is approved only with
// the confirmation box ticked, the page's counterpart of --confirm-destroys.
func TestMCPChange_DestroyNeedsConfirmation(t *testing.T) {
	h := newChangeHarness(t)
	p := h.plan(t, map[string]any{
		"intent":    map[string]any{"summary": "retire the fixture widget"},
		"resources": []any{},
		"destroys":  []any{h.addr},
	})
	_, body := getPage(t, p.ApprovalURL)
	if !strings.Contains(body, `name="confirm_destroys"`) {
		t.Fatalf("destroy page has no confirmation box:\n%s", body)
	}
	rh, token, _ := formOf(t, body)

	status, body := postApproval(t, p.ApprovalURL, url.Values{"rendered_hash": {rh}, "token": {token}}, nil)
	if status != http.StatusOK || !strings.Contains(body, "destroys 1 resource") {
		t.Fatalf("unconfirmed destroy: status %d\n%s", status, body)
	}
	if _, err := core.Open(h.root).Read(p.ProposalHash); err == nil {
		t.Fatal("an unconfirmed destroy was accepted")
	}

	status, _ = postApproval(t, p.ApprovalURL, url.Values{"rendered_hash": {rh}, "token": {token}, "confirm_destroys": {"yes"}}, nil)
	if status != http.StatusSeeOther {
		t.Fatalf("confirmed destroy: status %d", status)
	}
	if _, err := core.Open(h.root).Read(p.ProposalHash); err != nil {
		t.Fatalf("confirmed destroy not accepted: %v", err)
	}
}

// TestMCPChange_StaleAndTamperedPlans: a plan the stack has moved past, or a
// plan file edited after it was made, is never offered for approval.
func TestMCPChange_StaleAndTamperedPlans(t *testing.T) {
	h := newChangeHarness(t)
	first := h.plan(t, createWidgetB)
	second := h.plan(t, map[string]any{
		"intent":    map[string]any{"summary": "add widget c"},
		"resources": []any{map[string]any{"type": "fake_widget", "name": "c", "op": "create", "config": map[string]any{"name": "widget-c"}}},
	})

	// Approve the second; the first was resolved against the same head and
	// is now behind it.
	_, body := getPage(t, second.ApprovalURL)
	rh, token, _ := formOf(t, body)
	if status, _ := postApproval(t, second.ApprovalURL, url.Values{"rendered_hash": {rh}, "token": {token}}, nil); status != http.StatusSeeOther {
		t.Fatalf("approving the second plan: %d", status)
	}
	_, body = getPage(t, first.ApprovalURL)
	rh, token, _ = formOf(t, body)
	status, body := postApproval(t, first.ApprovalURL, url.Values{"rendered_hash": {rh}, "token": {token}}, nil)
	if status != http.StatusConflict || !strings.Contains(body, "plan the change again") {
		t.Fatalf("approving a stale plan: status %d\n%s", status, body)
	}

	third := h.plan(t, map[string]any{
		"intent":    map[string]any{"summary": "add widget d"},
		"resources": []any{map[string]any{"type": "fake_widget", "name": "d", "op": "create", "config": map[string]any{"name": "widget-d"}}},
	})
	path := planFilePath(h.root, third.ProposalHash)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Replace(raw, []byte("widget-d"), []byte("widget-x"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	status, body = getPage(t, third.ApprovalURL)
	if status != http.StatusConflict || !strings.Contains(body, "edited or corrupted") {
		t.Fatalf("tampered plan page: status %d\n%s", status, body)
	}
}

func TestMCPChange_ApplyRefusesUnknownHash(t *testing.T) {
	h := newChangeHarness(t)
	msg := callToolError(t, h.session, "apply", map[string]any{"stack": "payments", "proposal_hash": strings.Repeat("b", 64)})
	if !strings.Contains(msg, "no plan") || !strings.Contains(msg, "Call plan again") {
		t.Fatalf("unknown hash: %s", msg)
	}
	msg = callToolError(t, h.session, "apply", map[string]any{"stack": "payments", "proposal_hash": "abc"})
	if !strings.Contains(msg, "full 64-character") {
		t.Fatalf("short hash: %s", msg)
	}
}

func TestMCPChange_PlanRefusesBadIntent(t *testing.T) {
	h := newChangeHarness(t)
	for _, c := range []struct {
		intent map[string]any
		want   string
	}{
		{map[string]any{"resourcez": []any{}}, "unknown field"},
		{map[string]any{"stack": "billing", "resources": []any{}}, "names stack"},
		{map[string]any{"resources": []any{map[string]any{"type": "nope_widget", "name": "x", "op": "create", "config": map[string]any{}}}}, "nope_widget"},
	} {
		msg := callToolError(t, h.session, "plan", map[string]any{"stack": "payments", "intent": c.intent})
		if !strings.Contains(msg, c.want) {
			t.Errorf("intent %v: want %q in %s", c.intent, c.want, msg)
		}
	}
}

func TestMCPChange_NothingServed(t *testing.T) {
	session := connectMCPTestClient(t)
	for tool, args := range map[string]map[string]any{
		"plan":  {"stack": "payments", "intent": map[string]any{}},
		"apply": {"stack": "payments", "proposal_hash": strings.Repeat("a", 64)},
	} {
		msg := callToolError(t, session, tool, args)
		if !strings.Contains(msg, "--serve") {
			t.Errorf("%s on an unserved server: %s", tool, msg)
		}
	}
}
