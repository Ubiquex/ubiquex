"""ubx_sdk -- the describe-only Python runtime surface (UBI-36), mirroring
sdk/go/runtime and sdk/ts/runtime's semantics: stack/resource/secret/
cross/intent, Computed as a reference, never a value. A program using
this package never computes, never reaches a provider, never touches a
ledger -- it describes a desired end-state (an intent/v1 document) and
stops.

Unlike @ubx/sdk (runs inside a sandboxed interpreter alongside the
program) and unlike sdk/go/runtime (the compiled binary itself IS the
sandboxed unit), this package runs INSIDE a WASI sandbox (wasmtime + a
CPython-WASI build) alongside the program that imports it -- see
docs/sdk.md's "The Python evaluator: decided empirically" for the full
account of why that's hermetic. It deliberately never touches fs/net/
subprocess itself -- doing so would fail exactly the way a malicious
program's own attempt would (WASI does not distinguish "the runtime
library" from "the program," by design).
"""

from __future__ import annotations

import dataclasses
import json
import sys
from typing import Any, Callable, Optional

__all__ = [
    "Computed",
    "ComputedCoercionError",
    "FieldSpec",
    "FieldMap",
    "ResourceBinding",
    "IntentSource",
    "StackDefinition",
    "stack",
    "resource",
    "secret",
    "cross",
    "intent",
    "run",
    "is_computed",
]

# ---------------------------------------------------------------------
# Computed: a reference, never a value.
#
# TypeScript's Computed<T> is a Proxy that lets `primary.settings.enabled`
# keep drilling into a nested attribute and still produce a valid,
# traceable address. Python has no Proxy, but it has its own native
# attribute-customization hook -- __getattr__ -- an ordinary, idiomatic
# Python mechanism (used by countless dot-dict/ORM/mock libraries), not
# an imitation of JS's Proxy trap semantics. Coercion into a real value
# is blocked via Python's own actual implicit-coercion protocol methods
# (__str__/__bool__/__int__/__float__/__index__/__iter__/__len__) --
# the Pythonic equivalent of TS's COERCION_TRAPS, not a literal port of
# JS's own trap list (Python has no Symbol.toPrimitive; __repr__ is left
# alone deliberately, since unlike JS's toString it is never implicitly
# invoked by concatenation/arithmetic, only ever called explicitly for
# debugging -- blocking it would only hurt debuggability for no real
# safety gain).
# ---------------------------------------------------------------------


class ComputedCoercionError(Exception):
    """Raised when code tries to use a Computed value as a real Python
    value (str(), bool(), arithmetic, iteration, ...) instead of only
    ever passing it through into another resource()'s own config."""

    def __init__(self, address: str, attempted_operation: str):
        super().__init__(
            f'Computed value for "{address}" was used as a real Python value '
            f"({attempted_operation}) -- a Computed value is a reference to a "
            "not-yet-created resource's own attribute, never a real value; "
            "pass it through directly into another resource()'s own config "
            "field instead."
        )
        self.address = address


class Computed:
    """Computed is what resource()'s own return value is -- never a real
    attribute value (nothing has been created yet at describe time), only
    a reference to where a value will eventually be. Identified by real
    Python type (isinstance), never by structural duck-typing."""

    __slots__ = ("_address",)

    def __init__(self, address: str):
        object.__setattr__(self, "_address", address)

    @property
    def address(self) -> str:
        return self._address

    def __getattr__(self, name: str) -> "Computed":
        if name.startswith("_"):
            raise AttributeError(name)
        return Computed(f"{self._address}.{name}")

    def __repr__(self) -> str:
        return f"Computed({self._address!r})"

    def _reject(self, op: str):
        raise ComputedCoercionError(self._address, op)

    def __str__(self):
        self._reject("str()")

    def __bool__(self):
        self._reject("bool()")

    def __int__(self):
        self._reject("int()")

    def __float__(self):
        self._reject("float()")

    def __index__(self):
        self._reject("index/slice conversion")

    def __iter__(self):
        self._reject("iteration")

    def __len__(self):
        self._reject("len()")


def is_computed(v: Any) -> bool:
    """is_computed reports whether v is a real Computed produced by this
    module -- checked by isinstance, never by structural duck-typing, so
    a hand-rolled object that happens to look like one is never mistaken
    for a real reference."""
    return isinstance(v, Computed)


