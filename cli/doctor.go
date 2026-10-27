package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
	"wwfc/common"
	"wwfc/internal/bootstrap"
	"wwfc/internal/privbind"

	"github.com/miekg/dns"
)

func runDoctor(ctx context.Context, result bootstrap.Result, options options, stdout io.Writer) error {
	config := result.Config
	if err := applyOverrides(&config, options); err != nil {
		return err
	}
	specs, err := bindSpecs(config)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "PASS: embedded assets, configuration, payload key, and certificates")
	running := false
	for _, spec := range specs {
		state, err := probeSocket(spec)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s %s %s: %s\n", spec.Name, spec.Network, spec.Address, state)
		running = running || state == "in use (checking service identity below)"
	}
	if !running {
		fmt.Fprintln(stdout, "Server is not running; all sockets are available.")
		return nil
	}
	if err := checkDNS(ctx, config); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "PASS: local DNS A record and non-local forwarding")
	if err := checkHTTP(config, result); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "PASS: NAS HTTP, HTTPS, and exact stage 1 payloads")
	if err := checkGPCM(config); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "PASS: GPCM sends a GameSpy login challenge")
	return nil
}

func probeSocket(spec privbind.Spec) (string, error) {
	if spec.Network == "tcp" {
		connection, err := net.DialTimeout("tcp", spec.Address, 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return "in use (checking service identity below)", nil
		}
		listener, err := net.Listen("tcp", spec.Address)
		if err == nil {
			_ = listener.Close()
			return "available", nil
		}
		if permission(err) {
			return "permission required (run mkwfc run to authorize)", nil
		}
		if errors.Is(err, syscall.EADDRINUSE) {
			return "in use (checking service identity below)", nil
		}
		return "", fmt.Errorf("%s is unavailable: %w", spec.Address, err)
	}
	packet, err := net.ListenPacket("udp", spec.Address)
	if err == nil {
		_ = packet.Close()
		return "available", nil
	}
	if permission(err) {
		return "permission required (run mkwfc run to authorize)", nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return "in use (checking service identity below)", nil
	}
	return "", fmt.Errorf("%s is unavailable: %w", spec.Address, err)
}

func permission(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func checkDNS(ctx context.Context, config common.Config) error {
	for _, name := range []string{"naswii.nintendowifi.net.", "example.com."} {
		query := new(dns.Msg).SetQuestion(name, dns.TypeA)
		for _, network := range []string{"udp", "tcp"} {
			client := &dns.Client{Net: network, Timeout: 3 * time.Second}
			response, _, err := client.ExchangeContext(ctx, query, config.DNSAddress)
			if err != nil {
				return fmt.Errorf("DNS %s query for %s: %w", network, name, err)
			}
			if strings.HasSuffix(name, "nintendowifi.net.") {
				if len(response.Answer) != 1 || !response.Answer[0].(*dns.A).A.Equal(net.ParseIP(config.LANAddress)) {
					return fmt.Errorf("DNS returned the wrong local answer: %v", response.Answer)
				}
			} else if response.Rcode == dns.RcodeServerFailure || response.Rcode == dns.RcodeRefused {
				return fmt.Errorf("DNS forwarding failed with %s", dns.RcodeToString[response.Rcode])
			}
		}
	}
	return nil
}

func checkHTTP(config common.Config, result bootstrap.Result) error {
	address := net.JoinHostPort(*config.NASAddress, config.NASPort)
	client := &http.Client{Timeout: 3 * time.Second}
	request := func(url string) *http.Request {
		request, _ := http.NewRequest(http.MethodGet, url, nil)
		request.Host = "naswii.nintendowifi.net"
		return request
	}
	for _, version := range []int{0, 1} {
		expected, _ := result.Catalog.Stage1(version)
		response, err := client.Do(request(fmt.Sprintf("http://%s/w%d", address, version)))
		if err != nil {
			return fmt.Errorf("NAS HTTP stage 1: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 65536))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, expected) {
			return fmt.Errorf("NAS HTTP stage 1 version %d returned status %d or unexpected bytes", version, response.StatusCode)
		}
	}
	salt := strings.Repeat("01", 32)
	rawQuery := "g=RMCPD00&s=" + salt
	saltHash := sha256.Sum256([]byte("payload?" + rawQuery))
	response, err := client.Do(request("http://" + address + "/payload?" + rawQuery + "&h=" + hex.EncodeToString(saltHash[:4])))
	if err != nil {
		return fmt.Errorf("signed payload request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || len(body) < 0x130 {
		return fmt.Errorf("signed payload returned status %d and %d bytes", response.StatusCode, len(body))
	}
	if config.EnableHTTPS {
		httpsClient := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}}
		response, err := httpsClient.Do(request("https://" + net.JoinHostPort(*config.NASAddressHTTPS, config.NASPortHTTPS) + "/nastest.jsp"))
		if err != nil {
			return fmt.Errorf("NAS HTTPS request: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 65536))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("AuthServer is up")) {
			return fmt.Errorf("unexpected NAS HTTPS response: status %d", response.StatusCode)
		}
	}
	return nil
}

func checkGPCM(config common.Config) error {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(*config.GameSpyAddress, "29900"), 2*time.Second)
	if err != nil {
		return fmt.Errorf("GPCM challenge: %w", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	challenge := make([]byte, 0, 128)
	buffer := make([]byte, 128)
	for !bytes.HasSuffix(challenge, []byte(`\final\`)) {
		n, err := connection.Read(buffer)
		challenge = append(challenge, buffer[:n]...)
		if err != nil || len(challenge) > 4096 {
			return fmt.Errorf("unexpected GPCM challenge %q", challenge)
		}
	}
	// GameSpy message fields come from a Go map, so the order of \challenge\ and
	// \id\ is not fixed; assert on the command and the challenge field's presence.
	if !bytes.HasPrefix(challenge, []byte(`\lc\1\`)) || !bytes.Contains(challenge, []byte(`\challenge\`)) {
		return fmt.Errorf("unexpected GPCM challenge %q", challenge)
	}
	return nil
}
