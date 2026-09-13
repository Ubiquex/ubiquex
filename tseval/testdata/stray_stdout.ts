// A stray console.log. It goes to stdout, which is where the intent
// document goes, so the document arrives with a line of prose in front
// of it and no longer parses. The program exits 0.
import * as sdk from "@ubx/sdk";

console.log("debugging this, remove later");
console.error("and a line on stderr too");

export default sdk.stack("payments", () => {
  sdk.intent({ summary: "a program whose own logging corrupted its output" });
});
