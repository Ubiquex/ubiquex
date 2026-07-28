// installNondeterminismGuards -- the eager Date/Math.random override
// docs/sdk.md's own "The clock/random gap" section commits to: layer 1
// of a two-layer defense, closing the overwhelmingly common case (an
// accidental Date.now()/Math.random() call landing directly in a
// resource's own config value) with an immediate, legible failure named
// at the call site -- core.DoubleRun (sdkeval, Go side) is layer 2, the
// backstop for whatever this override can't see statically (e.g.
// Deno.pid, or native Map/Set iteration order built from genuinely
// timing-dependent insertion).
//
// This module is NOT part of `@ubx/sdk` itself (sdk/ts/runtime) -- a
// program author never imports it, and it is never published to npm. It
// is evaluator-harness-only code, embedded into the `ubx` binary
// (sdk/ts/embed.go) and statically imported by a freshly-generated
// per-invocation runner script (sdkeval's own runner.go) BEFORE that
// runner's own literal import of the program's entry file -- safe
// because `stack()` (sdk/ts/runtime) defers running a program's own
// describe function to an explicit `.evaluate()` call the runner makes
// only after this module's own installNondeterminismGuards() has already
// run, regardless of ES module import/evaluation ordering (confirmed
// empirically this session: a module's own top-level statements only run
// after every one of its static imports has already been evaluated, but
// `stack()`'s own deferred-execution design means the imported program
// module's own body never touches Date/Math.random at all -- only a
// later, explicit .evaluate() call does).

/** Thrown by the overridden Date/Math.random the instant a program's own
 * code calls them -- see docs/sdk.md's own adversarial row 1. */
export class NondeterministicAPIError extends Error {
  constructor(api: string) {
    super(
      `${api} is nondeterministic and forbidden inside an SDK program's own describe function -- ` +
        "ubx's own evaluator guards against it eagerly rather than risk two independent double-run " +
        "passes silently producing different output.",
    );
    this.name = "NondeterministicAPIError";
  }
}

/** installNondeterminismGuards replaces globalThis.Date and Math.random
 * with versions that throw NondeterministicAPIError. Idempotent -- safe
 * to call more than once (the second call's own "already installed"
 * check is a no-op, not a re-wrap of an already-guarded Date). */
export function installNondeterminismGuards(): void {
  // deno-lint-ignore no-explicit-any
  const g = globalThis as any;
  if (g.__ubxNondeterminismGuardsInstalled) {
    return;
  }
  g.__ubxNondeterminismGuardsInstalled = true;

  const RealDate = Date;

  class GuardedDate extends RealDate {
    constructor(...args: unknown[]) {
      if (args.length === 0) {
        // The one legitimate reason to construct a Date with zero
        // arguments is "read the current wall-clock time" -- exactly
        // the nondeterministic case. A Date built from explicit
        // arguments (a literal timestamp, a parsed ISO string) is
        // fully deterministic and stays allowed -- rejecting it too
        // would be over-blocking a real, legitimate use, not closing a
        // real gap.
        throw new NondeterministicAPIError("new Date()");
      }
      // TypeScript can't resolve a spread of a general unknown[] against
      // Date's own overloaded constructor signatures (a well-known
      // limitation of extending a built-in with a variadic super() call,
      // not specific to this file) -- args.length > 0 is already
      // confirmed above, so this is a real, safe pass-through, not a
      // masked bug.
      // @ts-expect-error -- see comment above
      super(...args);
    }

    static override now(): number {
      throw new NondeterministicAPIError("Date.now()");
    }
  }

  g.Date = GuardedDate;
  g.Math.random = () => {
    throw new NondeterministicAPIError("Math.random()");
  };
}
