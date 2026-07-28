import { installNondeterminismGuards, NondeterministicAPIError } from "./guards.ts";
import { assert, assertEquals, assertThrows } from "../runtime/src/testing.ts";

Deno.test("Date.now() throws NondeterministicAPIError after guards are installed", () => {
  installNondeterminismGuards();
  assertThrows(() => Date.now(), NondeterministicAPIError);
});

Deno.test("new Date() (zero-arg) throws NondeterministicAPIError", () => {
  installNondeterminismGuards();
  assertThrows(() => new Date(), NondeterministicAPIError);
});

Deno.test("new Date(<explicit args>) is NOT blocked -- deterministic construction stays legal", () => {
  installNondeterminismGuards();
  const d = new Date(2020, 0, 1);
  assertEquals(d.getFullYear(), 2020);
  const d2 = new Date("2020-01-01T00:00:00.000Z");
  assertEquals(d2.getUTCFullYear(), 2020);
  const d3 = new Date(1577836800000);
  assertEquals(d3.getUTCFullYear(), 2020);
});

Deno.test("Math.random() throws NondeterministicAPIError after guards are installed", () => {
  installNondeterminismGuards();
  assertThrows(() => Math.random(), NondeterministicAPIError);
});

Deno.test("installNondeterminismGuards is idempotent -- calling it twice doesn't double-wrap or throw", () => {
  installNondeterminismGuards();
  installNondeterminismGuards();
  assertThrows(() => Date.now(), NondeterministicAPIError);
  assert(true);
});
