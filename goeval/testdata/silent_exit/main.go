package main

// Builds a stack definition and never passes it to sdk.Main, so the
// program exits 0 having written no intent document. The stderr line
// proves stderr survives a zero exit.

import (
	"fmt"
	"os"

	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	fmt.Fprintln(os.Stderr, "a diagnosis written by a program that then exited 0")
	_ = sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "never evaluated"})
	})
}
