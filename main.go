package main

import "wwfc/server"

// The process logic lives in package server so it can also be embedded in the
// single-binary mkwfc wrapper. This binary is the standalone wwfc entrypoint.
func main() {
	server.Run()
}
