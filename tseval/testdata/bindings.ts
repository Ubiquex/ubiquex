// A tiny, hand-written fake binding -- standing in for a real `ubx sdk
// gen`-generated one, since sdkeval's own tests are about the evaluator
// harness, not codegen (already covered by sdk/codegen/... 's own
// tests). Mirrors fake_widget's real schema (provider/internal/
// fakeprovider): id (computed), name (required), tags (optional map).
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
