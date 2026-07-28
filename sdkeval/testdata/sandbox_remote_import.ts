// Adversarial row 2b: session 1's own real, empirically-found gap --
// dynamic remote import bypasses --deny-net entirely (Deno treats
// module resolution and network access as two separate permission
// surfaces); only --no-remote closes it. Exercised here as a top-level
// await, same reasoning as sandbox_net.ts -- stack()'s own describe
// callback can't be async at all.
await import("https://deno.land/std/version.ts");

import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 2b: unreachable if the remote import above was actually blocked" });
  resource(FakeWidget, "primary", { name: "unreachable" });
});
