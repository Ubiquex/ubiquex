# Mirrors cli/testdata/sdk_resolve/create_widget.ts and
# sdk_resolve_go/create_widget.go exactly, in Python.
import ubx_sdk as sdk
from bindings import FakeWidget, FakeWidgetConfig


def describe():
    sdk.intent("add widget1, authored in Python")
    sdk.resource(FakeWidget, "widget1", FakeWidgetConfig(name="widget1", tags={"env": "prod"}))


if __name__ == "__main__":
    sdk.run("payments", describe)
