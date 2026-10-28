//go:build darwin

package privbind

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRequest(specs ...Spec) Request {
	return Request{Version: 1, ParentUID: os.Getuid(), Token: strings.Repeat("a", 43), Sockets: specs}
}

func TestDescriptorPassingAndHighPortDirectBind(t *testing.T) {
	runtimeDir := testRuntimeDir(t)
	if err := os.Chmod(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeDir(runtimeDir, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	request := testRequest(Spec{Name: "tcp-test", Network: "tcp", Address: "127.0.0.1:0"}, Spec{Name: "udp-test", Network: "udp", Address: "127.0.0.1:0"})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(runtimeDir, "bind.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// runHelper holds the descriptors open until the control connection is
	// closed, so do all I/O first, then CloseAll (which signals the helper) and
	// finally wait for it to exit. Closing before using the sockets would drain
	// their accept queues; see FORK-NOTES.md.
	done := make(chan error, 1)
	go func() { done <- runHelper(request, listener.Addr().String()) }()
	sockets, err := receiveSockets(listener, request)
	if err != nil {
		t.Fatal(err)
	}
	defer sockets.CloseAll() // backstop: release the helper even if an assertion fails
	tcpListener, err := sockets.Listener("tcp-test")
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", tcpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := tcpListener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 5)
	if _, err := accepted.Read(buffer); err != nil || !bytes.Equal(buffer, []byte("hello")) {
		t.Fatalf("received TCP descriptor failed: %q, %v", buffer, err)
	}
	_ = client.Close()
	_ = accepted.Close()
	packet, err := sockets.PacketConn("udp-test")
	if err != nil {
		t.Fatal(err)
	}
	udpClient, err := net.Dial("udp", packet.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer udpClient.Close()
	if _, err := udpClient.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = packet.SetReadDeadline(time.Now().Add(time.Second))
	if n, _, err := packet.ReadFrom(buffer); err != nil || n != 5 {
		t.Fatalf("received UDP descriptor failed: %d, %v", n, err)
	}
	// Release the descriptors and wait for the helper to exit cleanly.
	sockets.CloseAll()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestCloseAllReleasesHelperSocketPromptly guards TODO.md's clean-shutdown
// requirement at the privileged-bind layer: closing the Sockets must close the
// control connection so the helper releases its listening socket right away, and
// must not block on reaping. A fresh rebind of the same port must then succeed
// immediately. This uses runHelper directly (no osascript), so it stands in for
// the privilegedBind teardown that CI cannot exercise through the GUI prompt.
func TestCloseAllReleasesHelperSocketPromptly(t *testing.T) {
	request := testRequest(Spec{Name: "tcp-test", Network: "tcp", Address: "127.0.0.1:0"})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(testRuntimeDir(t), "bind.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- runHelper(request, listener.Addr().String()) }()
	sockets, err := receiveSockets(listener, request)
	if err != nil {
		t.Fatal(err)
	}
	defer sockets.CloseAll() // backstop if an assertion fails before the explicit close
	tcpListener, err := sockets.Listener("tcp-test")
	if err != nil {
		t.Fatal(err)
	}
	address := tcpListener.Addr().String()

	start := time.Now()
	sockets.CloseAll()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("CloseAll blocked for %s, want prompt", elapsed)
	}
	// The helper must have exited (and released its descriptor) by now.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper did not exit after the control connection closed")
	}
	rebind, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("port %s still held after CloseAll: %v", address, err)
	}
	_ = rebind.Close()
}

func TestHelperPartialFailureClosesSockets(t *testing.T) {
	udpProbe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpAddress := udpProbe.LocalAddr().String()
	_ = udpProbe.Close()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	request := testRequest(Spec{Name: "udp-test", Network: "udp", Address: udpAddress}, Spec{Name: "tcp-test", Network: "tcp", Address: occupied.Addr().String()})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(testRuntimeDir(t), "bind.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- runHelper(request, listener.Addr().String()) }()
	if _, err := receiveSockets(listener, request); err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("partial bind error = %v", err)
	}
	if err := <-done; err == nil {
		t.Fatal("partial helper bind succeeded")
	}
	probe, err := net.ListenPacket("udp", udpAddress)
	if err != nil {
		t.Fatalf("helper left its first socket bound: %v", err)
	}
	_ = probe.Close()
}

func TestBindValidationAndCancellation(t *testing.T) {
	for _, spec := range []Spec{{Name: "bad", Network: "tcp", Address: "0.0.0.0:80"}, {Name: "bad", Network: "tcp", Address: "8.8.8.8:53"}, {Name: "bad", Network: "sctp", Address: "127.0.0.1:80"}} {
		if err := ValidateSpec(spec); err == nil {
			t.Fatalf("unsafe socket was accepted: %+v", spec)
		}
	}
	badDirectory := testRuntimeDir(t)
	if err := os.Chmod(badDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeDir(badDirectory, os.Getuid()); err == nil {
		t.Fatal("insecure runtime directory was accepted")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(testRuntimeDir(t), "bind.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(Spec{Name: "tcp-test", Network: "tcp", Address: "127.0.0.1:0"})
	_ = listener.Close()
	if _, err := receiveSockets(listener, request); err == nil {
		t.Fatal("closed descriptor socket was accepted")
	}
}

func testRuntimeDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "mkwfc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
