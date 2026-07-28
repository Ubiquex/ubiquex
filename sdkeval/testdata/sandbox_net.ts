// stack()'s own describe callback is deliberately synchronous only
// (sdk/ts/runtime: `fn: () => void`) -- a real program has no way to
// `await` a network call from inside it at all, a TypeScript compile
// error before ever reaching runtime. The realistic attempt shape is a
// top-level await, before the module's own default export is even
// defined -- exercised here, unambiguously: if this throws, nothing
// after it (including the stack() definition itself) ever runs.
await fetch("http://169.254.169.254/should-not-matter");

import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 2: unreachable if the fetch above was actually blocked" });
  resource(FakeWidget, "primary", { name: "unreachable" });
});
