package nas

import (
	"bufio"
	"bytes"
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
	"wwfc/internal/certs"
)

type testPayloadService struct{}

func (testPayloadService) Stage1(version int) ([]byte, bool) {
	if version < 0 || version > 1 {
		return nil, false
	}
	return []byte{1, byte(version + 44)}, true
}

func (testPayloadService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("embedded " + r.URL.Path))
}

func TestRealHTTPAndHTTPSWithInjectedPayload(t *testing.T) {
	directory := t.TempDir()
	if err := common.SetDataPath(directory); err != nil {
		t.Fatal(err)
	}
	paths := certs.DefaultPaths(directory)
	if err := certs.Generate(paths, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatal(err)
	}
	configuration := `<Config><address>127.0.0.1</address><nasPort>80</nasPort><nasPortHttps>443</nasPortHttps><enableHttps>true</enableHttps><enableHttpsExploitWii>true</enableHttpsExploitWii><certPath>cert.pem</certPath><keyPath>key.pem</keyPath><certDerPathWii>naswii-cert.der</certDerPathWii><keyPathWii>naswii-key.pem</keyPathWii><apiSecret>test-only-secret-12345</apiSecret></Config>`
	if err := os.WriteFile(common.ConfigPath(), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	httpSocket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsSocket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := StartServerWithListeners(testPayloadService{}, httpSocket, httpsSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Shutdown)
	client := &http.Client{Timeout: 3 * time.Second}
	for _, test := range []struct {
		path, body string
		status     int
	}{{"/payload", "embedded /payload", 200}, {"/payload/deployment", "embedded /payload/deployment", 200}, {"/w0", string([]byte{1, 44}), 200}, {"/w1", string([]byte{1, 45}), 200}, {"/w2", "", 404}} {
		request, _ := http.NewRequest(http.MethodGet, "http://"+httpSocket.Addr().String()+test.path, nil)
		request.Host = "naswii.nintendowifi.net:" + fmt.Sprint(httpSocket.Addr().(*net.TCPAddr).Port)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != test.status || (test.body != "" && string(body) != test.body) {
			t.Fatalf("%s returned %d %q", test.path, response.StatusCode, body)
		}
	}
	connection, err := net.Dial("tcp", httpSocket.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(connection, "GET /nastest.jsp HTTP/1.1\r\nHost: naswii.nintendowifi.net\r\nHost: duplicate.invalid\r\nConnection: close\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("duplicate Host request failed: %v", err)
	}
	_ = response.Body.Close()
	_ = connection.Close()

	httpsClient := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}}
	request, _ := http.NewRequest(http.MethodGet, "https://"+httpsSocket.Addr().String()+"/nastest.jsp", nil)
	request.Host = "naswii.nintendowifi.net"
	response, err = httpsClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("AuthServer is up")) {
		t.Fatalf("HTTPS returned %d %q", response.StatusCode, body)
	}
}

func TestWiiClientAlert(t *testing.T) {
	alert := []byte{0x15, 0x03, 0x01, 0x00, 0x02, 0x02, 0x2e}
	err := wiiClientAlert(alert)
	want := "Wii client TLS alert: " + tls.AlertError(46).Error()
	if err == nil || err.Error() != want {
		t.Fatalf("alert = %v, want %q", err, want)
	}
	if err := wiiClientAlert(alert[:6]); err != nil {
		t.Fatalf("partial alert: %v", err)
	}
	if err := wiiClientAlert([]byte{0x16, 0x03, 0x01, 0x00, 0x86, 0x10, 0x00}); err != nil {
		t.Fatalf("key exchange was treated as an alert: %v", err)
	}
}

func TestMalformedModernCertificateIsStartupError(t *testing.T) {
	directory := t.TempDir()
	certPath := filepath.Join(directory, "bad-cert.pem")
	keyPath := filepath.Join(directory, "bad-key.pem")
	if err := os.WriteFile(certPath, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setupRealTLS(keyPath, certPath); err == nil {
		t.Fatal("malformed certificate/key was accepted")
	}
}
