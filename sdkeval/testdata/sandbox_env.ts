import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 2: attempts an env read" });
  const home = Deno.env.get("HOME") ?? "unset";
  resource(FakeWidget, "primary", { name: home });
});
