import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "a real, well-behaved program for sdkeval's own happy-path test" });
  resource(FakeWidget, "primary", { name: "primary-widget" });
});
