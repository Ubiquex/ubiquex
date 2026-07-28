// Adversarial row 1's own "backstop" case: Deno.pid is NOT something
// installNondeterminismGuards() (sdk/ts/evaluator/guards.ts) knows to
// block -- it's a legitimate, ordinary process-introspection value, not
// a clock/PRNG call, so the eager override can't and shouldn't catch it
// statically. It genuinely differs between DoubleRun's own two separate
// subprocess runs, though, which is exactly what core.DoubleRun (the
// second layer of defense) exists to catch instead.
import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 1: Deno.pid leaks real cross-process nondeterminism the guards can't see" });
  resource(FakeWidget, "primary", { name: `widget-pid-${Deno.pid}` });
});
