"""Hermetic unit tests for ubx_sdk (stdlib unittest -- zero extra
dependencies, consistent with the sandboxed evaluator's own "no pip"
posture; a contributor can run these with a bare `python3 -m unittest`).
Mirrors sdk/go/runtime/runtime_test.go's own coverage, one for one."""

import dataclasses
import unittest
from typing import Any, Optional

import ubx_sdk as sdk


@dataclasses.dataclass
class WidgetConfig:
    name: Any = None
    count: Any = None
    tags: Any = None
    owner: Any = None
    timeouts: Any = None


@dataclasses.dataclass
class WidgetTimeouts:
    create: Any = None


WIDGET = sdk.ResourceBinding(
    wire_type="fake_widget",
    fields={
        "name": sdk.FieldSpec(wire_name="name"),
        "count": sdk.FieldSpec(wire_name="count"),
        "tags": sdk.FieldSpec(wire_name="tags"),
        "owner": sdk.FieldSpec(wire_name="owner_ref"),
        "timeouts": sdk.FieldSpec(
            wire_name="timeouts",
            kind="object",
            fields={"create": sdk.FieldSpec(wire_name="create")},
        ),
    },
)


class RuntimeTest(unittest.TestCase):
    def test_basic_resource_round_trip(self):
        def describe():
            sdk.intent("a summary")
            sdk.resource(WIDGET, "widget-a", WidgetConfig(name="hello", count=3))

        doc = sdk.stack("demo", describe).evaluate()
        self.assertEqual(doc["schema_version"], 1)
        self.assertEqual(doc["kind"], "ubx:intent/v1")
        self.assertEqual(len(doc["resources"]), 1)
        res = doc["resources"][0]
        self.assertEqual((res["type"], res["name"], res["op"]), ("fake_widget", "widget-a", "create"))
        self.assertEqual(res["config"], {"name": "hello", "count": 3})
        self.assertNotIn("owner_ref", res["config"])

    def test_computed_reference_serializes_to_ref(self):
        def describe():
            sdk.intent("s")
            first = sdk.resource(WIDGET, "a", WidgetConfig(name="a"))
            sdk.resource(WIDGET, "b", WidgetConfig(owner=first.id))

        doc = sdk.stack("demo", describe).evaluate()
        second = doc["resources"][1]
        self.assertEqual(second["config"]["owner_ref"], {"$ref": {"to": "demo.fake_widget.a.id"}})

    def test_secret_and_cross_markers(self):
        def describe():
            sdk.intent("s")
            sdk.resource(
                WIDGET,
                "a",
                WidgetConfig(
                    name=sdk.secret("vault", "path/to/secret"),
                    owner=sdk.cross("../networking", "networking.fake_widget.shared"),
                ),
            )

        doc = sdk.stack("demo", describe).evaluate()
        cfg = doc["resources"][0]["config"]
        self.assertEqual(cfg["name"], {"$secret": {"backend": "vault", "path": "path/to/secret"}})
        self.assertEqual(
            cfg["owner_ref"],
            {"$cross": {"ledger_dir": "../networking", "to": "networking.fake_widget.shared"}},
        )

    def test_nested_object_field(self):
        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "a", WidgetConfig(name="a", timeouts=WidgetTimeouts(create="20m")))

        doc = sdk.stack("demo", describe).evaluate()
        self.assertEqual(doc["resources"][0]["config"]["timeouts"], {"create": "20m"})

    def test_opaque_dict_field(self):
        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "a", WidgetConfig(name="a", tags={"env": "prod", "owner": "roozbeh"}))

        doc = sdk.stack("demo", describe).evaluate()
        self.assertEqual(doc["resources"][0]["config"]["tags"], {"env": "prod", "owner": "roozbeh"})

    def test_missing_intent_is_hard_failure(self):
        def describe():
            sdk.resource(WIDGET, "a", WidgetConfig(name="a"))

        with self.assertRaisesRegex(RuntimeError, "intent\\(\\) was never called"):
            sdk.stack("demo", describe).evaluate()

    def test_duplicate_resource_address_is_hard_failure(self):
        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "a", WidgetConfig(name="1"))
            sdk.resource(WIDGET, "a", WidgetConfig(name="2"))

        with self.assertRaisesRegex(RuntimeError, "duplicate resource"):
            sdk.stack("demo", describe).evaluate()

    def test_unrecognized_config_field_is_hard_failure(self):
        @dataclasses.dataclass
        class BadConfig:
            not_a_real_field: Any = None

        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "a", BadConfig(not_a_real_field="x"))

        with self.assertRaisesRegex(TypeError, "unrecognized config field"):
            sdk.stack("demo", describe).evaluate()

    def test_function_in_config_is_hard_failure(self):
        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "a", WidgetConfig(name=lambda: None))

        with self.assertRaisesRegex(TypeError, "cannot appear in a resource's own config"):
            sdk.stack("demo", describe).evaluate()

    def test_resource_outside_stack_is_hard_failure(self):
        with self.assertRaisesRegex(RuntimeError, "called outside of an active stack"):
            sdk.resource(WIDGET, "a", WidgetConfig(name="a"))

    def test_computed_coercion_raises(self):
        def describe():
            sdk.intent("s")
            first = sdk.resource(WIDGET, "a", WidgetConfig(name="a"))
            str(first.id)  # coercion, not a pass-through into config -- must raise

        with self.assertRaises(sdk.ComputedCoercionError):
            sdk.stack("demo", describe).evaluate()

    def test_computed_repr_is_not_blocked(self):
        c = sdk.Computed("demo.fake_widget.a.id")
        self.assertIn("demo.fake_widget.a.id", repr(c))

    def test_override_round_trip(self):
        def describe():
            sdk.intent("s")
            sdk.override("payments.aws_sqs_queue.pipeline-events", {"some_hardcoded_field": "new_value"})

        doc = sdk.stack("payments", describe).evaluate()
        self.assertEqual(len(doc["overrides"]), 1)
        ov = doc["overrides"][0]
        self.assertEqual(ov["address"], "payments.aws_sqs_queue.pipeline-events")
        self.assertEqual(ov["config"]["some_hardcoded_field"], "new_value")

    def test_override_computed_reference(self):
        def describe():
            sdk.intent("s")
            primary = sdk.resource(WIDGET, "primary", WidgetConfig(name="primary-widget"))
            sdk.override("payments.fake_widget.mirror", {"owner_ref": primary})

        doc = sdk.stack("payments", describe).evaluate()
        config = doc["overrides"][0]["config"]
        self.assertEqual(config["owner_ref"]["$ref"]["to"], "payments.fake_widget.primary")

    def test_override_no_overrides_omitted_from_wire(self):
        def describe():
            sdk.intent("s")

        doc = sdk.stack("payments", describe).evaluate()
        self.assertNotIn("overrides", doc)

    def test_override_empty_address_is_hard_failure(self):
        def describe():
            sdk.intent("s")
            sdk.override("", {"x": "y"})

        with self.assertRaisesRegex(RuntimeError, "address is required"):
            sdk.stack("payments", describe).evaluate()

    def test_override_empty_config_is_hard_failure(self):
        def describe():
            sdk.intent("s")
            sdk.override("payments.fake_widget.a", {})

        with self.assertRaisesRegex(RuntimeError, "config is required"):
            sdk.stack("payments", describe).evaluate()

    def test_override_outside_stack_is_hard_failure(self):
        with self.assertRaisesRegex(RuntimeError, "called outside of an active stack"):
            sdk.override("payments.fake_widget.a", {"x": "y"})

    # --- push_blueprint_source()/pop_blueprint_source() (UBI-126) ---

    def test_resource_inside_blueprint_source_scope_stamps_incomplete_source(self):
        def describe():
            sdk.intent("s")
            sdk.push_blueprint_source("ci-platform")
            try:
                sdk.resource(WIDGET, "primary", WidgetConfig(name="x"))
            finally:
                sdk.pop_blueprint_source()

        doc = sdk.stack("platform", describe).evaluate()
        self.assertEqual(doc["resources"][0]["sources"], [{"kind": "blueprint", "ref": "ci-platform"}])

    def test_resource_outside_any_blueprint_source_scope_has_no_sources(self):
        def describe():
            sdk.intent("s")
            sdk.resource(WIDGET, "primary", WidgetConfig(name="x"))

        doc = sdk.stack("platform", describe).evaluate()
        self.assertNotIn("sources", doc["resources"][0])

    def test_innermost_blueprint_source_scope_wins_when_nested(self):
        def describe():
            sdk.intent("s")
            sdk.push_blueprint_source("outer")
            sdk.push_blueprint_source("inner")
            try:
                sdk.resource(WIDGET, "primary", WidgetConfig(name="x"))
            finally:
                sdk.pop_blueprint_source()
                sdk.pop_blueprint_source()

        doc = sdk.stack("platform", describe).evaluate()
        self.assertEqual(doc["resources"][0]["sources"], [{"kind": "blueprint", "ref": "inner"}])

    def test_pop_blueprint_source_with_no_matching_push_raises(self):
        with self.assertRaisesRegex(RuntimeError, "no matching push_blueprint_source"):
            sdk.pop_blueprint_source()


if __name__ == "__main__":
    unittest.main()
