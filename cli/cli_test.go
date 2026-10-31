package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"wwfc/internal/privbind"
)

func TestPreflightReportsAllOccupiedPorts(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	specs := []privbind.Spec{
		{Name: "admin", Network: "tcp", Address: tcp.Addr().String()},
		{Name: "dns-udp", Network: "udp", Address: udp.LocalAddr().String()},
	}
	_, err = preflightPorts(context.Background(), specs)
	if err == nil {
		t.Fatal("occupied ports passed preflight")
	}
	for _, spec := range specs {
		if !strings.Contains(err.Error(), spec.Name) || !strings.Contains(err.Error(), spec.Address) {
			t.Fatalf("missing conflict for %+v: %v", spec, err)
		}
	}
	if !strings.Contains(err.Error(), "in use by") {
		t.Fatalf("conflict not described as occupied: %v", err)
	}
	_ = tcp.Close()
	_ = udp.Close()
	if needsAuthorization, err := preflightPorts(context.Background(), specs); err != nil || needsAuthorization {
		t.Fatalf("freed ports failed preflight: needsAuthorization=%v, err=%v", needsAuthorization, err)
	}
	// A successful probe must release both sockets for the real binder.
	sockets, err := privbind.BindDirect(specs)
	if err != nil {
		t.Fatalf("preflight left sockets bound: %v", err)
	}
	sockets.CloseAll()
}

func TestPreflightDetectsConfiguredOverlapAndReleasesProbes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	_ = ln.Close()
	_, err = preflightPorts(context.Background(), []privbind.Spec{
		{Name: "admin", Network: "tcp", Address: address},
		{Name: "nas-http", Network: "tcp", Address: address},
	})
	if err == nil || !strings.Contains(err.Error(), "nas-http") {
		t.Fatalf("configured overlap not detected: %v", err)
	}
	ln, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("failed preflight left sockets bound: %v", err)
	}
	_ = ln.Close()
}

func TestPreflightDoesNotMislabelUnavailableAddress(t *testing.T) {
	_, err := preflightPorts(context.Background(), []privbind.Spec{
		{Name: "admin", Network: "tcp", Address: "192.0.2.1:29990", Unsafe: true},
	})
	if err == nil || strings.Contains(err.Error(), "in use") || !strings.Contains(err.Error(), "192.0.2.1:29990") {
		t.Fatalf("unassigned address should report its bind error: %v", err)
	}
}

func TestIdentifyWildcardPortHolder(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof unavailable")
	}
	holder := identifyPortHolder(context.Background(), "tcp", "127.0.0.1:"+port)
	if !strings.Contains(holder, "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("wildcard listener not attributed to this process: %s", holder)
	}
}

func TestPortHolderMatchesLocalAddress(t *testing.T) {
	output := "p10\ncunrelated\nn10.0.0.2:53\np11\ncdns server\nn*:53\n"
	if got := portHolderFromOutput(output, "10.0.0.1:53"); got != "in use by dns server (pid 11)" {
		t.Fatalf("wildcard holder = %q", got)
	}
	if got := portHolderFromOutput("p10\ncunrelated\nn10.0.0.2:53\n", "10.0.0.1:53"); got != "" {
		t.Fatalf("blamed process on a different interface: %q", got)
	}
	if got := portHolderFromOutput("p12\ncudp\nn10.0.0.1:53->10.0.0.2:9000\n", "10.0.0.1:53"); got != "in use by udp (pid 12)" {
		t.Fatalf("connected UDP holder = %q", got)
	}
}

func TestPrivilegedPortSummary(t *testing.T) {
	specs := []privbind.Spec{
		{Name: "dns-udp", Network: "udp", Address: "127.0.0.1:53"},
		{Name: "dns-tcp", Network: "tcp", Address: "127.0.0.1:53"},
		{Name: "nas-http", Network: "tcp", Address: "127.0.0.1:80"},
		{Name: "admin", Network: "tcp", Address: "127.0.0.1:29990"},
	}
	if got := privilegedPortSummary(specs); got != "dns-udp 53, dns-tcp 53, nas-http 80" {
		t.Fatalf("privileged ports = %q", got)
	}
}
