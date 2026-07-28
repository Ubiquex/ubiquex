import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "adversarial row 1: Date.now() used in a resource's own config" });
  resource(FakeWidget, "primary", { name: `widget-${Date.now()}` });
});
