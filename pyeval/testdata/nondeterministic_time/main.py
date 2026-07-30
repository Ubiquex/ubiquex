import dataclasses
import time
from typing import Any

import ubx_sdk as sdk

Widget = sdk.ResourceBinding(wire_type="fake_widget", fields={"name": sdk.FieldSpec(wire_name="name")})


@dataclasses.dataclass
class WidgetConfig:
    name: Any = None


def describe():
    sdk.intent("leaks time.time() into config -- must be caught by DoubleRun")
    sdk.resource(Widget, "primary", WidgetConfig(name=f"widget-{time.time_ns()}"))


if __name__ == "__main__":
    sdk.run("payments", describe)
