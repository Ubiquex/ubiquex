package core

import (
	"fmt"
	"strings"
)

// evaloutput.go turns an evaluator subprocess's unusable output into an
// error that says what happened.
//
// Every evaluator (goeval, tseval, pyeval) runs a user's program and
// parses its stdout as an intent/v1 document. Each one used to surface
// the subprocess's stderr ONLY when it exited nonzero, and to report a
// parse failure as the decoder saw it. A program that exited 0 and
// wrote nothing produced:
//
//	plan: pyeval: decode json: EOF
//
// which names the decoder, says nothing about the program, and throws
// away whatever the program wrote to stderr on its way out.
//
// That is not hypothetical: a real Python stack called ubx.stack(...)
// instead of ubx.run(...), which builds a stack definition and returns
// it rather than evaluating and printing one. It exited 0 in silence,
// and "decode json: EOF" was the entire diagnosis available for a
// program that had simply never been asked to do anything.

// EvaluatorHint is the likeliest cause of an empty document in a given
// language. It differs per evaluator because the entry-point convention
// differs.
//
// A hint is phrased as the usual cause, never as a diagnosis, because
// it is a guess by construction: a program that wrote nothing left
// nothing to diagnose from. An early exit produces the same silence as
// a missing entry point, and stating the wrong cause as fact would send
// a reader looking in the wrong place, which is the failure this whole
// file exists to stop.
type EvaluatorHint string

const (
	// HintGo is sdk.Main's own contract.
	HintGo EvaluatorHint = "the stack definition was never passed to sdk.Main. sdk.Main(sdk.Stack(name, fn)) is what evaluates a stack and writes the document, so a main() that builds a definition and does nothing with it exits successfully having done nothing"

	// HintTS covers a program whose stack definition never becomes the
	// default export the generated runner imports.
	HintTS EvaluatorHint = "the stack definition was never exported as the module's default. The generated runner evaluates the default export, so a definition that is only assigned to a local never runs"

	// HintPy is the one this was found by.
	HintPy EvaluatorHint = "ubx.run(name, fn) was never called. ubx.stack(name, fn) only BUILDS a definition and returns it, so a program calling stack() alone exits successfully having done nothing"
)

// ExplainEvaluatorOutput reports why an evaluator's output could not be
// used, preferring the program's own words over the parser's.
//
// stderr is included whatever the exit code, which is the actual fix:
// a program can write a diagnosis and still exit 0, and that diagnosis
// used to be discarded precisely when it was the only thing available.
//
// The empty-output case is separated out because it has a specific,
// common cause worth naming rather than describing as a parse failure.
// The evaluator's own name is deliberately NOT included: every caller
// already wraps this in "pyeval: %w" or its equivalent, and including
// it here produced "pyeval: pyeval: ..." on the first real run.
func ExplainEvaluatorOutput(stdout, stderr []byte, hint EvaluatorHint, cause error) error {
	msg := strings.TrimSpace(string(stderr))

	if len(strings.TrimSpace(string(stdout))) == 0 {
		out := fmt.Sprintf("the program exited successfully but wrote no intent document. This usually means %s", hint)
		if msg != "" {
			out += "\n\nit wrote this to stderr:\n" + msg
		}
		return fmt.Errorf("%s", out)
	}

	// Non-empty but unparseable. Show what arrived, bounded, since the
	// usual cause is a program printing something of its own alongside
	// (or instead of) the document.
	out := fmt.Sprintf("the program's output is not a valid intent document: %v", cause)
	if msg != "" {
		out += "\n\nit wrote this to stderr:\n" + msg
	}
	out += "\n\nit wrote this to stdout:\n" + truncateForError(string(stdout))
	return fmt.Errorf("%s", out)
}

// truncateForError bounds what a failure quotes back. A program that
// printed a megabyte of something is still best diagnosed from its
// first few lines, and an unbounded error is its own problem.
func truncateForError(s string) string {
	const max = 2000
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... (truncated, %d bytes total)", len(s))
}
