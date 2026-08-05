// Hermetic tests for @ubx/sdk's own runtime (docs/sdk.md slice 3:
// "hermetically tested against fake codegen'd bindings, no real provider
// needed yet"). Run with `deno test` from sdk/ts/runtime/ -- no network,
// no fs, no env access needed by these tests themselves (the same
// hermetic posture the eventual evaluator harness enforces on a real
// program, exercised here on this package's own code first).

import {
  addressOf,
  ComputedCoercionError,
  cross,
  intent,
  isComputed,
  override,
  popBlueprintSource,
  pushBlueprintSource,
  resource,
  secret,
  stack,
  type FieldMap,
  type ResourceBinding,
} from "./index.ts";
import { assert, assertEquals, assertThrows } from "./testing.ts";

// --- fake codegen'd bindings, hand-written, mirroring exactly what
// sdk/codegen/templates/ts would generate for a small fixture schema. ---

interface WidgetConfig {
  name: string;
  tags?: Record<string, string>;
  settings?: { enabled: boolean };
  rules?: { fromPort: number }[];
}
interface WidgetAttrs {
  id: string;
  name: string;
  tags: Record<string, string>;
  settings: { enabled: boolean };
  rules: { fromPort: number }[];
}

const widgetFields: FieldMap = {
  name: "name",
  tags: "tags",
  settings: { wireName: "settings", kind: "object", fields: { enabled: "enabled" } },
  rules: { wireName: "rules", kind: "list", fields: { fromPort: "from_port" } },
};

const Widget: ResourceBinding<WidgetConfig, WidgetAttrs> = {
  wireType: "fake_widget",
  fields: widgetFields,
};

interface MirrorConfig {
  targetId: string;
}
interface MirrorAttrs {
  id: string;
  targetId: string;
}
const Mirror: ResourceBinding<MirrorConfig, MirrorAttrs> = {
  wireType: "fake_mirror",
  fields: { targetId: "target_id" },
};

Deno.test("stack/resource/intent -- basic document shape", () => {
  const def = stack("payments", () => {
    intent({ summary: "provision a widget" });
    resource(Widget, "primary", { name: "primary-widget" });
  });

  const doc = def.evaluate();
  assertEquals(doc.schema_version, 1);
  assertEquals(doc.kind, "ubx:intent/v1");
  assertEquals(doc.stack, "payments");
  assertEquals(doc.intent.summary, "provision a widget");
  assertEquals(doc.resources.length, 1);
  assertEquals(doc.resources[0], {
    type: "fake_widget",
    name: "primary",
    op: "create",
    config: { name: "primary-widget" },
  });
});

Deno.test("resource() returns a Computed<T> handle; passing it into a sibling config emits $ref", () => {
  const def = stack("payments", () => {
    intent({ summary: "wire a mirror to a widget" });
    const primary = resource(Widget, "primary", { name: "primary-widget" });
    resource(Mirror, "copy", { targetId: primary.id as unknown as string });
  });

  const doc = def.evaluate();
  const mirror = doc.resources.find((r) => r.type === "fake_mirror");
  assertEquals(mirror?.config, { target_id: { $ref: { to: "payments.fake_widget.primary.id" } } });
});

Deno.test("nested object field serializes with wire-name mapping applied recursively", () => {
  const def = stack("payments", () => {
    intent({ summary: "widget with settings" });
    resource(Widget, "primary", { name: "primary-widget", settings: { enabled: true } });
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].config, {
    name: "primary-widget",
    settings: { enabled: true },
  });
});

Deno.test("list-of-nested-object field serializes each element with wire-name mapping", () => {
  const def = stack("payments", () => {
    intent({ summary: "widget with rules" });
    resource(Widget, "primary", {
      name: "primary-widget",
      rules: [{ fromPort: 22 }, { fromPort: 443 }],
    });
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].config, {
    name: "primary-widget",
    rules: [{ from_port: 22 }, { from_port: 443 }],
  });
});

Deno.test("a Computed<T> nested inside a list-of-object element also becomes a $ref", () => {
  const def = stack("payments", () => {
    intent({ summary: "widget referencing another resource inside a nested list element" });
    const primary = resource(Widget, "primary", { name: "primary-widget" });
    resource(Widget, "secondary", {
      name: "secondary-widget",
      rules: [{ fromPort: primary.rules as unknown as number }],
    });
  });
  const doc = def.evaluate();
  const secondary = doc.resources.find((r) => r.name === "secondary");
  assertEquals(secondary?.config, {
    name: "secondary-widget",
    rules: [{ from_port: { $ref: { to: "payments.fake_widget.primary.rules" } } }],
  });
});

