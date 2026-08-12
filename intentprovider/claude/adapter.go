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
//
// UBI-87: was claude-opus-4-8. A zero-config user (no [intent] block, or
// an [intent] block with no model field) was silently burning opus
// pricing on every plan/propose/chat call -- a real cost trap, found live
// by the founder hitting a credit wall with no prior warning. sonnet-5 is
// the standing "good balance of cost/quality" tier project-wide, and
// UBI-65's fix means even haiku now handles this project's hardest tested
// case (the IAM attach-policy shape) correctly -- sonnet-5 as the silent
// default is a comfortable, defensible choice, not a quality compromise.
// docs/intent-provider.md and cli/config.mdx both document this default
// explicitly so it's never a silent surprise either way again.
const DefaultModel = "claude-sonnet-5"

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

// readStackConfigToolName is UBI-81 v1's own one, deliberately narrow
// read-only tool -- the intent provider's first (and, this session,
// only) capability to poke a small, well-justified hole through this
// package's own "no filesystem access" boundary (intentprovider/
// adapter.go's own doc comment). Answered entirely from
// req.StackConfig, itself just plain, pre-computed DATA the caller
// handed over before drafting began (cli/stackcontext.go) -- Draft
// never performs a live read of anything to answer this tool call, the
// identical "still just data, never a live capability" relationship
// KnownResources already has to a ledger read.
const readStackConfigToolName = "read_stack_config"

// maxToolRounds bounds how many tool-use round trips one Draft call will
// make before giving up -- a real, small ceiling (there is exactly one
// tool, and a well-behaved model calls it at most once before producing
// its final draft), not an unbounded loop that could burn an unbounded
// number of billed requests against a model that keeps calling it.
const maxToolRounds = 4

func readStackConfigTool() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name: readStackConfigToolName,
			Description: anthropic.String(
				"Read the target stack's own name and a small, fixed set of its real " +
					".ubx/config values (its provider source, ledger store location, GitHub " +
					"repo, and similar). Use this when the document's own phrasing depends on " +
					"which real environment this stack is (\"if this is production...\"), or on " +
					"a naming convention (a stack named \"payments-prod\"), or on any other " +
					"config-derived fact you would otherwise have to guess. Takes no input, " +
					"always returns immediately. It never returns live infrastructure state, " +
					"resource data, or cost information -- only static configuration, and it " +
					"may have nothing useful to say. If the returned data doesn't actually " +
					"resolve what you needed, you must not guess -- record a blocking question " +
					"instead, exactly as you would with no tool at all. Calling this tool never " +
					"replaces recording a named default: whenever you DO use this data to " +
					"resolve something the document left ambiguous, you must still record that " +
					"resolution as a normal intent.defaults entry, naming the real value you " +
					"chose and the config fact that justified it.",
			),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]any{},
			},
		},
	}
}

// stackConfigToolResult is the ONLY answer Draft ever gives
// read_stack_config -- req.StackConfig verbatim when the caller supplied
// any, or an honest "not available" string otherwise (a blueprint build,
// intentprovider/conformance's own fixtures, or any future caller that
// hasn't opted in) -- never a guessed or fabricated value.
func stackConfigToolResult(stackConfig json.RawMessage) string {
	if len(stackConfig) == 0 {
		return "no stack context is available for this drafting session -- do not guess; " +
			"if resolving this genuinely needs it, record a blocking question instead"
	}
	return string(stackConfig)
}

