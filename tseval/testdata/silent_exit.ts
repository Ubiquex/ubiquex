// Exits 0 before the runner can evaluate anything, so the program
// writes no intent document. Unlike a missing default export (which
// Deno itself refuses loudly and nonzero), this exits successfully.
import * as sdk from "@ubx/sdk";

console.error("a diagnosis written by a program that then exited 0");
Deno.exit(0);

export default sdk.stack("payments", () => {
  sdk.intent({ summary: "never reached" });
});
