import dataclasses
from typing import Any

import ubx_sdk as sdk

FakeWidget = sdk.ResourceBinding(
    wire_type="fake_widget",
    fields={"name": sdk.FieldSpec(wire_name="name")},
)


@dataclasses.dataclass
class FakeWidgetConfig:
    name: Any = None


def describe():
    sdk.intent("a real, well-behaved program for pyeval's own happy-path test")
    sdk.resource(FakeWidget, "primary", FakeWidgetConfig(name="primary-widget"))


if __name__ == "__main__":
    sdk.run("payments", describe)