Deno.test("secret() and cross() markers pass through unchanged", () => {
  const def = stack("payments", () => {
    intent({ summary: "widget with a secret and a cross-stack ref" });
    resource(Mirror, "copy", {
      targetId: secret("aws_secrets_manager", "payments/token"),
    });
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].config, {
    target_id: { $secret: { backend: "aws_secrets_manager", path: "payments/token" } },
  });
});

Deno.test("cross() builds the exact $cross marker shape", () => {
  const ref = cross<string>("../networking", "networking.aws_vpc.main.id");
  // deno-lint-ignore no-explicit-any
  assertEquals(ref as any, { $cross: { ledger_dir: "../networking", to: "networking.aws_vpc.main.id" } });
});

Deno.test("Computed<T> is recognized by identity, never by structural duck-typing", () => {
  const lookalike = { __ubxAddress: "payments.fake_widget.primary.id" };
  assert(!isComputed(lookalike));

  const def = stack("payments", () => {
    intent({ summary: "x" });
    const primary = resource(Widget, "primary", { name: "x" });
    assert(isComputed(primary.id));
    assert(isComputed(primary));
  });
  def.evaluate();
});

Deno.test("using a Computed<T> as a real JS value throws ComputedCoercionError", () => {
  stack("payments", () => {
    intent({ summary: "x" });
    const primary = resource(Widget, "primary", { name: "x" });
    assertThrows(() => `${primary.id}`, ComputedCoercionError);
    assertThrows(() => JSON.stringify(primary.id), ComputedCoercionError);
    assertThrows(() => String(primary.id), ComputedCoercionError);
  }).evaluate();
});

Deno.test("addressOf extracts a real Computed<T>'s own address", () => {
  stack("payments", () => {
    intent({ summary: "x" });
    const primary = resource(Widget, "primary", { name: "x" });
    assertEquals(addressOf(primary.id), "payments.fake_widget.primary.id");
    assertEquals(addressOf(primary.settings.enabled), "payments.fake_widget.primary.settings.enabled");
  }).evaluate();
});

Deno.test("addressOf throws for a value that isn't a real Computed<T>", () => {
  assertThrows(() => addressOf({ __ubxAddress: "fake" } as never));
});

Deno.test("duplicate resource name in the same stack throws", () => {
  const def = stack("payments", () => {
    intent({ summary: "x" });
    resource(Widget, "primary", { name: "a" });
    resource(Widget, "primary", { name: "b" });
  });
  assertThrows(() => def.evaluate(), Error, "duplicate resource");
});

Deno.test("intent() called twice in one stack() body throws", () => {
  const def = stack("payments", () => {
    intent({ summary: "first" });
    intent({ summary: "second" });
  });
  assertThrows(() => def.evaluate(), Error, "more than once");
});

Deno.test("intent() never called -- evaluate() throws (missing summary is a hard failure)", () => {
  const def = stack("payments", () => {
    resource(Widget, "primary", { name: "a" });
  });
  assertThrows(() => def.evaluate(), Error, "intent() was never called");
});

Deno.test("resource()/intent() called outside an active stack() evaluation throws", () => {
  assertThrows(() => resource(Widget, "primary", { name: "a" }), Error, "outside of an active stack()");
  assertThrows(() => intent({ summary: "x" }), Error, "outside of an active stack()");
});

Deno.test("an unrecognized config field throws, naming the field", () => {
  const def = stack("payments", () => {
    intent({ summary: "x" });
    // deno-lint-ignore no-explicit-any
    resource(Widget, "primary", { name: "a", notARealField: 1 } as any);
  });
  assertThrows(() => def.evaluate(), TypeError, "notARealField");
});

Deno.test("a function in config throws -- intent/v1 cannot represent it", () => {
  const def = stack("payments", () => {
    intent({ summary: "x" });
    // deno-lint-ignore no-explicit-any
    resource(Widget, "primary", { name: "a", tags: { x: (() => {}) as any } });
  });
  assertThrows(() => def.evaluate(), TypeError, "function");
});