// Draft issues one structured-output request per docs/intent-provider.md's
// own "The Claude adapter" section: output_config.format constrains the
// final response to schema.go's own IntentDraftJSONSchema (the API-level
// half of DraftWithRetry's two-layer validation -- never trusted alone;
// see intentprovider/driver.go). UBI-81 v1 extends this into a real,
// bounded tool-use loop (maxToolRounds): the model may call
// read_stack_config zero or more times before producing that final
// structured draft -- each call is answered, in the SAME request-response
// cycle Anthropic's own tool-use protocol defines (resp.ToParam() replays
// the model's own tool_use turn verbatim, a tool_result turn answers it,
// then the SAME conversation continues), never a second, parallel
// mechanism.
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
		anthropic.NewUserMessage(anthropic.NewTextBlock(buildUserPrompt(req))),
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

	tools := []anthropic.ToolUnionParam{readStackConfigTool()}

	for round := 0; round < maxToolRounds; round++ {
		resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:        a.model,
			MaxTokens:    maxTokens,
			System:       system,
			Messages:     messages,
			Tools:        tools,
			OutputConfig: outputConfig,
		})
		if err != nil {
			return nil, classifyError(err)
		}
		if resp.StopReason == anthropic.StopReasonRefusal {
			return nil, fmt.Errorf("claude adapter: refused (%s): %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
		}

		if resp.StopReason != anthropic.StopReasonToolUse {
			for _, block := range resp.Content {
				if block.Type == "text" {
					return json.RawMessage(block.Text), nil
				}
			}
			return nil, errors.New("claude adapter: response had no text content block")
		}

		// A real tool-use turn: replay the model's own turn verbatim
		// (resp.ToParam() -- the SDK's own round-trip converter, never a
		// hand-built approximation of it), answer every tool_use block
		// this response carries (in practice always exactly one call to
		// read_stack_config, but a well-formed multi-block response is
		// handled correctly regardless), then loop back into the SAME
		// conversation for the model's own next turn.
		messages = append(messages, resp.ToParam())
		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			if block.Name != readStackConfigToolName {
				results = append(results, anthropic.NewToolResultBlock(block.ID, fmt.Sprintf("unrecognized tool %q", block.Name), true))
				continue
			}
			results = append(results, anthropic.NewToolResultBlock(block.ID, stackConfigToolResult(req.StackConfig), false))
		}
		messages = append(messages, anthropic.NewUserMessage(results...))
	}

	return nil, fmt.Errorf("claude adapter: exceeded %d tool-use round(s) without a final draft", maxToolRounds)
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

