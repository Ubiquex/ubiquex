package intentprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
)

// MaxAttempts is DraftWithRetry's own attempt budget -- one first attempt
// plus two retries-with-errors, matching
// docs/intent-provider-adversarial.md row 7's own required outcome
// ("invalid JSON thrice" hard-fails on the third consecutive failure).
const MaxAttempts = 3

// AttemptError is one failed attempt's own record, inside DraftError.
type AttemptError struct {
	Attempt int
	Errors  []string
}

// DraftError is returned once MaxAttempts is exhausted -- never a
// silent partial/best-effort acceptance of the last attempt's own still-
// invalid output.
type DraftError struct {
	Attempts []AttemptError
}

func (e *DraftError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "intentprovider: draft failed validation after %d attempts", len(e.Attempts))
	for _, a := range e.Attempts {
		fmt.Fprintf(&b, "\n  attempt %d: %s", a.Attempt, strings.Join(a.Errors, "; "))
	}
	return b.String()
}

// DraftWithRetry drives a through up to MaxAttempts structured-output
// rounds against one document, validating each raw response against
// intent/v1 (validate.go's parseAndValidate) before accepting it.
//
// Two layers of validation run on every attempt, neither trusted alone
// (docs/intent-provider.md's own "Structured-output validation" section
// -- the same "a claimed guarantee is never sufficient by itself"
// posture UBI-44's own post-destroy read-back generalized, applied here
// to a vendor's own schema-conformance claim instead of a provider's own
// apply-success claim): an adapter's own API-level schema constraint
// (adapter-specific, best-effort -- e.g. intentprovider/claude's own
// output_config.format) and this function's own ubx-side semantic
// validation (adapter-agnostic, always runs, trusted unconditionally).
//
// On a validation failure, the driver retries within the SAME logical
// attempt sequence, feeding the exact rejected output and validation
// errors back to the adapter verbatim (Adapter.Draft's own PriorOutput/
// PriorErrors) -- never a client-side patch of the adapter's own output.
// On the MaxAttempts'th consecutive failure, returns *DraftError naming
// every attempt's own errors; never a partial or corrupted draft.
//
// An error returned directly from a.Draft (network failure,
// authentication failure, a policy refusal) aborts immediately, without
// consuming a retry -- distinct from a validation failure, and never
// retried, since re-asking the identical model call cannot fix a
// transport/auth/policy problem (docs/intent-provider-adversarial.md row
// 6's own required distinction between the two failure categories).
//
// The returned *resolver.IntentFile has NOT yet had its own
// Intent.Sources populated with document/intent_provider evidence
// (sources.go's PopulateSources) -- DraftWithRetry's job ends at "a
// valid draft"; provenance is the caller's own next step, since only the
// caller knows the source document's real content_hash.
func DraftWithRetry(ctx context.Context, a Adapter, stack string, content []byte) (*resolver.IntentFile, json.RawMessage, error) {
	var attempts []AttemptError
	var priorOutput json.RawMessage
	var priorErrors []string

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		raw, err := a.Draft(ctx, DraftRequest{
			Stack:       stack,
			Content:     content,
			Attempt:     attempt,
			PriorOutput: priorOutput,
			PriorErrors: priorErrors,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("intentprovider: adapter %q attempt %d: %w", a.Name(), attempt, err)
		}

		file, validationErrs := parseAndValidate(raw, stack)
		if len(validationErrs) == 0 {
			return file, raw, nil
		}

		attempts = append(attempts, AttemptError{Attempt: attempt, Errors: validationErrs})
		priorOutput = raw
		priorErrors = validationErrs
	}

	return nil, nil, &DraftError{Attempts: attempts}
}
