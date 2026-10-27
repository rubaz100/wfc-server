package nas

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/rsa"
	"crypto/sha1"
	"io"
	"net"
	"os"
	"testing"
	"time"
	"wwfc/common"
	"wwfc/internal/certs"
)

func TestWiiLegacyHandshakeWithGeneratedCertificate(t *testing.T) {
	directory := t.TempDir()
	paths := certs.DefaultPaths(directory)
	if err := certs.Generate(paths, "mkwfc.lan", net.ParseIP("192.168.8.202"), time.Now()); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := setupTLS(common.Config{
		KeyPath: paths.ModernKey, CertPath: paths.ModernCert,
		KeyPathWii: paths.WiiKey, CertPathWii: paths.WiiCert,
		EnableHTTPSExploitWii: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection := &tlsConnection{Conn: server}
	serverResult := make(chan error, 1)
	go func() { serverResult <- connection.handleTLSHandshake() }()

	clientHello := append(bytes.Clone(wiiClientHelloPrefix), make([]byte, 0x2d-len(wiiClientHelloPrefix))...)
	if _, err := client.Write(clientHello); err != nil {
		t.Fatal(err)
	}
	certificate, err := os.ReadFile(paths.WiiCert)
	if err != nil {
		t.Fatal(err)
	}
	serverHello := make([]byte, 47+len(certificate)+24)
	if _, err := io.ReadFull(client, serverHello); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(serverHello, []byte{0x16, 0x03, 0x01, 0x00, 0x2a, 0x02}) ||
		!bytes.Equal(serverHello[44:47], []byte{0x00, 0x04, 0x00}) {
		t.Fatal("unexpected Wii ServerHello")
	}
	if !bytes.Equal(serverHello[62:62+len(certificate)], certificate) {
		t.Fatal("server sent a different Wii certificate")
	}

	transcript := bytes.Clone(clientHello[2:])
	transcript = append(transcript, serverHello[5:47]...)
	transcript = append(transcript, serverHello[52:62+len(certificate)]...)
	transcript = append(transcript, serverHello[67+len(certificate):71+len(certificate)]...)

	preMasterSecret := make([]byte, 48)
	if _, err := rand.Read(preMasterSecret); err != nil {
		t.Fatal(err)
	}
	copy(preMasterSecret, []byte{0x03, 0x01})
	encryptedSecret, err := rsa.EncryptPKCS1v15(rand.Reader, &rsaKeyWii.PublicKey, preMasterSecret)
	if err != nil {
		t.Fatal(err)
	}
	keyExchange := append([]byte{0x16, 0x03, 0x01, 0x00, 0x86, 0x10, 0x00, 0x00, 0x82, 0x00, 0x80}, encryptedSecret...)
	transcript = append(transcript, keyExchange[5:]...)
	clientRandom := append(make([]byte, 16), clientHello[0x1d:0x2d]...)
	serverRandom := serverHello[11:43]
	randomPair := append(bytes.Clone(clientRandom), serverRandom...)
	masterSecret := make([]byte, 48)
	prf10(masterSecret, preMasterSecret, []byte("master secret"), randomPair)
	clientMAC, serverMAC, clientKey, serverKey, _, _ := keysFromMasterSecret(VersionTLS10, masterSecret, clientRandom, serverRandom, 16, 16, 16)
	clientCipher, err := rc4.NewCipher(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	clientVerify := make([]byte, 12)
	clientMD5 := md5.Sum(transcript)
	clientSHA1 := sha1.Sum(transcript)
	prf10(clientVerify, masterSecret, []byte("client finished"), append(clientMD5[:], clientSHA1[:]...))
	clientFinished, _ := encryptTLS(macMD5(VersionTLS10, clientMAC), clientCipher, append([]byte{0x14, 0x00, 0x00, 0x0c}, clientVerify...), 0, []byte{0x16, 0x03, 0x01, 0x00, 0x10})
	packet := append(keyExchange, 0x14, 0x03, 0x01, 0x00, 0x01, 0x01)
	packet = append(packet, clientFinished...)
	writeResult := make(chan error, 1)
	go func() {
		_, err := client.Write(packet)
		writeResult <- err
	}()
	serverFinished := make([]byte, 43)
	if _, err := io.ReadFull(client, serverFinished); err != nil {
		select {
		case serverErr := <-serverResult:
			t.Fatalf("server handshake: %v (client read: %v)", serverErr, err)
		default:
			t.Fatal(err)
		}
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serverFinished[:6], []byte{0x14, 0x03, 0x01, 0x00, 0x01, 0x01}) || !bytes.HasPrefix(serverFinished[6:], []byte{0x16, 0x03, 0x01, 0x00, 0x20}) {
		t.Fatalf("unexpected Wii ChangeCipherSpec or Finished record: % X", serverFinished[:11])
	}
	decrypted := serverFinished[11:]
	serverCipher, err := rc4.NewCipher(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCipher.XORKeyStream(decrypted, decrypted)
	if !hmac.Equal(decrypted[16:], macMD5(VersionTLS10, serverMAC).MAC(nil, make([]byte, 8), []byte{0x16, 0x03, 0x01, 0x00, 0x10}, decrypted[:16], nil)) {
		t.Fatal("invalid server Finished MAC")
	}
	transcript = append(transcript, 0x14, 0x00, 0x00, 0x0c)
	transcript = append(transcript, clientVerify...)
	serverVerify := make([]byte, 12)
	serverMD5 := md5.Sum(transcript)
	serverSHA1 := sha1.Sum(transcript)
	prf10(serverVerify, masterSecret, []byte("server finished"), append(serverMD5[:], serverSHA1[:]...))
	if !bytes.Equal(decrypted[:16], append([]byte{0x14, 0x00, 0x00, 0x0c}, serverVerify...)) {
		t.Fatal("invalid server Finished verify_data")
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	if !connection.handshake {
		t.Fatal("Wii connection did not complete its handshake")
	}
}
