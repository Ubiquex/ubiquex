package core

import (
	"errors"
	"strings"
	"testing"
)

// The defect these cover: an evaluator that exits 0 having written
// nothing produced "decode json: EOF", which names the decoder and says
// nothing about the program. See evaloutput.go's own doc comment for the
// real stack that hit it.

func TestExplainEvaluatorOutput_EmptyStdout_SaysWhatHappened(t *testing.T) {
	err := ExplainEvaluatorOutput(nil, nil, HintPy, errors.New("decode json: EOF"))
	msg := err.Error()

	if !strings.Contains(msg, "exited successfully but wrote no intent document") {
		t.Fatalf("message does not say what happened: %s", msg)
	}
	if !strings.Contains(msg, "ubx.run(name, fn) was never called") {
		t.Fatalf("message does not carry the language's own hint: %s", msg)
	}
	// The whole point is to stop leading the reader to the decoder.
	if strings.Contains(msg, "decode json") {
		t.Fatalf("message still names the decoder: %s", msg)
	}
}

func TestExplainEvaluatorOutput_EmptyStdout_KeepsStderr(t *testing.T) {
	err := ExplainEvaluatorOutput(
		[]byte("   \n  "), // whitespace only is still "wrote nothing"
		[]byte("Traceback (most recent call last):\n  boom\n"),
		HintGo,
		errors.New("decode json: EOF"),
	)
	msg := err.Error()

	if !strings.Contains(msg, "Traceback (most recent call last)") {
		t.Fatalf("stderr was discarded on a zero exit: %s", msg)
	}
	if !strings.Contains(msg, "exited successfully but wrote no intent document") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestExplainEvaluatorOutput_UnparseableStdout_ShowsBoth(t *testing.T) {
	err := ExplainEvaluatorOutput(
		[]byte("not json at all"),
		[]byte("warning: something"),
		HintTS,
		errors.New("decode json: invalid character 'o'"),
	)
	msg := err.Error()

	for _, want := range []string{
		"not a valid intent document",
		"not json at all",    // what it did write
		"warning: something", // and what it said about it
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
	// A program that wrote SOMETHING is not the "never called run" case,
	// so the hint would be a guess. It stays out.
	if strings.Contains(msg, "usually means") {
		t.Fatalf("empty-output hint offered for output that was not empty: %s", msg)
	}
}

func TestExplainEvaluatorOutput_NoDoubledPrefix(t *testing.T) {
	// Every caller already wraps this in "pyeval: %w" or its equivalent.
	// The first real run printed "plan: pyeval: pyeval: ...".
	err := ExplainEvaluatorOutput(nil, nil, HintPy, errors.New("x"))
	if strings.Contains(err.Error(), "pyeval") {
		t.Fatalf("helper names the evaluator its caller already named: %s", err)
	}
}

func TestExplainEvaluatorOutput_TruncatesRunawayStdout(t *testing.T) {
	huge := strings.Repeat("x", 50_000)
	err := ExplainEvaluatorOutput([]byte(huge), nil, HintGo, errors.New("x"))
	msg := err.Error()

	if len(msg) > 10_000 {
		t.Fatalf("a 50KB stdout reached the terminal whole: %d bytes", len(msg))
	}
	if !strings.Contains(msg, "truncated") {
		t.Fatalf("truncation is silent: %s", msg)
	}
}
