package main

import (
	"fmt"
	"os"
)

func main() {
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox_fs: read /etc/hosts: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "sandbox_fs: read unexpectedly succeeded: %d bytes\n", len(data))
	os.Exit(1)
}