# ---------------------------------------------------------------------
# secret()/cross(): thin, typed front ends to existing, already-pinned
# wire markers (docs/schema.md) -- no new wire shape either introduces.
# ---------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class SecretMarker:
    backend: str
    path: str


def secret(backend: str, path: str) -> Any:
    """secret(backend, path) builds the exact {"$secret": {"backend",
    "path"}} marker docs/schema.md pins."""
    return SecretMarker(backend, path)


@dataclasses.dataclass(frozen=True)
class CrossMarker:
    ledger_dir: str
    to: str


def cross(ledger_dir: str, to: str) -> Any:
    """cross(ledger_dir, to) builds the exact {"$cross": {"ledger_dir",
    "to"}} marker docs/schema.md pins. to is a hand-typed address string
    in v1 (docs/sdk.md's own "Out of scope" -- a typed cross-stack handle
    is real, useful, deferred future work)."""
    return CrossMarker(ledger_dir, to)


# ---------------------------------------------------------------------
# The codegen'd binding shape -- what a `ubx sdk gen --lang py`-produced
# module actually exports for resource() to consume
# (sdk/codegen/templates/py). FieldMap is keyed by the Config dataclass's
# own field name -- which, unlike TS's camelCase or Go's PascalCase,
# usually IS the real provider wire name verbatim: wire names are already
# lowercase-with-underscores, i.e. already valid, idiomatic Python
# identifiers, in every real schema this project has generated against.
# ---------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class FieldSpec:
    """One settable config field's own wire-name mapping. kind == ""
    means a scalar (or list/set/map-of-scalar) leaf -- wire_name is used
    directly, fields is unused. A non-empty kind ("object", "list",
    "set", "map") names a nested object (or a list/set/map of nested
    objects) needing its own fields walked recursively."""

    wire_name: str
    kind: str = ""
    fields: Optional["FieldMap"] = None


FieldMap = dict  # dict[str, FieldSpec]


@dataclasses.dataclass(frozen=True)
class ResourceBinding:
    """What a codegen'd module exports per resource type -- wire_type for
    resources[].type, fields for Config-field-name -> wire-name mapping
    at evaluation time."""

    wire_type: str
    fields: "FieldMap"


# ---------------------------------------------------------------------
# intent/v1 document shape (docs/schema.md's own ubx:intent/v1) -- only
# the fields this runtime actually populates.
# ---------------------------------------------------------------------


@dataclasses.dataclass(frozen=True)
class IntentSource:
    kind: str
    ref: str
    content_hash: Optional[str] = None

    def _as_dict(self) -> dict:
        d = {"kind": self.kind, "ref": self.ref}
        if self.content_hash is not None:
            d["content_hash"] = self.content_hash
        return d


# ---------------------------------------------------------------------
# stack()/resource()/intent(): the describe-only surface itself.
# ---------------------------------------------------------------------


class _Collector:
    def __init__(self, stack_name: str):
        self.stack_name = stack_name
        self.resources: list = []
        self.seen_addresses: set = set()
        self.intent_info: Optional[dict] = None

    def set_intent(self, summary: str, sources: Optional[list]) -> None:
        if self.intent_info is not None:
            raise RuntimeError(
                "intent(): called more than once in a single stack() body -- "
                "there is exactly one summary per stack, same as a hand-written intent file."
            )
        if not summary or not summary.strip():
            raise RuntimeError("intent(): summary is required and cannot be empty.")
        rendered = []
        for s in sources or []:
            rendered.append(s._as_dict() if isinstance(s, IntentSource) else s)
        self.intent_info = {"summary": summary, "sources": rendered}

    def add_resource(self, binding: ResourceBinding, name: str, config: Any) -> Computed:
        if not name or not name.strip():
            raise RuntimeError(f"resource(): name is required (type {binding.wire_type}).")
        address = f"{self.stack_name}.{binding.wire_type}.{name}"
        if address in self.seen_addresses:
            raise RuntimeError(
                f'resource(): duplicate resource {binding.wire_type} "{name}" in stack "{self.stack_name}".'
            )
        self.seen_addresses.add(address)

        serialized = _serialize_config(binding.fields, config, address)
        self.resources.append({"type": binding.wire_type, "name": name, "op": "create", "config": serialized})

        return Computed(address)

    def finish(self) -> dict:
        if self.intent_info is None:
            raise RuntimeError(
                f'stack("{self.stack_name}"): intent() was never called -- a missing summary is a '
                'collection-time hard failure, matching resources[].op\'s own "always explicit, never inferred" discipline.'
            )
        return {
            "schema_version": 1,
            "kind": "ubx:intent/v1",
            "stack": self.stack_name,
            "intent": self.intent_info,
            "resources": self.resources,
        }


