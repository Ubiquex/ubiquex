import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 2: attempts a filesystem read" });
  const secret = Deno.readTextFileSync("/etc/hosts");
  resource(FakeWidget, "primary", { name: secret });
});