// buildUserPrompt is Draft's own first-turn text -- the stack name
// (unchanged), UBI-85's own "Resources already recorded" section when
// req.KnownResources carries any (see the system prompt's own
// "Re-planning against an existing stack" section for exactly how the
// model is instructed to use it), and the document/transcript content
// (unchanged). json.MarshalIndent on a map[string]json.RawMessage
// indents the outer key structure; each already-compact RawMessage value
// is spliced in verbatim rather than re-indented -- fine here, the model
// needs to parse it correctly, not admire its formatting.
func buildUserPrompt(req intentprovider.DraftRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Stack: %s\n\n", req.Stack)
	if len(req.KnownResources) > 0 {
		known, err := json.MarshalIndent(req.KnownResources, "", "  ")
		if err == nil {
			fmt.Fprintf(&b, "Resources already recorded in this stack's ledger, keyed by \"<type>.<name>\" "+
				"(see the system prompt's own \"Re-planning against an existing stack\" section):\n\n%s\n\n", known)
		}
	} else {
		b.WriteString("No resources are currently recorded for this stack -- treat every resource the document describes as new.\n\n")
	}
	fmt.Fprintf(&b, "Document or conversation transcript:\n\n%s", string(req.Content))
	return b.String()
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

You have access to one read-only tool, read_stack_config, which returns
the target stack's own name plus a small, fixed set of its real
.ubx/config values. Call it whenever the document's own phrasing depends
on a fact about the real target environment you don't already have --
most commonly a conditional naming an environment tier ("if this is
production...", "in staging, use..."), or a convention implied by the
stack's own name. Call it at most once; its answer does not change
between calls in the same conversation. If its answer genuinely resolves
the condition, use the resolved branch and record what you inferred and
why as a normal intent.defaults entry, naming the real config fact that
justified it -- exactly like any other default you fill in, never
silently. If its answer does not resolve the condition (the fact you
needed isn't in it, or its answer is itself ambiguous for this purpose),
you still must not guess: record a blocking question naming exactly what
you could not determine, precisely as you would if this tool did not
exist at all. Calling the tool is never itself a substitute for either of
those -- it only ever changes whether you're recording a default or a
question, never removes the need to record one.

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
about in assumptions/defaults/questions as something being CREATED or
MODIFIED MUST also appear as a real entry in "resources", with a real
"config". Before you finish, check: does every address you named in an
"affects" list correspond to an entry that actually exists in
"resources"? A draft whose reasoning describes a change to a resource
but whose "resources" array is empty or missing that resource is wrong,
even if every other field looks complete. (The one exception: a resource
you recognize as already matching what's currently recorded needs no
entry at all -- see "Re-planning against an existing stack" below.)

Re-planning against an existing stack (UBI-85): the user turn may include
a "Resources already recorded in this stack's ledger" section, listing
every resource this stack already has, keyed by "<type>.<name>", each
with its own full currently-recorded state (including provider-computed
attributes you have no other way to know, like a generated id or ARN).
When this section is present, treat it as ground truth for what already
exists. For every resource you identify in the document:

- If it clearly describes the SAME real-world resource as an entry in
  that list (matching what it represents, not just superficial wording),
  and you include it at all, you MUST reuse that entry's EXACT "type"
  and "name" -- never invent a slightly different name for something
  that already has one recorded; a renamed duplicate would draft as an
  unwanted second resource, not a recognized match.
- If every attribute the document specifies for it is already consistent
  with that entry's own recorded state, DO NOT include it in "resources"
  at all. It is unchanged; correctly omitting it is the right answer,
  not an oversight.
- If the document now describes it differently (any attribute value that
  doesn't match), include it with "op": "modify". Its "config" must be
  the resource's COMPLETE current desired state -- every attribute
  already recorded for it, reproduced UNCHANGED, plus whatever the
  document now says differently. Never omit an attribute just because it
  isn't changing, and never omit one you don't see mentioned in the
  document at all (a provider-computed id/arn, say) -- an attribute
  missing from your "config" is read as REMOVED, not preserved, so
  dropping it would silently destroy real, already-existing state that
  was never actually asked to change.
- If nothing in the known-resources list matches, it's genuinely new --
  "op": "create", exactly as you would without this section present.

If an entry in the known-resources list has a "<type>.<name>" the
CURRENT document text does not describe anywhere at all, this is a real,
visible tension, not something to resolve silently either way. Do NOT
add it to "destroys" -- destroys are never inferred from a resource's
absence (see the "destroys" field's own description; that rule applies
here just as much as anywhere else). Instead, add an intent.questions
entry with blocking: true, naming the exact address, stating plainly
that the ledger still tracks it but the current document no longer
describes it, and that an explicit "ubx terminate" is the only way to
actually remove it if that is truly what's intended.

It is entirely correct -- not a failure, not something to avoid -- for a
draft to end up with a completely empty "resources" array (and empty
"destroys") when literally everything the document describes already
matches what's recorded. Say so plainly in intent.summary (e.g. "No
changes: all 5 resources already match their currently recorded state")
rather than inventing a change that isn't there.

If no "Resources already recorded" section is present in the user turn at
all, there is no known existing state to compare against -- draft every
resource exactly as you would without this capability, "op": "create"
for everything the document describes.

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
credential material, do not include it anywhere in your response.

A distinct, common phrasing pattern deserves its own explicit rule, the
same way the IAM attach-language rule above does: when the document says
to USE, CALL, or INSTANTIATE an existing BLUEPRINT by name -- "Use
blueprint ci-platform:v1 with: repo_name = payments-ci-artifacts,
queue_name = payments-notifications", or any phrasing naming a blueprint
reference plus parameter values -- this is NEVER something you draft
resources for yourself. You have no way to know what resources a
blueprint actually contains (its own build step already fixed that,
separately, once, outside this conversation entirely), and guessing
would silently duplicate or contradict it. Instead, extract exactly the
blueprint's own reference (verbatim, including any version/ref/path the
document gives) and every named parameter's own value, and record ONE
entry in the top-level "blueprint_calls" array: "name" a short label for
this call, "blueprint" the reference exactly as written, "ref"/"path"
the git ref/path if the document names one (empty string "" if not --
never omit these two fields, and never guess at a value the document
doesn't state), and "args" a JSON-encoded object string of parameter
name -> value, e.g. "{\"repo_name\":\"payments-ci-artifacts\",\"queue_name\":
\"payments-notifications\"}" -- every value a plain string, even one
that looks like a number, since you don't have the blueprint's own
declared param types available to you. Do NOT add anything to
"resources" for a blueprint call, and do NOT record it as an assumption
or default either -- extracting the call correctly IS the complete,
correct output for that part of the document, nothing else to reason
about.

If the SAME sentence also gives this call an explicit NAME to reference
it by later -- "Call blueprint ci-platform as 'platform' with: ..." or
"...as \"platform\" with: ..." -- extract that quoted name verbatim into
"call_name" on the same blueprint_calls[] entry. This is a completely
different thing from "name" above: "name" is a free-text label for error
messages only; "call_name" is a real identifier later prose in the SAME
document can reference by, to use one of that call's own outputs (the
blueprint's own declared outputs: -- you have no way to know what
outputs a blueprint declares any more than you know what resources it
contains, so never guess whether one exists, only ever transcribe what
the document itself says). Leave "call_name" as the empty string "" when
the document gives this call no such alias -- never invent one, and
never reuse "name" as a substitute even if it happens to look like a
plausible identifier.

Wherever LATER prose in the document references that alias's own
output -- "platform's own repo_arn output", "using platform's repo_arn",
or similar phrasing naming a call_name plus one of its outputs -- treat
it exactly like an ordinary "@<address>" resource reference (the rule
above this one): place a real, nested {"$ref": {"to": "..."}} object at
the position the referenced value itself belongs (inside a resource's
own "config", or one level deeper inside a string-valued attribute that
itself holds JSON text, exactly like an ordinary reference), but with
"to" set to the literal string "$blueprint_output:<call_name>:<output_key>"
instead of a <stack>.<type>.<name>.<attr> address -- e.g.
"{\"$ref\":{\"to\":\"$blueprint_output:platform:repo_arn\"}}". Never a
<stack>.<type>.<name>.<attr> address for this case: a blueprint call's
own real resource types are never available to you, only the calling
document's own call_name and the output name it names. If the document
references an alias that was never given a call_name anywhere in the
same document, or references an output name the document itself never
otherwise ties to that call, record a blocking question instead of
guessing.

A second, distinct phrasing pattern needs the identical zero-reasoning
treatment: when the document says to OVERRIDE an existing resource's
attribute -- "Override the pipeline-events queue's some_hardcoded_field
to new_value", or any phrasing naming a resource, a field, and a new
value -- this is a THIN MAPPING step only, never a re-drafting of that
resource. The target resource already exists (often created by a
blueprint call this same document also makes, sometimes by an entirely
separate stack); you do not redraft it, you do not touch any OTHER
attribute of it, and you do not add it to "resources" at all. Instead,
record ONE entry in the top-level "overrides" array: "address" the
target resource's own canonical "<stack>.<type>.<name>" address exactly
as the document names it, and "config" a JSON-encoded object string
mapping the real WIRE attribute name (never a friendlier synonym you
invent) to its new value, e.g.
"{\"some_hardcoded_field\":\"new_value\"}". If the document's own
override statement doesn't make the target address or field
unambiguous, record a blocking question instead of guessing which
resource or field it means.`
