package dnssrv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type Config struct {
	Address     string
	LANAddress  string
	LocalDomain string
	Upstream    string
	AllowUnsafe bool
}

type Server struct {
	ip        net.IP
	domain    string
	upstream  string
	timeout   time.Duration
	udpServer *dns.Server
	tcpServer *dns.Server
}

func New(config Config) (*Server, error) {
	address, err := ValidateBindAddress(config.Address, config.AllowUnsafe)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(config.LANAddress)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return nil, fmt.Errorf("DNS destination %q is not a private IPv4 address", config.LANAddress)
	}
	domain := strings.ToLower(strings.TrimSuffix(config.LocalDomain, "."))
	if domain == "" || len(domain) > 63 || !validDNSName(domain) {
		return nil, fmt.Errorf("invalid local DNS domain %q", config.LocalDomain)
	}
	upstream, err := upstreamAddress(config.Upstream, address)
	if err != nil {
		return nil, err
	}
	return &Server{ip: ip.To4(), domain: domain, upstream: upstream, timeout: 3 * time.Second}, nil
}

func (s *Server) Run(ctx context.Context, packetConn net.PacketConn, listener net.Listener) error {
	if packetConn == nil || listener == nil {
		return fmt.Errorf("DNS requires both UDP and TCP sockets")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.udpServer = &dns.Server{PacketConn: packetConn, Handler: dns.HandlerFunc(s.serveDNS), ReadTimeout: s.timeout}
	s.tcpServer = &dns.Server{Listener: listener, Handler: dns.HandlerFunc(s.serveDNS), ReadTimeout: s.timeout, WriteTimeout: s.timeout}
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- s.udpServer.ActivateAndServe() }()
	go func() { errorsChannel <- s.tcpServer.ActivateAndServe() }()
	var runError error
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			runError = fmt.Errorf("DNS server: %w", err)
		}
	}
	cancel()
	shutdownContext, release := context.WithTimeout(context.Background(), 3*time.Second)
	defer release()
	if s.udpServer != nil {
		_ = s.udpServer.ShutdownContext(shutdownContext)
	}
	if s.tcpServer != nil {
		_ = s.tcpServer.ShutdownContext(shutdownContext)
	}
	return runError
}

func (s *Server) serveDNS(writer dns.ResponseWriter, request *dns.Msg) {
	response := new(dns.Msg)
	response.SetReply(request)
	response.RecursionAvailable = s.upstream != ""
	if !lanClient(writer.RemoteAddr()) {
		response.Rcode = dns.RcodeRefused
		_ = writer.WriteMsg(response)
		return
	}
	if len(request.Question) != 1 || request.Opcode != dns.OpcodeQuery {
		response.Rcode = dns.RcodeFormatError
		_ = writer.WriteMsg(response)
		return
	}
	question := request.Question[0]
	if question.Qclass != dns.ClassINET {
		response.Rcode = dns.RcodeRefused
		_ = writer.WriteMsg(response)
		return
	}
	if s.localName(question.Name) {
		response.Authoritative = true
		response.RecursionAvailable = false
		if question.Qtype == dns.TypeA {
			response.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   append(net.IP(nil), s.ip...),
			}}
		}
		_ = writer.WriteMsg(response)
		return
	}
	if s.upstream == "" {
		response.Rcode = dns.RcodeServerFailure
		_ = writer.WriteMsg(response)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	upstreamResponse, err := s.exchange(ctx, request)
	if err != nil {
		response.Rcode = dns.RcodeServerFailure
		_ = writer.WriteMsg(response)
		return
	}
	upstreamResponse.Id = request.Id
	upstreamResponse.Compress = true
	_ = writer.WriteMsg(upstreamResponse)
}

func (s *Server) localName(name string) bool {
	name = strings.ToLower(dns.Fqdn(name))
	return name == dns.Fqdn(s.domain) || strings.HasSuffix(name, "."+dns.Fqdn(s.domain)) ||
		name == "nintendowifi.net." || strings.HasSuffix(name, ".nintendowifi.net.") ||
		name == "gamespy.com." || strings.HasSuffix(name, ".gamespy.com.")
}

func (s *Server) exchange(ctx context.Context, request *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{Net: "udp", Timeout: s.timeout, UDPSize: 1232}
	response, _, err := client.ExchangeContext(ctx, request, s.upstream)
	if err != nil {
		return nil, err
	}
	if response.Truncated {
		client.Net = "tcp"
		response, _, err = client.ExchangeContext(ctx, request, s.upstream)
	}
	return response, err
}

func upstreamAddress(configured, localAddress string) (string, error) {
	value := configured
	if value == "" {
		configuration, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return "", fmt.Errorf("read system DNS upstream: %w", err)
		}
		if len(configuration.Servers) == 0 {
			return "", nil
		}
		value = net.JoinHostPort(configuration.Servers[0], configuration.Port)
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		if ip := net.ParseIP(value); ip != nil {
			host, port = value, "53"
		} else {
			return "", fmt.Errorf("invalid DNS upstream %q: use IP address or IP:port", value)
		}
	}
	if net.ParseIP(host) == nil {
		return "", fmt.Errorf("DNS upstream must be an IP address, not %q", host)
	}
	if port == "0" {
		return "", fmt.Errorf("DNS upstream port cannot be zero")
	}
	if value == localAddress {
		return "", fmt.Errorf("DNS upstream cannot be this server (%s)", localAddress)
	}
	return net.JoinHostPort(host, port), nil
}

func validDNSName(name string) bool {
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
