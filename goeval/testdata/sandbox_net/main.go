package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	conn, err := net.DialTimeout("tcp", "8.8.8.8:53", 3*time.Second)
	if err == nil {
		conn.Close()
		fmt.Fprintln(os.Stderr, "sandbox_net: dial unexpectedly succeeded")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "sandbox_net: dial 8.8.8.8:53: %v\n", err)
	os.Exit(1)
}
