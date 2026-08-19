// Package describe generates a one-sentence field description via a real
// Claude API call, or honestly abstains -- the LLM-based half of the
// provider-onboarding pipeline's own "source real descriptions from the
// model; where genuinely absent, generate one and label it visibly as
// AI-inferred" requirement (STATE.md's own real, current arc).
//
// Deliberately standalone, not adapted from intentprovider/claude: that
// package's own real, useful Adapter is hard-wired to intent-provider's
// own domain (a DraftRequest carrying a stack transcript, a tool-use loop
// for reading stack config, a JSON-Schema-constrained response shaped
// like a whole intent/v1 document). This package has exactly one job --
// given a field's own real name, type, constraints, enum values, and
// parent operation context, either produce a real description or abstain
// -- and nothing else. It follows the identical real, proven SDK usage
// pattern (github.com/anthropics/anthropic-sdk-go, Config{Model, APIKey},
// option.WithAPIKey, a JSON-Schema-constrained structured response,
// classifyError's own real error-bucketing shape) without importing
// intentprovider at all.
//
// Abstention is a real, common, FIRST-CLASS outcome, not an edge case: a
// bare field name with no other signal ("id", "name", "tags") is
// deliberately NOT enough on its own to produce a real description --
// see systemPromptText's own real instructions for exactly where the bar
// sits. A caller must check Result.Abstained explicitly; there is no
// "best-effort" description this package will ever silently pad from
// insufficient signal.
package describe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel mirrors intentprovider/claude's own identical real
// default -- this codebase's standing "good balance of cost/quality"
// choice for anything not explicitly pinned (see that package's own doc
// comment for the real cost-trap history behind this default).
const DefaultModel = "claude-sonnet-5"

// maxTokens bounds a single real request's own response -- generous for
// a one-sentence description plus a short abstention reason, well under
// any current model's own output ceiling. Deliberately small: this is a
// narrow, single-field task, never a multi-turn conversation.
const maxTokens = 1024

// Config configures New. APIKey is resolved by the caller -- this
// package never reads an environment variable or a ubx config file
// itself, the identical real division of responsibility
// intentprovider/claude's own Config already establishes. A zero-value
// APIKey falls through to the Anthropic SDK's own default credential
// chain (ANTHROPIC_API_KEY and beyond).
type Config struct {
	Model  string
	APIKey string
}

// FieldContext is the REAL, closed, minimal signal set this package's
// own real contract promises to use -- and only this. No caller may pass
// broader context (a whole spec, unrelated sibling fields, external
// documentation) -- the whole point of a narrow, honest abstention
// decision is that it's made from exactly the same limited signal a
// human skimming a schema diff would have, never from information the
// generated description's own reader won't also have.
type FieldContext struct {
	// Name is the field's own real wire name (e.g. "instance_class",
	// "queue_url").
	Name string
	// Type is a real, human-readable rendering of the field's own type
	// (e.g. "string", "number", "list of string", "object").
	Type     string
	Required bool
	Optional bool
	Computed bool
	// Enum is the field's own real, declared enum values, if any --
	// empty when the field's type doesn't declare one.
	Enum []string
	// Constraints is the field's own real, declared constraints, if
	// any, as short human-readable strings (e.g. "minimum: 1",
	// "maximum: 100", "pattern: ^[a-z][a-z0-9-]*$") -- empty when none
	// are declared.
	Constraints []string
	// ParentContext names the real resource/operation this field
	// belongs to (e.g. "aws_sqs_queue", "kubernetes_apps_deployment.spec"),
	// giving the model real, if limited, domain grounding without
	// handing over the whole schema.
	ParentContext string
}

// Result is Describe's own real, honest outcome -- see the package doc
// comment for why Abstained is a first-class, expected result, not an
// error.
type Result struct {
	// Description is the real, generated one-sentence description --
	// only meaningful when Abstained is false.
	Description string
	// Abstained is true when the model itself judged the given signal
	// insufficient to say something real, non-obvious, and honest.
	Abstained bool
	// Reason is the model's own real, short explanation for abstaining
	// -- always populated when Abstained is true, always empty
	// otherwise. Never silently discarded by this package -- a caller
	// that wants to know WHY a field has no description can always ask.
	Reason string
}

// Generator produces field descriptions via a real Claude API call.
type Generator struct {
	client anthropic.Client
	model  string
}

// New constructs a real Generator. Never touches the network itself --
// SDK client construction is a pure local operation, identical to
// intentprovider/claude.New's own real behavior; the first real request
// happens inside Describe.
func New(cfg Config) *Generator {
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &Generator{client: anthropic.NewClient(opts...), model: model}
}

// resultJSONSchema forces a real, structured, forced-choice response --
// never free text this package would need to parse heuristically. Both
// "description" and "reason" are always present in the real response
// (an empty string for whichever one doesn't apply); "abstained" is the
// one real field that decides which of the other two actually means
// something -- see Result's own doc comment.
var resultJSONSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"abstained", "description", "reason"},
	"properties": map[string]any{
		"abstained": map[string]any{
			"type":        "boolean",
			"description": "true if the given signal is not enough to write a real, honest, non-obvious description -- the common, correct outcome for a bare field name with no other real signal.",
		},
		"description": map[string]any{
			"type":        "string",
			"description": "A real, concise, one-sentence field description. Empty string if abstained is true.",
		},
		"reason": map[string]any{
			"type":        "string",
			"description": "A short, real explanation of why the given signal was insufficient. Empty string if abstained is false.",
		},
	},
}

