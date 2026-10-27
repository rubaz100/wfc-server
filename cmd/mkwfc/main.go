// Command mkwfc is the single-binary Nintendo WFC server for Mario Kart Wii.
//
// This is a thin entry point; the implementation lives in wwfc/cli so the root
// mkwfc module can build the same binary from an identical shim. The binary
// also re-executes itself as a privileged __bind_helper (handled in cli.Main).
package main

import (
	"os"
	"wwfc/cli"
)

func main() { os.Exit(cli.Main()) }
