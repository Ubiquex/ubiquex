// A tiny, hand-written fake binding, mirroring fake_widget's real schema
// (provider/internal/fakeprovider) -- standing in for a real `ubx sdk
// gen`-generated one, since this fixture is about `ubx resolve
// --from-code`'s own CLI wiring, not codegen (already covered by
// sdk/codegen/... 's own tests).
import type { FieldMap, ResourceBinding } from "@ubx/sdk";

export interface FakeWidgetConfig {
  name: string;
  tags?: Record<string, string>;
}
export interface FakeWidgetAttrs {
  id: string;
  name: string;
  tags: Record<string, string>;
}

const fields: FieldMap = { name: "name", tags: "tags" };

export const FakeWidget: ResourceBinding<FakeWidgetConfig, FakeWidgetAttrs> = {
  wireType: "fake_widget",
  fields,
};
