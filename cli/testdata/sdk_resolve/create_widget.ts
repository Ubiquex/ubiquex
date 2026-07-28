import { intent, resource, stack } from "@ubx/sdk";
import { FakeWidget } from "./bindings.ts";

export default stack("payments", () => {
  intent({ summary: "add widget1, authored in TypeScript" });
  resource(FakeWidget, "widget1", { name: "widget1", tags: { env: "prod" } });
});
