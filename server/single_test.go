package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wwfc/common"
	"wwfc/internal/bootstrap"
	"wwfc/internal/privbind"
)

func runtimeFixture(t *testing.T, includeAdmin bool) (bootstrap.Result, common.Config, *privbind.Sockets) {
	t.Helper()
	// bootstrap.Ensure requires a data directory with mode 0700 and refuses to
	// chmod one it did not create. t.TempDir()'s sequential leaf is created
	// 0777&^umask (0755 here), so hand Ensure a fresh child it creates at 0700.
	result, err := bootstrap.Ensure(filepath.Join(t.TempDir(), "data"), "10.0.20.246")
	if err != nil {
		t.Fatal(err)
	}
	config := result.Config
	*config.GameSpyAddress = "127.0.0.1"
	*config.NASAddress = "127.0.0.1"
	*config.NASAddressHTTPS = "127.0.0.1"
	// DNS serves on the pre-bound socket below; config.DNSAddress is only used
	// for the upstream!=self check, and dnssrv.New requires a real (non-zero)
	// port there, so a production-shaped value is used rather than :0. A fixed
	// upstream keeps the test independent of the host's /etc/resolv.conf.
	config.DNSAddress = "127.0.0.1:53"
	config.DNSUpstream = "9.9.9.9:53"
	config.AdminAddress = "127.0.0.1:0"
	specs := []privbind.Spec{
		{Name: "dns-udp", Network: "udp", Address: "127.0.0.1:0"},
		{Name: "dns-tcp", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "nas-http", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "nas-https", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "serverbrowser", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "gpcm", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "gpsp", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "gamestats", Network: "tcp", Address: "127.0.0.1:0"},
		{Name: "qr2", Network: "udp", Address: "127.0.0.1:0"},
		{Name: "natneg", Network: "udp", Address: "127.0.0.1:0"},
	}
	if includeAdmin {
		specs = append(specs, privbind.Spec{Name: "admin", Network: "tcp", Address: "127.0.0.1:0"})
	}
	sockets, err := privbind.BindDirect(specs)
	if err != nil {
		t.Fatal(err)
	}
	return result, config, sockets
}

func TestSingleProcessRealConnectionsAndCancellation(t *testing.T) {
	result, config, sockets := runtimeFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunSingle(ctx, config, SingleOptions{Catalog: result.Catalog, Sockets: sockets}) }()
	gpcmListener, _ := sockets.Listener("gpcm")
	connection := dialUntil(t, gpcmListener.Addr().String())
	challenge := readFinal(t, connection)
	// GameSpy messages are built from a Go map, so field order is random; assert
	// on the command and the presence of the challenge field, not a fixed order.
	if !bytes.HasPrefix(challenge, []byte(`\lc\1\`)) || !bytes.Contains(challenge, []byte(`\challenge\`)) {
		t.Fatalf("unexpected login challenge %q", challenge)
	}
	_, _ = connection.Write([]byte(`\k`))
	_, _ = connection.Write([]byte(`a\\final\`))
	if response := readFinal(t, connection); !bytes.Equal(response, []byte(`\ka\\final\`)) {
		t.Fatalf("fragmented keepalive response = %q", response)
	}
	_ = connection.Close()
	gpspListener, _ := sockets.Listener("gpsp")
	gpspConnection := dialUntil(t, gpspListener.Addr().String())
	_, _ = gpspConnection.Write([]byte(`\ka\\fin`))
	_, _ = gpspConnection.Write([]byte(`al\`))
	if response := readFinal(t, gpspConnection); !bytes.Equal(response, []byte(`\ka\\final\`)) {
		t.Fatalf("GPSP fragmented keepalive response = %q", response)
	}
	_ = gpspConnection.Close()

	httpListener, _ := sockets.Listener("nas-http")
	request, _ := http.NewRequest(http.MethodGet, "http://"+httpListener.Addr().String()+"/w1", nil)
	request.Host = "naswii.nintendowifi.net"
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	expected, _ := result.Catalog.Stage1(1)
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, expected) {
		t.Fatalf("NAS stage 1 returned %d and %d bytes", response.StatusCode, len(body))
	}
	httpsListener, _ := sockets.Listener("nas-https")
	httpsClient := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}}
	request, _ = http.NewRequest(http.MethodGet, "https://"+httpsListener.Addr().String()+"/nastest.jsp", nil)
	request.Host = "naswii.nintendowifi.net"
	response, err = httpsClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS returned %d", response.StatusCode)
	}
	dnsUDP, _ := sockets.PacketConn("dns-udp")
	if dnsUDP.LocalAddr() == nil {
		t.Fatal("embedded DNS is not bound")
	}
	if _, err := os.Stat(config.DatabasePath); err != nil {
		t.Fatalf("SQLite database was not created: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("single-process server did not stop")
	}
	listener, err := net.Listen("tcp", gpcmListener.Addr().String())
	if err != nil {
		t.Fatalf("GPCM socket remained bound after shutdown: %v", err)
	}
	_ = listener.Close()
}

func TestStartupFailureRollsBackSockets(t *testing.T) {
	result, config, sockets := runtimeFixture(t, false)
	httpListener, _ := sockets.Listener("nas-http")
	address := httpListener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := RunSingle(ctx, config, SingleOptions{Catalog: result.Catalog, Sockets: sockets}); err == nil {
		t.Fatal("startup without an admin listener succeeded")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("startup failure left NAS bound: %v", err)
	}
	_ = listener.Close()
}

func dialUntil(t *testing.T, address string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			return connection
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("could not connect to %s", address))
	return nil
}

func readFinal(t *testing.T, connection net.Conn) []byte {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var result []byte
	for !bytes.HasSuffix(result, []byte(`\final\`)) {
		buffer := make([]byte, 256)
		n, err := connection.Read(buffer)
		result = append(result, buffer[:n]...)
		if err != nil || len(result) > 4096 {
			t.Fatalf("read GameSpy response %q: %v", result, err)
		}
	}
	return result
}
