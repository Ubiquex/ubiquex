package main

import (
	"fmt"

	"rsc.io/quote"
)

// This program's own go.mod requires a real external package, no local
// replace, and no go.sum entry -- the Go analog of TS's dynamic remote-
// import escape row: a build reaching for something beyond what a local
// replace/module cache already provides must fail loudly (GOPROXY=off),
// never silently fetch untrusted code from the network mid-evaluation.
func main() {
	fmt.Println(quote.Hello())
}
