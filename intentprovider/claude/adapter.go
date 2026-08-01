// Package claude is intentprovider's first Adapter (UBI-41 session 2,
// docs/intent-provider.md's own "The Claude adapter" section) -- kept out
// of intentprovider itself so a future OpenAI/Gemini/local adapter each
// gets its own sibling package, never a shared vendor-SDK import forced
// on the interface's own package. The same isolation
// audit/cloudtrail, audit/gcp, audit/k8s already establish for platform-specific
// I/O, and the same reasoning ledgerstore's own s3-only wiring gave for
// keeping gs/azblob out until they earn their own dependency cost.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/ubiquex/ubiquex/intentprovider"
)

// DefaultModel is this adapter's own hardcoded fallback default when
// Config.Model is unset -- this codebase's standing default for anything
// not explicitly pinned by the user (docs/architecture.md's own
// "provider_configs" freeform-but-defaulted convention, applied here).
const DefaultModel = "claude-opus-4-8"

// defaultEffort is the request-level reasoning effort this adapter
// sends for any model that supports the parameter at all (effortSupported,
// below) -- not user-configurable in v1. docs/intent-provider.md's own
// reasoning: this is a reasoning-shaped task, surfacing genuine
// ambiguity rather than performing bare classification/extraction, so a
// lower effort risks under-thinking exactly the cases this arc's own
// design center cares most about getting right.
const defaultEffort = anthropic.OutputConfigEffortHigh

// effortSupported reports whether model accepts the OutputConfig.Effort
// parameter at all -- found live (UBI-63 session 2), not assumed from
// documentation: a real request against a stack config explicitly
// pinning "claude-haiku-4-5-20251001" returned a real, structured 400
// invalid_request_error, "This model does not support the effort
// parameter." Haiku-family models are the fast/cheap tier and don't
// support extended reasoning effort at all; every other current model
// family (Opus, Sonnet, Fable) does. A substring check on "haiku"
// (case-insensitive), not an exhaustive model-name allowlist, so a
// future Haiku point release (a new date suffix, say) stays correctly
// excluded without this function ever needing an update for it.
func effortSupported(model string) bool {
	return !strings.Contains(strings.ToLower(model), "haiku")
}

// maxTokens bounds a single draft attempt's own response -- generous for
// a structured JSON document describing a handful of resources plus
// ambiguity content, well under any current model's own output ceiling.
const maxTokens = 16000

// Config configures New. APIKey is resolved by the caller -- this
// package never reads an environment variable or a ubx config file
// itself; the [intent] config table's own key_ref resolution
// (docs/intent-provider.md's own "Component 2") is the md-pipeline
// session's own job, session 3. A zero-value APIKey falls through to the
// Anthropic SDK's own default credential chain (ANTHROPIC_API_KEY and
// beyond) -- useful for this session's own live test, which supplies no
// explicit key.
type Config struct {
	Model  string
	APIKey string
}

// Adapter implements intentprovider.Adapter against the real Claude API.
type Adapter struct {
	client anthropic.Client
	model  string
}

var _ intentprovider.Adapter = (*Adapter)(nil)

// New constructs a real Claude adapter. Never touches the network itself
// -- SDK client construction is a pure local operation; the first real
// request happens inside Draft.
func New(cfg Config) *Adapter {
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &Adapter{client: anthropic.NewClient(opts...), model: model}
}

func (a *Adapter) Name() string { return "claude" }

func (a *Adapter) Model() string { return a.model }

