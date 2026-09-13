// Command prefetchpython downloads and caches the pinned CPython-WASI
// build the Python evaluator needs, and prints where it landed.
//
// It exists so CI can acquire that asset in a named setup step beside
// bubblewrap and wasmtime (UBI-255). Without it, the first Python test
// that happens to run pulls 42MB from a third-party GitHub release
// mid-suite, and an outage there surfaces as a handful of
// unrelated-looking test failures rather than as one honest setup
// failure at the step that caused it.
//
// It deliberately calls pyeval's own acquisition rather than curling the
// URL itself. The version, the URL and the cache location then stay in
// one place; a CI step with its own hardcoded URL would be a second copy
// of a pin, which is exactly the drift this project keeps paying for.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ubiquex/ubiquex/pyeval"
)

func main() {
	// Generous but bounded: a 42MB download over a slow runner link,
	// with pyeval's own retries inside it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dir, err := pyeval.PrefetchInterpreter(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prefetchpython: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(dir)
}
