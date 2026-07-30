import dataclasses
import os
from typing import Any

import ubx_sdk as sdk

Widget = sdk.ResourceBinding(wire_type="fake_widget", fields={"name": sdk.FieldSpec(wire_name="name")})


@dataclasses.dataclass
class WidgetConfig:
    name: Any = None


def describe():
    sdk.intent("env should be empty -- wasmtime forwards nothing unless --env names it")
    sdk.resource(Widget, "primary", WidgetConfig(name=f"home={os.environ.get('HOME')!r}"))


if __name__ == "__main__":
    sdk.run("payments", describe)
