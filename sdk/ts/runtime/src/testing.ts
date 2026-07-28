// A tiny, hand-written, self-contained set of assertion helpers used
// only by this package's own tests (index_test.ts) -- deliberately not
// `jsr:@std/assert` or any other remote/npm dependency: that would need
// network access on first run even just to execute `deno test`, which
// this project's own "verify before implementing" probe (docs/sdk.md's
// own evaluator section) confirmed empirically this session. A handful
// of assertions is little enough code that a real dependency isn't
// worth the network dependency it would add to every future `deno test`
// invocation, including CI.

export function assert(condition: unknown, message?: string): asserts condition {
  if (!condition) {
    throw new Error(message ?? "assertion failed");
  }
}

export function assertEquals<T>(actual: T, expected: T, message?: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    throw new Error(message ?? `assertEquals failed:\n  actual:   ${a}\n  expected: ${e}`);
  }
}

export function assertThrows(
  fn: () => unknown,
  errorClassOrMessageIncludes?: (new (...args: never[]) => Error) | string,
  messageIncludes?: string,
): void {
  let thrown: unknown;
  try {
    fn();
  } catch (e) {
    thrown = e;
  }
  if (thrown === undefined) {
    throw new Error("assertThrows: function did not throw");
  }
  if (typeof errorClassOrMessageIncludes === "function") {
    if (!(thrown instanceof errorClassOrMessageIncludes)) {
      throw new Error(`assertThrows: thrown value is not an instance of ${errorClassOrMessageIncludes.name}: ${thrown}`);
    }
    if (messageIncludes && !(thrown as Error).message.includes(messageIncludes)) {
      throw new Error(`assertThrows: message %"${(thrown as Error).message}" does not include "${messageIncludes}"`);
    }
    return;
  }
  if (typeof errorClassOrMessageIncludes === "string") {
    if (!(thrown as Error).message?.includes(errorClassOrMessageIncludes)) {
      throw new Error(`assertThrows: message "${(thrown as Error).message}" does not include "${errorClassOrMessageIncludes}"`);
    }
  }
}