_current: Optional[_Collector] = None


class StackDefinition:
    """StackDefinition is stack()'s own return value -- evaluate() runs
    this stack's own describe function exactly once and returns the
    resulting intent/v1 document. Never called by stack() itself at
    construction time -- deferred to an explicit call so the evaluator
    harness fully controls when and how many times a program's own code
    actually runs."""

    def __init__(self, name: str, fn: Callable[[], None]):
        if not name or not name.strip():
            raise ValueError("stack(): name is required.")
        self.name = name
        self.fn = fn

    def evaluate(self) -> dict:
        global _current
        collector = _Collector(self.name)
        previous = _current
        _current = collector
        try:
            self.fn()
        finally:
            _current = previous
        return collector.finish()


def stack(name: str, fn: Callable[[], None]) -> StackDefinition:
    return StackDefinition(name, fn)


def _require_collector(caller: str) -> _Collector:
    if _current is None:
        raise RuntimeError(f"{caller}() called outside of an active stack() evaluation.")
    return _current


def resource(binding: ResourceBinding, name: str, config: Any) -> Computed:
    """resource(binding, name, config) declares one resource -- always
    op "create" in v1 (a hermetic, describe-only program has no way to
    read ledger state to know a resource already exists, so it has no
    way to express "modify" intent). Returns a Computed handle for wiring
    into sibling resources' own config."""
    return _require_collector("resource").add_resource(binding, name, config)


def intent(summary: str, sources: Optional[list] = None) -> None:
    """intent(summary, sources=None) populates the emitted document's own
    intent.summary/intent.sources. Required, called at most once per
    stack() body."""
    _require_collector("intent").set_intent(summary, sources)


def run(name: str, fn: Callable[[], None]) -> None:
    """run(name, fn) is the standard entry point a Python SDK program's
    own `if __name__ == "__main__":` block calls: evaluates
    stack(name, fn) and, on success, writes the resulting intent/v1
    document to stdout as JSON; on failure, writes the error to stderr
    and exits 1. Mirrors Go's sdk.Main(sdk.Stack(name, fn)) -- Python's
    own natural entry-point idiom (`if __name__ == "__main__"`) replaces
    Go's explicit func main() wrapper and TS's separate generated-
    runner-script indirection; there is no equivalent indirection needed
    here either, since the interpreter runs the program's own top-level
    code directly."""
    try:
        doc = stack(name, fn).evaluate()
    except Exception as e:  # noqa: BLE001 -- deliberately broad: no partial
        # intent/v1 is ever emitted, whatever the failure (docs/sdk.md's
        # own adversarial row: "no partial intent/v1 is ever emitted").
        print(str(e), file=sys.stderr)
        sys.exit(1)
    json.dump(doc, sys.stdout)


# ---------------------------------------------------------------------
# The recursive config serializer -- a Config dataclass's own field
# values, mapped back to the provider's real wire names via the
# binding's own FieldMap, recursively for nested objects/lists/sets/
# maps of objects. Mirrors sdk/go/runtime's serializeConfig/
# serializeOpaque split exactly, walked via dataclasses.fields()
# introspection instead of Go's reflect package -- Python is natively
# introspectable, so no separate reflection library is needed at all.
# ---------------------------------------------------------------------