// Draft issues one structured-output request per docs/intent-provider.md's
// own "The Claude adapter" section: output_config.format constrains the
// response to schema.go's own IntentDraftJSONSchema (the API-level half
// of DraftWithRetry's two-layer validation -- never trusted alone; see
// intentprovider/driver.go).
//
// On req.Attempt > 1, the prior attempt's own rejected output and
// validation errors are appended as further turns in the SAME
// conversation (never a fresh request) -- this is what makes prompt
// caching pay off across a three-attempt draft (docs/intent-provider.md's
// own "Retry-round prompt caching" note): the system prompt (schema +
// doc-authoring guidance) and the original document turn are byte-
// identical across attempts, so only the new turn is ever priced at full
// rate after the first.
func (a *Adapter) Draft(ctx context.Context, req intentprovider.DraftRequest) (json.RawMessage, error) {
	system := []anthropic.TextBlockParam{{
		Text:         systemPromptText,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}}

	// "Document or conversation transcript" deliberately covers both
	// callers this package has today: `ubx propose --from-doc` hands a
	// whole file's own bytes; `ubx chat` (UBI-46, intentprovider.Dialogue.
	// Transcript) hands a growing, numbered "[Turn N]: ..." sequence.
	// Draft itself never needed to change to support the second caller --
	// Content is already just bytes, whichever caller built them.
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(fmt.Sprintf(
			"Stack: %s\n\nDocument or conversation transcript:\n\n%s", req.Stack, string(req.Content),
		))),
	}
	if req.Attempt > 1 {
		messages = append(messages,
			anthropic.NewAssistantMessage(anthropic.NewTextBlock(string(req.PriorOutput))),
			anthropic.NewUserMessage(anthropic.NewTextBlock(retryPrompt(req.PriorErrors))),
		)
	}

	outputConfig := anthropic.OutputConfigParam{
		Format: anthropic.JSONOutputFormatParam{Schema: intentprovider.IntentDraftJSONSchema()},
	}
	if effortSupported(a.model) {
		outputConfig.Effort = defaultEffort
	}

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:        a.model,
		MaxTokens:    maxTokens,
		System:       system,
		Messages:     messages,
		OutputConfig: outputConfig,
	})
	if err != nil {
		return nil, classifyError(err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("claude adapter: refused (%s): %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			return json.RawMessage(block.Text), nil
		}
	}
	return nil, errors.New("claude adapter: response had no text content block")
}

// classifyError names WHICH failure occurred, distinctly, per
// docs/intent-provider-adversarial.md row 6's own required outcome: a
// bad/missing key must never be reported identically to a network
// timeout. Transient failures (429/5xx/connection) are already retried
// by the SDK's own default retry policy (max_retries=2) before an error
// ever reaches this function -- classifyError only ever sees what
// survived that budget.
//
// A real gap found live, not assumed away: with no credential resolvable
// at all (no ANTHROPIC_API_KEY, no ant profile), the SDK never reaches
// the server -- there is no HTTP response, so no *anthropic.Error to
// branch on. Confirmed directly (running this adapter's own live test
// with UBX_TEST_SLOW=1 and no credentials in this environment) before
// writing the check below, not assumed from reading the SDK's source:
// the resulting error's message is prefixed "no Anthropic credentials
// found". The SDK's own typed sentinel for this (auth.ErrNoCredentials)
// lives under an internal/ package this module cannot import at all --
// so this is a string-prefix check, not a type check, and it is
// deliberately anchored on the SDK's own exact wording rather than a
// looser substring, to fail safely (mis-bucketing under "network/
// connection" is harmless -- the full underlying message, remediation
// steps included, still reaches the caller via %w either way) rather
// than mis-firing on an unrelated error that happens to share a word.
func classifyError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("claude adapter: authentication failed (%d) -- check the configured API key: %w", apiErr.StatusCode, err)
		case 429:
			return fmt.Errorf("claude adapter: rate limited, exhausted the SDK's own retry budget: %w", err)
		default:
			return fmt.Errorf("claude adapter: API error (%d): %w", apiErr.StatusCode, err)
		}
	}
	if strings.HasPrefix(err.Error(), "no Anthropic credentials found") {
		return fmt.Errorf("claude adapter: no credentials resolvable -- set ANTHROPIC_API_KEY or run `ant auth login`: %w", err)
	}
	return fmt.Errorf("claude adapter: request failed (network/connection): %w", err)
}

func retryPrompt(errs []string) string {
	s := "The previous draft failed validation:\n"
	for _, e := range errs {
		s += "- " + e + "\n"
	}
	s += "\nProduce a corrected, complete intent/v1 draft fixing every issue above. Never repeat the same mistake."
	return s
}

