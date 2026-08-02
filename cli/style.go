package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Package cli's own visual-language foundation (UBI-61, docs/
// cli-output-spec.md): manual ANSI escape codes, not a third-party
// color/TUI library -- the palette is a small, fixed set of 7 codes, and
// there's no live-updating widget anywhere in this plan (only
// progressively-printed lines), so a dependency like fatih/color or
// lipgloss would cost real dependency-footprint weight (this project's
// own standing discipline) for nothing this actually needs.
//
// TTY detection is the one exception: an `os.ModeCharDevice` check alone
// is a known-insufficient heuristic -- verified directly, not assumed:
// /dev/null itself is a character device on this platform (confirmed via
// `stat`), so `ubx ship </dev/null` would otherwise be misdetected as an
// interactive terminal. golang.org/x/term's IsTerminal does the real
// ioctl-based check and was ALREADY present in this module's own
// dependency graph (v0.44.0, pulled in transitively) before this
// session -- using it directly adds no new third-party code to the
// build, just a direct import of something already fetched.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiPurple = "\x1b[35m"
)

// isTerminal reports whether v (expected to be cmd.OutOrStdout() or
// cmd.InOrStdin()) is a real interactive terminal -- false for anything
// else, including every hermetic test's bytes.Buffer/strings.Reader,
// which is exactly what makes color/interactive-prompt behavior
// automatically inert in tests without any test needing to opt out
// explicitly. A package var (not a bare inline check), following the
// same test-swappable pattern already established by
// configSearchStartDir (cli/config.go) and userHomeDir, since Go's
// testing framework has no way to fake a real terminal otherwise -- a
// test that specifically wants to exercise TTY-on behavior swaps this
// var for its own duration.
var isTerminal = func(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// terminalWidth returns v's own real column width via the same
// TIOCGWINSZ-style ioctl golang.org/x/term already provides for
// IsTerminal (term.GetSize) -- 0 if v isn't a real *os.File or the ioctl
// fails (a hermetic test's bytes.Buffer, or any other non-terminal
// writer). UBI-93 (docs/executor.md's own amendment): newProgressPrinter
// uses this to bound how much of a single row's own content it will ever
// render, so a long line (most commonly a real provider's own diagnostic
// error text) can never wrap onto a second physical terminal row and
// silently break the in-place-redraw row-counting invariant every other
// row's own cursor math depends on. A package var, same test-swappable
// convention as isTerminal.
var terminalWidth = func(v any) int {
	f, ok := v.(*os.File)
	if !ok {
		return 0
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// colorEnabled implements docs/cli-output-spec.md principle 2 exactly:
// NO_COLOR (https://no-color.org) and non-TTY both mean plain output --
// --json is never affected since JSON payloads are built and marshaled
// entirely separately from every styler-using render function below.
func colorEnabled(cmd *cobra.Command) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(cmd.OutOrStdout())
}

// styler is the one thing every render function in this package takes
// to decide whether to wrap text in ANSI codes -- a struct rather than a
// bare bool so call sites read as "st.Green(x)", not a color constant
// threaded everywhere by hand. The semantic mapping (founder-ratified,
// same session as docs/cli-output-spec.md): green creates/confirmations,
// yellow modifies/drift/verification, red destroys, blue hashes/
// identities, purple AI judgment/attribution.
type styler struct {
	enabled    bool
	fullHashes bool
}

// newStyler builds a styler from cmd's own output stream -- the one
// place colorEnabled is consulted; every render function downstream just
// takes the resulting *styler, never cmd or colorEnabled directly, so a
// render function is equally testable whether it's reached via a real
// command or constructed directly in a unit test.
func newStyler(cmd *cobra.Command) *styler {
	return &styler{enabled: colorEnabled(cmd)}
}

// newStylerFull is newStyler plus a command's own --full-hashes flag
// value, for every command that has one (docs/cli-output-spec.md
// principle 3: short hashes by default, --full-hashes opts into full).
func newStylerFull(cmd *cobra.Command, fullHashes bool) *styler {
	return &styler{enabled: colorEnabled(cmd), fullHashes: fullHashes}
}

// plainStyler is a always-disabled styler for callers that render outside
// of any single command's own output stream (e.g. a helper building a
// string for later use) or that explicitly want unstyled text.
func plainStyler() *styler { return &styler{enabled: false} }

// Hash renders id per this styler's own --full-hashes setting, blue
// (docs/cli-output-spec.md's ratified color semantics: blue = hashes/
// identities) -- the one call every render function should use for a
// proposal/plan hash, rather than composing displayHash + Blue by hand
// at each call site.
func (s *styler) Hash(id string) string {
	full := s != nil && s.fullHashes
	return s.Blue(displayHash(id, full))
}

// Ref is Hash's own marker-free sibling, for a hash rendered where it
// must stay directly copy-pasteable as a real command argument (a
// "next: ubx ship <hash>" hint, or a card naming the same hash) -- see
// shortRef's own doc comment for why the "…" marker breaks that use.
// Still honors --full-hashes.
func (s *styler) Ref(id string) string {
	if s != nil && s.fullHashes {
		return s.Blue(id)
	}
	return s.Blue(shortRef(id))
}

func (s *styler) color(code, text string) string {
	if s == nil || !s.enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (s *styler) Green(text string) string  { return s.color(ansiGreen, text) }
func (s *styler) Yellow(text string) string { return s.color(ansiYellow, text) }
func (s *styler) Red(text string) string    { return s.color(ansiRed, text) }
func (s *styler) Blue(text string) string   { return s.color(ansiBlue, text) }
func (s *styler) Purple(text string) string { return s.color(ansiPurple, text) }
func (s *styler) Dim(text string) string    { return s.color(ansiDim, text) }
func (s *styler) Bold(text string) string   { return s.color(ansiBold, text) }

// GreenBold combines both codes in one escape sequence (docs/cli-output-
// spec.md §v2: resource-create headers and the plan/ship footer are
// "green AND bold") -- color()'s own single-reset-at-the-end design
// means nesting a Bold(Green(text)) call would terminate the whole
// sequence early at the inner call's own reset, clobbering the outer
// style; this applies both codes once, correctly.
func (s *styler) GreenBold(text string) string {
	if s == nil || !s.enabled || text == "" {
		return text
	}
	return ansiBold + ansiGreen + text + ansiReset
}

// RedBold and YellowBold are GreenBold's own destroy/modify siblings
// (UBI-61 comment thread, the "general +/-/~ header rule": every
// "+/-/~ <address>" style header, wherever it renders, colors AND bolds
// by its own operation kind -- not just creates, which is all GreenBold
// covered before this). Same one-escape-sequence composition, same
// reasoning as GreenBold's own doc comment.
func (s *styler) RedBold(text string) string {
	if s == nil || !s.enabled || text == "" {
		return text
	}
	return ansiBold + ansiRed + text + ansiReset
}

func (s *styler) YellowBold(text string) string {
	if s == nil || !s.enabled || text == "" {
		return text
	}
	return ansiBold + ansiYellow + text + ansiReset
}

// resourceOpKind is the shared create/modify/destroy classification the
// general header rule keys off of -- factored into one small type so
// ship.go's live per-resource progress header (the one caller that must
// classify an address dynamically, one ProgressEvent at a time, rather
// than simply walking Delta.Creates/Modifies/Destroys) shares the exact
// three-way color+bold mapping every other header-rendering call site
// already applies directly (renderCreates' GreenBold, renderDestroys'
// RedBold, status.go's drift header YellowBold).
type resourceOpKind int

const (
	opUnknown resourceOpKind = iota
	opCreate
	opModify
	opDestroy
)

// OpHeader colors+bolds text per kind, or leaves it untouched for
// opUnknown (an address this session's classification couldn't place --
// a plain header is a harmless fallback, never a missing/blank line).
func (s *styler) OpHeader(kind resourceOpKind, text string) string {
	switch kind {
	case opCreate:
		return s.GreenBold(text)
	case opModify:
		return s.YellowBold(text)
	case opDestroy:
		return s.RedBold(text)
	default:
		return text
	}
}

// forceBold makes an ALREADY-composed line bold throughout, including
// through any other color() calls embedded inside it (docs/cli-output-
// spec.md §v2: "each line BOLD," e.g. the delta/blast-radius summary,
// where the create/modify/destroy counts individually stay green/
// yellow/red exactly as before). Nesting Bold(text) around a string
// that itself contains other styled segments doesn't work with this
// package's single-reset-at-the-end color() design: each inner call's
// own ansiReset would cancel the outer bold partway through. This
// re-asserts bold immediately after every embedded reset instead, so
// bold persists across every inner color segment and the plain text
// between them alike.
func (s *styler) forceBold(line string) string {
	if s == nil || !s.enabled || line == "" {
		return line
	}
	return ansiBold + strings.ReplaceAll(line, ansiReset, ansiReset+ansiBold) + ansiReset
}

// forceDim is forceBold's own dim sibling (UBI-68): a per-resource line's
// metadata segment (kind, hash, "accepted <timestamp>") should read as one
// dim block -- including a hash that Hash() already colored blue -- with
// the resource's own address, printed outside this call, left at full
// brightness so it reads brightest. Same reassert-after-every-embedded-
// reset technique as forceBold, for the same reason: color()'s single-
// reset-at-the-end design means a naive Dim(text-with-embedded-colors)
// call would have its dim effect cancelled by the first inner reset.
func (s *styler) forceDim(line string) string {
	if s == nil || !s.enabled || line == "" {
		return line
	}
	return ansiDim + strings.ReplaceAll(line, ansiReset, ansiReset+ansiDim) + ansiReset
}

// displayHash is the one canonical hash-truncation convention for the
// whole package (docs/cli-output-spec.md principle 3: "short hashes (12
// chars) everywhere; --full-hashes opts into full") -- replaces the two
// slightly-inconsistent truncation helpers this codebase grew
// independently (plan.go's own shortHash: 12 chars, no marker; why.go's
// own shortID: 12 chars + "…"), unified on shortID's own "+ …" shape
// since it's the one that visually signals truncation happened at all.
// full=true (only ever set from a --full-hashes flag) returns the
// untouched string -- this is purely a display convention, never used
// for lookup (resolvePlanHash and friends already accept any prefix
// length independent of this).
func displayHash(id string, full bool) string {
	const shortLen = 12
	if full || len(id) <= shortLen {
		return id
	}
	return id[:shortLen] + "…"
}

// displayResourceState translates a core.ResourceState (or an
// executor.ProgressEvent.State, the identical wire vocabulary) into ship's
// own display vocabulary for any HUMAN-rendered, non-JSON line (UBI-75/
// UBI-79): "shipped," never "applied," anywhere a person reads it. The
// stored/hashed value itself -- core.ResourceState's own enum, an
// ApplyRecord's own JSON -- is completely untouched by this; it's a
// display-layer mapping over an already-computed string, the same posture
// "shipping" (over the unchanged in_flight value) already established.
func displayResourceState(s string) string {
	if s == "applied" {
		return "shipped"
	}
	return s
}

// displayOutcome is displayResourceState's own core.ApplyRecord.Summary.
// Outcome sibling (UBI-75/UBI-79): applied/partially_applied -> shipped/
// partially_shipped for human text only -- failed already carries no
// apply-family word and passes through unchanged. --json output keeps the
// real, unmangled stored value (shipJSON/whyJSON wrap core.ApplyRecord
// directly) -- this helper is never called from a JSON-encoding path.
func displayOutcome(s string) string {
	switch s {
	case "applied":
		return "shipped"
	case "partially_applied":
		return "partially_shipped"
	default:
		return s
	}
}

// shortRef is displayHash's own marker-free sibling, for the one context
// where truncation must stay directly reusable as a real command
// argument: a "next: ubx ship <hash>" handoff (or any other hint meant to
// be copied and run verbatim). displayHash's trailing "…" is exactly
// right for a purely informational display (blame/why/status/promote's
// own "set by"/"accepted"/"promoted from" lines, never re-typed), but
// would silently corrupt a copy-pasted argument here -- "…" isn't a legal
// hex char, so resolvePlanHash would refuse it as a non-matching prefix.
// Both are still just display conventions: resolvePlanHash itself accepts
// any real hex prefix length, short or full.
func shortRef(id string) string {
	const shortLen = 12
	if len(id) <= shortLen {
		return id
	}
	return id[:shortLen]
}
