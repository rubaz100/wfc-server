package dnssrv

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestLANAddressSelectionAndBindGuard(t *testing.T) {
	ip, err := SelectLANAddress([]net.IP{net.ParseIP("10.0.3.55"), net.ParseIP("10.0.20.246"), net.ParseIP("8.8.8.8")}, net.ParseIP("10.0.20.246"))
	if err != nil || ip.String() != "10.0.20.246" {
		t.Fatalf("selected %v, err %v", ip, err)
	}
	if _, err := SelectLANAddress([]net.IP{net.ParseIP("10.0.3.55"), net.ParseIP("10.0.20.246")}, nil); err == nil {
		t.Fatal("ambiguous address selection succeeded")
	}
	for _, address := range []string{"0.0.0.0:53", "8.8.8.8:53", "localhost:53", "10.0.20.246:0"} {
		if _, err := ValidateBindAddress(address, false); err == nil {
			t.Errorf("unsafe or invalid bind %q was accepted", address)
		}
	}
}

func listenDNSPair(t *testing.T) (net.PacketConn, net.Listener) {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", packetConn.LocalAddr().String())
		if err == nil {
			return packetConn, listener
		}
		_ = packetConn.Close()
	}
	t.Fatal("could not allocate a shared UDP/TCP port")
	return nil, nil
}

func TestLocalUDPAndTCPDNS(t *testing.T) {
	server := &Server{ip: net.ParseIP("10.0.20.246").To4(), domain: "mkwfc.lan"}
	packetConn, listener := listenDNSPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, packetConn, listener) }()
	for _, network := range []string{"udp", "tcp"} {
		address := listener.Addr().String()
		if network == "udp" {
			address = packetConn.LocalAddr().String()
		}
		for _, test := range []struct {
			name   string
			qtype  uint16
			answer bool
		}{{"naswii.nintendowifi.net.", dns.TypeA, true}, {"mariokartwii.ms19.gs.nintendowifi.net.", dns.TypeA, true}, {"naswii.nintendowifi.net.", dns.TypeAAAA, false}, {"nas.mkwfc.lan.", dns.TypeA, true}} {
			query := new(dns.Msg)
			query.SetQuestion(test.name, test.qtype)
			client := &dns.Client{Net: network, Timeout: time.Second}
			response, _, err := client.Exchange(query, address)
			if err != nil {
				t.Fatalf("%s %s: %v", network, test.name, err)
			}
			if test.answer && (len(response.Answer) != 1 || !response.Answer[0].(*dns.A).A.Equal(server.ip)) {
				t.Errorf("%s %s answer = %v", network, test.name, response.Answer)
			}
			if !test.answer && len(response.Answer) != 0 {
				t.Errorf("unsupported local query escaped: %v", response.Answer)
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DNS did not stop")
	}
}

func TestForwardingRetriesTruncatedUDP(t *testing.T) {
	upstreamUDP, upstreamTCP := listenDNSPair(t)
	upstream := &dns.Server{PacketConn: upstreamUDP, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Truncated = true
		_ = w.WriteMsg(m)
	})}
	tcpUpstream := &dns.Server{Listener: upstreamTCP, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.10")}}
		_ = w.WriteMsg(m)
	})}
	go upstream.ActivateAndServe()
	go tcpUpstream.ActivateAndServe()
	defer upstream.Shutdown()
	defer tcpUpstream.Shutdown()
	server, err := New(Config{Address: "127.0.0.1:53053", LANAddress: "10.0.20.246", LocalDomain: "mkwfc.lan", Upstream: upstreamTCP.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.exchange(context.Background(), (&dns.Msg{}).SetQuestion("example.test.", dns.TypeA))
	if err != nil || len(response.Answer) != 1 {
		t.Fatalf("forwarding response = %v, err %v", response, err)
	}
}

func TestRejectsNonLANClient(t *testing.T) {
	server := &Server{ip: net.ParseIP("10.0.20.246"), domain: "mkwfc.lan"}
	writer := &testWriter{remote: &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1234}}
	server.serveDNS(writer, (&dns.Msg{}).SetQuestion("naswii.nintendowifi.net.", dns.TypeA))
	if writer.message.Rcode != dns.RcodeRefused || len(writer.message.Answer) != 0 {
		t.Fatalf("non-LAN response = %+v", writer.message)
	}
}

type testWriter struct {
	remote  net.Addr
	message *dns.Msg
}

func (w *testWriter) LocalAddr() net.Addr             { return &net.UDPAddr{IP: net.ParseIP("127.0.0.1")} }
func (w *testWriter) RemoteAddr() net.Addr            { return w.remote }
func (w *testWriter) WriteMsg(message *dns.Msg) error { w.message = message; return nil }
func (w *testWriter) Write(data []byte) (int, error)  { return len(data), nil }
func (w *testWriter) Close() error                    { return nil }
func (w *testWriter) TsigStatus() error               { return nil }
func (w *testWriter) TsigTimersOnly(bool)             {}
func (w *testWriter) Hijack()                         {}
