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


if __name__ == "__main__":
    unittest.main()