Deno.test("a scalar-map field's own keys pass through untranslated -- never looked up in a FieldMap", () => {
  // Regression test for a real bug found this session: the scalar-leaf
  // serialization path once reused the field-map-translating serializer
  // with an empty {} map, which made ANY map/tags key throw "unrecognized
  // config field" (map keys are arbitrary, user-chosen, and were never
  // supposed to be looked up in a resource's own FieldMap at all).
  const def = stack("payments", () => {
    intent({ summary: "x" });
    resource(Widget, "primary", {
      name: "a",
      // "Owner-Team" would not round-trip through any wire-name
      // translation at all -- proving these keys are truly untouched.
      tags: { "Owner-Team": "payments", env: "prod" },
    });
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].config, {
    name: "a",
    tags: { "Owner-Team": "payments", env: "prod" },
  });
});

Deno.test("thrown exception mid-evaluation produces no partial resources -- evaluate() itself throws", () => {
  const def = stack("payments", () => {
    intent({ summary: "x" });
    resource(Widget, "primary", { name: "a" });
    throw new Error("boom");
  });
  assertThrows(() => def.evaluate(), Error, "boom");
});

Deno.test("evaluate() runs the describe function exactly once per call, and is repeatable across calls", () => {
  let calls = 0;
  const def = stack("payments", () => {
    calls++;
    intent({ summary: "x" });
    resource(Widget, "primary", { name: "a" });
  });
  def.evaluate();
  def.evaluate();
  assertEquals(calls, 2);
});

Deno.test("override() round trip -- ticket's own worked example verbatim", () => {
  const def = stack("payments", () => {
    intent({ summary: "s" });
    override("payments.aws_sqs_queue.pipeline-events", { some_hardcoded_field: "new_value" });
  });
  const doc = def.evaluate();
  assertEquals(doc.overrides?.length, 1);
  assertEquals(doc.overrides?.[0], {
    address: "payments.aws_sqs_queue.pipeline-events",
    config: { some_hardcoded_field: "new_value" },
  });
});

Deno.test("override() config value can be a Computed<T> reference", () => {
  const def = stack("payments", () => {
    intent({ summary: "s" });
    const primary = resource(Widget, "primary", { name: "primary-widget" });
    override("payments.fake_widget.mirror", { owner_ref: primary.id as unknown as string });
  });
  const doc = def.evaluate();
  assertEquals(doc.overrides?.[0].config, { owner_ref: { $ref: { to: "payments.fake_widget.primary.id" } } });
});

Deno.test("no override() calls -- \"overrides\" is entirely absent from the wire document", () => {
  const def = stack("payments", () => {
    intent({ summary: "s" });
  });
  const doc = def.evaluate();
  assert(!("overrides" in doc));
});

Deno.test("override() with an empty address throws", () => {
  const def = stack("payments", () => {
    intent({ summary: "s" });
    override("", { x: "y" });
  });
  assertThrows(() => def.evaluate(), Error, "address is required");
});

Deno.test("override() with empty config throws", () => {
  const def = stack("payments", () => {
    intent({ summary: "s" });
    override("payments.fake_widget.a", {});
  });
  assertThrows(() => def.evaluate(), Error, "config is required");
});

Deno.test("override() called outside an active stack() evaluation throws", () => {
  assertThrows(() => override("payments.fake_widget.a", { x: "y" }), Error, "called outside of an active stack");
});

// --- pushBlueprintSource/popBlueprintSource (UBI-126) ---

Deno.test("resource() called inside a pushBlueprintSource scope stamps an incomplete blueprint source", () => {
  const def = stack("platform", () => {
    intent({ summary: "s" });
    pushBlueprintSource("ci-platform");
    try {
      resource(Widget, "primary", { name: "x" });
    } finally {
      popBlueprintSource();
    }
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].sources, [{ kind: "blueprint", ref: "ci-platform" }]);
});

Deno.test("resource() called outside any pushBlueprintSource scope has no sources at all", () => {
  const def = stack("platform", () => {
    intent({ summary: "s" });
    resource(Widget, "primary", { name: "x" });
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].sources, undefined);
  assert(!("sources" in doc.resources[0]));
});

Deno.test("the innermost pushBlueprintSource scope wins when nested", () => {
  const def = stack("platform", () => {
    intent({ summary: "s" });
    pushBlueprintSource("outer");
    pushBlueprintSource("inner");
    try {
      resource(Widget, "primary", { name: "x" });
    } finally {
      popBlueprintSource();
      popBlueprintSource();
    }
  });
  const doc = def.evaluate();
  assertEquals(doc.resources[0].sources, [{ kind: "blueprint", ref: "inner" }]);
});

Deno.test("popBlueprintSource with no matching pushBlueprintSource throws", () => {
  assertThrows(() => popBlueprintSource(), Error, "no matching pushBlueprintSource");
});