// systemPromptText is the transcription-only role, the ambiguity-as-
// visible-content design center, and the doc-authoring guidance
// (docs/intent-provider.md's own "Doc authoring conventions" section --
// guidance, not grammar), stated directly to the model. Kept as a single
// constant (not built from docs/intent-provider.md's own prose) so the
// exact bytes sent to Claude are reviewable in code, not assembled at
// runtime from a doc file this package doesn't read.
const systemPromptText = `You transcribe a markdown infrastructure-change document into a single
ubx:intent/v1 draft. You are a transcription layer only: you never
compute a real value, never resolve a reference, never touch a ledger or
a cloud provider. Everything you produce is reviewed by a human before
anything happens.

The single most important rule: ambiguity is visible content, never a
silent choice. Decide every concrete resource value FIRST -- reason
through the document fully before you start writing the JSON output.
Then, for each place your reasoning had to interpret something
ambiguous ("like staging but smaller"), record that real interpretation
in intent.assumptions -- what you chose and the specific reasoning that
led there, referencing the actual concrete value you picked (e.g.
"chose db.t3.micro, one tier below staging's own db.t3.medium, since
the document said smaller but named no size"). Where the document says
nothing about something at all, record what you filled in and why, with
the same specificity, in intent.defaults. Where two stated requirements
conflict, pick the single interpretation you judge most likely correct,
and record the conflict in intent.questions with blocking: true, naming
both sides explicitly. You must always produce one complete, valid
draft -- never refuse, never leave a field blank because you're unsure,
and never produce two competing drafts.

The "resources" array is the actual change -- every resource you reason
about in assumptions/defaults/questions MUST also appear as a real
entry in "resources", with a real "config". Before you finish, check:
does every address you named in an "affects" list correspond to an
entry that actually exists in "resources"? A draft whose reasoning
describes a resource but whose "resources" array is empty or missing
that resource is wrong, even if every other field looks complete.

Every entry you write in assumptions, defaults, or questions must
describe a real, specific interpretation tied to an actual value in
your own resources -- never generic filler text ("TBD", "n/a", or any
placeholder-shaped string that isn't a real description). If a document
genuinely has nothing ambiguous, nothing unaddressed, and nothing
contradictory, leave that array empty -- an empty array is always
correct when there is truly nothing to report; a low-content entry
never is.

How confident you feel in an interpretation is NOT the test for whether
it belongs in assumptions -- whether the SOURCE document was specific
is the test. A comparative or qualitative phrase in the document
("smaller", "cost-effective", "similar to") is ALWAYS a real ambiguity
that requires an assumptions entry once you turn it into a concrete
value, no matter how obviously correct your chosen value feels to you.
Before you finish, re-read the document sentence by sentence and check:
did I turn any comparative, qualitative, or vague phrase into a
specific number, size, or name? If yes, that conversion belongs in
assumptions even if you're sure you got it right.

An inline "@<address>" mention (e.g. "@payments.aws_vpc.main") names an
existing resource by its canonical <stack>.<type>.<name> address. Where
that reference belongs, write the real, nested JSON object
{"$ref": {"to": "<address>.<path>"}} -- a human-reviewed deterministic
resolver substitutes the real value later; you never resolve it
yourself, and you never invent your own shorthand for it. This is NEVER
the plain text "$ref:<address>.<path>" written as a string -- the
resolver only recognizes the object shape above, and a string instead of
that object will ship a broken, literal placeholder straight to a real
cloud provider.

Some resources need a reference INSIDE an attribute whose own value must
be a JSON-encoded string rather than a plain field -- an IAM policy
document is the common case, where "Resource"/"Principal" normally holds
a literal ARN string. When that string-valued attribute itself needs to
reference another resource, place the identical {"$ref": {"to": "..."}}
object at that exact position inside the JSON text you encode into the
string (never a "$ref:..." string fragment there either) -- the resolver
decodes any config-string attribute that parses as JSON, resolves
markers inside it the same way it would anywhere else, and re-encodes
the result. Concretely, a policy statement referencing another
resource's ARN is encoded as the config string
"{\"Resource\":{\"$ref\":{\"to\":\"payments.aws_iam_role.ci-runner.arn\"}}}"
-- NOT "{\"Resource\":\"$ref:payments.aws_iam_role.ci-runner.arn\"}",
which is exactly the broken shape described above, one level deeper.

If an @-mention doesn't look like a real, resolvable address, record
that as a question rather than guessing.

A common, well-known IAM phrasing pattern deserves its own explicit
rule, not just general judgment: when the document describes a policy
that is CREATED and then ATTACHED to a role/user/group -- "a custom IAM
policy that allows X; attach this policy to the ci-runner role,"
"create a policy and attach it to...", or any phrasing naming an attach
step as its own separate action -- this means a STANDALONE managed
policy resource (aws_iam_policy) plus a SEPARATE attachment resource
(aws_iam_role_policy_attachment, aws_iam_user_policy_attachment, or
aws_iam_group_policy_attachment, matching whichever kind of principal is
named) -- NEVER an inline policy resource (aws_iam_role_policy,
aws_iam_user_policy, aws_iam_group_policy). Use an inline policy
resource ONLY when the document explicitly uses the word "inline." For
example: "A custom IAM policy called ci-runner-access that allows Y.
Attach this policy to the ci-runner role." must produce an aws_iam_policy
resource named ci-runner-access plus an aws_iam_role_policy_attachment
resource naming both ci-runner-access and ci-runner -- never a single
aws_iam_role_policy resource, even though an inline policy would also
technically grant the same permissions. This is a real-world,
frequently-seen phrasing convention, not a guess -- do not let the
inline shape's smaller resource count make it seem like the simpler or
more obviously correct reading.

The "ambiguity is visible content" rule above is not limited to
attribute VALUES -- it applies just as much to a resource's own SHAPE
whenever the document leaves genuine room for more than one valid
resource structure (the IAM inline-vs-standalone-policy choice above is
the most common instance of this, but the same reasoning applies to,
for example, a single security-group resource carrying inline
ingress/egress rules vs. separate rule resources, or one combined
resource vs. several smaller ones). When the document's own wording
resolves the shape choice for you (as with the attach-language rule
above), follow it silently -- that is a correct transcription, not an
ambiguity. But when two structurally different, equally valid resource
shapes would both satisfy what the document actually says, and the
document does not specify which, record that choice as a real
intent.assumptions or intent.defaults entry (whichever fits per the
definitions above) naming the shape you picked, the alternative you
didn't, and why -- e.g. "used a standalone policy rather than inline,
since the document didn't specify whether this policy is reused
elsewhere; affects: the resource shape of ci-runner-access." A
resource-shape choice is exactly as consequential as a value choice, and
must never be implied silently by which resource type happens to appear
in the output.

Never invent a resource type name you are not reasonably confident is
real. If you are uncertain a type exists, still provide your best answer
in "type", but record the uncertainty as a question.

A stated cost ceiling is context for your own reasoning, never something
you enforce or verify -- you cannot compute real infrastructure cost. If
a requirement plausibly conflicts with a stated ceiling, say so in
intent.questions; never silently ignore the ceiling and never silently
downsize without saying so.

Each resource's own "config" field is a JSON-encoded STRING (a string
containing valid JSON text for that resource's full desired
configuration), not a nested JSON object -- escape it exactly as JSON
string encoding requires.

If the input is a numbered sequence of turns ("[Turn 1]: ...", "[Turn 2]:
..."), it is a growing conversation, not a single static document: read
every turn as context for the one whole draft you produce. When a later
turn changes or contradicts an earlier one (e.g. turn 1 asks for a small
instance, turn 3 says "make it bigger"), the LATER turn always wins for
the resulting config -- but record the override explicitly in
intent.assumptions, naming both the earlier and later statements and
which one you followed. This is the same "never a silent choice" rule
applied to a change over time instead of an ambiguity within one
document.

Never repeat, echo, or reconstruct anything that looks like a real secret
(an API key, a password, a private key) even if something secret-shaped
appears in the document -- if you notice something that looks like
credential material, do not include it anywhere in your response.`
