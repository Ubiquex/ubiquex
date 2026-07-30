# A tiny, hand-written fake binding, mirroring fake_widget's real schema
# (provider/internal/fakeprovider) -- standing in for a real `ubx sdk
# gen`-generated one, since this fixture is about `ubx resolve
# --from-code`'s own CLI wiring, not codegen (already covered by
# sdk/codegen/templates/py's own tests). Mirrors
# cli/testdata/sdk_resolve/bindings.ts and sdk_resolve_go/bindings.go
# exactly, in Python.
import dataclasses
from typing import Any

import ubx_sdk as sdk

FakeWidget = sdk.ResourceBinding(
    wire_type="fake_widget",
    fields={
        "name": sdk.FieldSpec(wire_name="name"),
        "tags": sdk.FieldSpec(wire_name="tags"),
    },
)


@dataclasses.dataclass
class FakeWidgetConfig:
    name: Any = None
    tags: Any = None
