package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// buildReceipt renders a human-readable Markdown summary of a
// scan-generated drift_adopt proposal — UBI-11 stage 3's "receipt"
// (docs/architecture.md — Decision loop: "drift surfaces as an issue or PR
// containing the generated proposal plus a receipt (diff, attribution if
// any, blast radius)"). Used for both issue and PR bodies.
//
// trailerHash, if non-empty, is rendered as the leading "ubx-proposal:
// <hash>" line PR mode's acceptance derivation depends on (docs/schema.md's
// pr_merge acceptance amendment) — issue mode passes an empty hash, since
// an issue is never merged/accepted, so there's nothing to derive from it.
func buildReceipt(p *core.Proposal, trailerHash, diffPreview string) string {
	var b strings.Builder
	if trailerHash != "" {
		fmt.Fprintf(&b, "ubx-proposal: %s\n\n", trailerHash)
	}
	fmt.Fprintf(&b, "## %s\n\n", p.Intent.Summary)
	fmt.Fprintf(&b, "- kind: `%s`\n", p.Kind)
	fmt.Fprintf(&b, "- stack: `%s`\n", p.Stack)
	fmt.Fprintf(&b, "- blast radius: +%d ~%d -%d\n\n",
		p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)

	if len(p.Intent.Sources) > 0 {
		b.WriteString("### Attribution\n\n")
		for _, s := range p.Intent.Sources {
			b.WriteString(receiptIntentSourceLine(s))
		}
		b.WriteString("\n")
	}

	if diffPreview != "" {
		b.WriteString("### .tf write-back preview\n\n```diff\n")
		b.WriteString(diffPreview)
		b.WriteString("\n```\n\n")
	}

	b.WriteString("<details><summary>Full proposal</summary>\n\n```json\n")
	if proposalJSON, err := json.MarshalIndent(p, "", "  "); err == nil {
		b.Write(proposalJSON)
	}
	b.WriteString("\n```\n</details>\n")

	return b.String()
}

// receiptIntentSourceLine renders one intent.sources entry as a single
// Markdown bullet — a condensed version of cli/why.go's renderIntentSource
// (full attribution detail belongs in `ubx why`, not a GitHub receipt,
// which favors a short, scannable summary a reviewer can take in at a
// glance).
func receiptIntentSourceLine(s core.IntentSource) string {
	switch s.Kind {
	case "cloudtrail":
		line := fmt.Sprintf("- **%s** %s at %s", s.ActorARN, s.EventName, s.EventTime)
		if s.SourceIP != "" {
			line += fmt.Sprintf(" from %s", s.SourceIP)
		}
		return line + "\n"
	case "cloudtrail_unattributed":
		return fmt.Sprintf("- attribution unavailable: %s\n", unattributedReason(s.Reason))
	default:
		return fmt.Sprintf("- %s: %s\n", s.Kind, s.Ref)
	}
}
