package bootstrap

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wwfc/internal/certs"
)

func TestFirstRunIsCompleteAndPreservesExistingFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested", "data")
	result, err := Ensure(directory, "10.0.20.246", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Config.LocalDomain != "mkwfc.lan" || len(result.Config.APISecret) < 32 {
		t.Fatalf("unexpected first-run result: %+v", result)
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("data directory mode = %v, err %v", info.Mode(), err)
	}
	configBefore, err := os.ReadFile(filepath.Join(directory, "config.xml"))
	if err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(filepath.Join(directory, "payload-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.DatabasePath != filepath.Join(directory, "mkwfc.db") {
		t.Fatalf("database path = %q", result.Config.DatabasePath)
	}
	again, err := Ensure(directory, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created {
		t.Fatal("existing data directory was reinitialized")
	}
	configAfter, _ := os.ReadFile(filepath.Join(directory, "config.xml"))
	keyAfter, _ := os.ReadFile(filepath.Join(directory, "payload-key.pem"))
	if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("second initialization changed configuration or private key")
	}
	if _, err := os.Stat(filepath.Join(directory, "gecko", "RMCPN0001.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunImportsSuppliedCertificates(t *testing.T) {
	// Pre-made certificates the operator supplies, generated into a scratch dir.
	source := certs.DefaultPaths(t.TempDir())
	if err := certs.Generate(source, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatal(err)
	}
	supplied := certs.Sources{ModernCert: source.ModernCert, ModernKey: source.ModernKey, WiiCert: source.WiiCert, WiiKey: source.WiiKey}

	directory := filepath.Join(t.TempDir(), "data")
	result, err := Ensure(directory, "10.0.20.246", &supplied)
	if err != nil {
		t.Fatalf("first-run import: %v", err)
	}
	if !result.Created {
		t.Fatal("import run was not treated as first-run initialization")
	}
	// The data directory must hold copies of the supplied certificates, not
	// freshly generated ones.
	for _, pair := range [][2]string{
		{source.ModernCert, filepath.Join(directory, "cert.pem")},
		{source.WiiCert, filepath.Join(directory, "naswii-cert.der")},
	} {
		want, _ := os.ReadFile(pair[0])
		got, err := os.ReadFile(pair[1])
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("data dir file %s does not match the supplied certificate (err %v)", pair[1], err)
		}
	}

	// Re-running with supplied certificates on an initialized directory must be
	// refused rather than silently ignored, and must not alter the stored files.
	before, _ := os.ReadFile(filepath.Join(directory, "cert.pem"))
	if _, err := Ensure(directory, "", &supplied); err == nil {
		t.Fatal("importing over an initialized data directory was accepted")
	}
	after, _ := os.ReadFile(filepath.Join(directory, "cert.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("a refused import changed the stored certificate")
	}
}

func TestRefusesPartialOrUnsafeDataDirectory(t *testing.T) {
	partial := t.TempDir()
	if err := os.WriteFile(filepath.Join(partial, "do-not-delete"), []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(partial, "10.0.20.246", nil); err == nil {
		t.Fatal("unrecognized nonempty directory was initialized")
	}
	if _, err := os.Stat(filepath.Join(partial, "do-not-delete")); err != nil {
		t.Fatal("existing file was removed")
	}
	if _, err := Ensure(t.TempDir(), "8.8.8.8", nil); err == nil {
		t.Fatal("public LAN address was accepted")
	}
}
