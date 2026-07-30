import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "throws partway through, after one resource already ran" });
  resource(FakeWidget, "primary", { name: "primary-widget" });
  throw new Error("sdkeval adversarial row 4: deliberate mid-evaluation throw");
});
