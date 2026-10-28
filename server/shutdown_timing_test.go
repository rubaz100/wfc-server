package server

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestShutdownIsPromptAndFreesPorts guards the clean-shutdown behavior recorded
// in TODO.md: cancelling RunSingle (the same path SIGINT/Ctrl-C and SIGTERM take
// through cli.commandContext) must return quickly and leave none of the bound
// TCP ports held, so an immediate restart is not blocked by a lingering bind.
func TestShutdownIsPromptAndFreesPorts(t *testing.T) {
	result, config, sockets := runtimeFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunSingle(ctx, config, SingleOptions{Catalog: result.Catalog, Sockets: sockets}) }()

	// Collect the addresses of every TCP listener before shutdown.
	tcpNames := []string{"nas-http", "nas-https", "serverbrowser", "gpcm", "gpsp", "gamestats", "admin"}
	addresses := map[string]string{}
	for _, name := range tcpNames {
		listener, err := sockets.Listener(name)
		if err != nil {
			t.Fatalf("listener %q: %v", name, err)
		}
		addresses[name] = listener.Addr().String()
	}
	// Make sure the server is actually accepting before we stop it.
	_ = dialUntil(t, addresses["gpcm"]).Close()

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunSingle returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("single-process server did not stop")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown took %s, want prompt (<3s)", elapsed)
	}

	for name, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("%s port %s remained bound after shutdown: %v", name, address, err)
		}
		_ = listener.Close()
	}
}