def _serialize_generic_or_marker(value: Any, address: str) -> tuple[bool, Any]:
    """Returns (handled, result). handled is True for every value SHAPE
    common to both field-map-translated and opaque serialization (a
    Computed reference, a $secret/$cross marker, None, an
    unrepresentable primitive that must raise, or an ordinary scalar) --
    False only for a dataclass/dict/list/tuple, which the caller then
    walks its own, genuinely different way."""
    if value is None:
        return True, None
    if isinstance(value, Computed):
        return True, {"$ref": {"to": value.address}}
    if isinstance(value, SecretMarker):
        return True, {"$secret": {"backend": value.backend, "path": value.path}}
    if isinstance(value, CrossMarker):
        return True, {"$cross": {"ledger_dir": value.ledger_dir, "to": value.to}}
    if callable(value):
        raise TypeError(
            f'resource "{address}": a function cannot appear in a resource\'s own config -- intent/v1 has no representation for it.'
        )
    if isinstance(value, (str, bool, int, float)):
        return True, value
    if dataclasses.is_dataclass(value) or isinstance(value, (dict, list, tuple)):
        return False, None
    raise TypeError(f'resource "{address}": unrepresentable config value of type {type(value).__name__}.')


def _serialize_config(fields: "FieldMap", value: Any, address: str) -> Any:
    """serializeConfig walks a value whose own shape IS described by
    fields (the top-level resource config, or a nested field typed as a
    generated nested dataclass) -- every dataclass field is translated
    from its Python identifier to the provider's real wire name via
    fields, recursively. A field left at its default (None) is omitted
    from the output entirely, matching TS's own "only keys actually
    present in the object literal are walked."""
    handled, result = _serialize_generic_or_marker(value, address)
    if handled:
        return result

    if isinstance(value, (list, tuple)):
        # A field-map-shaped value reaching this point as a list means
        # the caller bypassed the generated Config type -- defensively
        # walk each element opaquely rather than guessing at a field map
        # for it that was never named.
        return [_serialize_opaque(el, address) for el in value]

    if not dataclasses.is_dataclass(value):
        raise TypeError(f'resource "{address}": expected a dataclass-shaped config value, got {type(value).__name__}.')

    out: dict = {}
    for f in dataclasses.fields(value):
        raw = getattr(value, f.name)
        if raw is None:
            continue  # not set -- omitted, matching TS's "key not present at all"
        spec = fields.get(f.name)
        if spec is None:
            raise TypeError(
                f'resource "{address}": unrecognized config field "{f.name}" -- not present in this resource type\'s own generated binding.'
            )
        out[spec.wire_name] = _serialize_field_value(spec, raw, address, f.name)
    return out


def _serialize_field_value(spec: FieldSpec, raw: Any, address: str, field_name: str) -> Any:
    """serializeFieldValue serializes one already-looked-up field's own
    raw value, dispatching on its FieldSpec."""
    if spec.kind == "":
        return _serialize_opaque(raw, address)
    if spec.kind == "object":
        return _serialize_config(spec.fields, raw, address)
    if spec.kind in ("list", "set"):
        handled, result = _serialize_generic_or_marker(raw, address)
        if handled:
            return result
        if not isinstance(raw, (list, tuple)):
            raise TypeError(f'resource "{address}": field "{field_name}" expects a list.')
        return [_serialize_config(spec.fields, el, address) for el in raw]
    if spec.kind == "map":
        handled, result = _serialize_generic_or_marker(raw, address)
        if handled:
            return result
        if not isinstance(raw, dict):
            raise TypeError(f'resource "{address}": field "{field_name}" expects a dict.')
        return {str(k): _serialize_config(spec.fields, v, address) for k, v in raw.items()}
    raise TypeError(f'resource "{address}": field "{field_name}" has unrecognized FieldSpec kind "{spec.kind}".')


def _serialize_opaque(value: Any, address: str) -> Any:
    """serializeOpaque walks a value with NO field-map-driven structure
    at all -- a scalar, or (recursively) anything nested inside a
    scalar/list/set/map-of-scalar field, e.g. a `tags: dict[str, str]`
    field's own arbitrary, user-chosen dict keys. Dict/list keys and
    shape pass through completely unchanged; only Computed/marker
    recognition and representability are checked."""
    handled, result = _serialize_generic_or_marker(value, address)
    if handled:
        return result
    if isinstance(value, (list, tuple)):
        return [_serialize_opaque(el, address) for el in value]
    if isinstance(value, dict):
        return {str(k): _serialize_opaque(v, address) for k, v in value.items()}
    if dataclasses.is_dataclass(value):
        out = {}
        for f in dataclasses.fields(value):
            v = getattr(value, f.name)
            if v is None:
                continue
            out[f.name] = _serialize_opaque(v, address)
        return out
    raise TypeError(f'resource "{address}": unrepresentable config value of type {type(value).__name__}.')
