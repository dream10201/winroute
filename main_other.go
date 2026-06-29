//go:build !windows

package main

import (
	"fmt"
	"os"
)

// winroute only does anything on Windows. This stub exists so the package
// still compiles when cross-tooling runs on Linux/macOS.

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

func main() {
	fmt.Fprintln(os.Stderr, "winroute only runs on Windows")
	os.Exit(1)
}