// systemPromptText sets the real, deliberately strict abstention bar --
// see the package doc comment for why abstaining must be the common
// outcome for underspecified fields, not a rare edge case.
const systemPromptText = `You generate a single, one-sentence description for one field of a real API schema, to be inserted into auto-generated SDK reference documentation. The generated text will be labeled "AI-inferred" wherever it is shown -- this label is the reader's only integrity guarantee that the text was not written by whoever maintains the real API, so it must never restate the obvious or invent detail the given signal does not actually support.

You will be given exactly five things about the field, and nothing else: its name, its type, any declared constraints, any declared enum values, and the name of its real parent resource/operation. You have no access to the full specification, no examples, and no external knowledge beyond common, general API conventions.

Your job is a real, binary choice for every field:

1. Write a real, concise, one-sentence description ONLY if the given signal is genuinely sufficient to say something concrete that is NOT already obvious from the field's own name and type alone. Real, declared constraints (a range, a pattern, a real enum's own actual values) or a genuinely informative parent-context combination are the kind of signal that can justify writing something. A plausible-sounding guess is not enough -- if you are not confident the sentence is actually true of this real field, abstain instead.

2. Abstain (set abstained=true, and explain why in one short sentence) whenever the given signal is NOT enough. A bare, common field name with no other real signal ("id", "name", "tags", "description", "created_at", "status") -- even though you could easily write a plausible-sounding sentence for it -- is NOT enough signal on its own: abstain rather than restate the field name in sentence form. Abstaining is the common, correct, expected outcome for most simple fields, not a failure or a rare edge case -- if in doubt, abstain.

Never write a description that just restates the field's own name or type in sentence form (e.g. never "The name of the resource" for a field named "name"). Never invent a real-sounding detail (a default value, a real behavior, a real limit) that the given signal does not actually state.`

// Describe generates a description for one field, or abstains -- a
// single real API request, no retries beyond the SDK's own default
// policy, no conversation state carried between calls (each real field
// is independent).
func (g *Generator) Describe(ctx context.Context, field FieldContext) (Result, error) {
	resp, err := g.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     g.model,
		MaxTokens: maxTokens,
		System: []anthropic.TextBlockParam{{
			Text:         systemPromptText,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildUserPrompt(field))),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: resultJSONSchema},
		},
	})
	if err != nil {
		return Result{}, classifyError(err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return Result{}, fmt.Errorf("describe: refused (%s): %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
	}

	for _, block := range resp.Content {
		if block.Type != "text" {
			continue
		}
		var parsed struct {
			Abstained   bool   `json:"abstained"`
			Description string `json:"description"`
			Reason      string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(block.Text), &parsed); err != nil {
			return Result{}, fmt.Errorf("describe: parse structured response: %w", err)
		}
		return Result{Description: parsed.Description, Abstained: parsed.Abstained, Reason: parsed.Reason}, nil
	}
	return Result{}, errors.New("describe: response had no text content block")
}

// buildUserPrompt renders field's own real signal, and only that signal,
// as the real request body -- see FieldContext's own doc comment for why
// this is a closed set.
func buildUserPrompt(field FieldContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Field name: %s\n", field.Name)
	fmt.Fprintf(&b, "Type: %s\n", field.Type)
	fmt.Fprintf(&b, "Required: %v, Optional: %v, Computed: %v\n", field.Required, field.Optional, field.Computed)
	if len(field.Enum) > 0 {
		fmt.Fprintf(&b, "Enum values: %s\n", strings.Join(field.Enum, ", "))
	} else {
		b.WriteString("Enum values: (none declared)\n")
	}
	if len(field.Constraints) > 0 {
		fmt.Fprintf(&b, "Constraints: %s\n", strings.Join(field.Constraints, "; "))
	} else {
		b.WriteString("Constraints: (none declared)\n")
	}
	if field.ParentContext != "" {
		fmt.Fprintf(&b, "Parent resource/operation: %s\n", field.ParentContext)
	} else {
		b.WriteString("Parent resource/operation: (none given)\n")
	}
	return b.String()
}

// classifyError mirrors intentprovider/claude's own identical real
// error-bucketing shape (a bad/missing key must never be reported
// identically to a network timeout or a rate limit) -- restated here,
// not imported, since importing intentprovider/claude for one function
// would pull this standalone package back into that package's own real
// dependency graph for no reason beyond avoiding ~15 lines of
// duplication.
func classifyError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("describe: authentication failed (%d) -- check the configured API key: %w", apiErr.StatusCode, err)
		case 429:
			return fmt.Errorf("describe: rate limited, exhausted the SDK's own retry budget: %w", err)
		default:
			return fmt.Errorf("describe: API error (%d): %w", apiErr.StatusCode, err)
		}
	}
	if strings.HasPrefix(err.Error(), "no Anthropic credentials found") {
		return fmt.Errorf("describe: no credentials resolvable -- set ANTHROPIC_API_KEY or run `ant auth login`: %w", err)
	}
	return fmt.Errorf("describe: %w", err)
}
