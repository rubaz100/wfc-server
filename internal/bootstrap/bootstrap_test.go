package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFirstRunIsCompleteAndPreservesExistingFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested", "data")
	result, err := Ensure(directory, "10.0.20.246")
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
	again, err := Ensure(directory, "")
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

func TestRefusesPartialOrUnsafeDataDirectory(t *testing.T) {
	partial := t.TempDir()
	if err := os.WriteFile(filepath.Join(partial, "do-not-delete"), []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(partial, "10.0.20.246"); err == nil {
		t.Fatal("unrecognized nonempty directory was initialized")
	}
	if _, err := os.Stat(filepath.Join(partial, "do-not-delete")); err != nil {
		t.Fatal("existing file was removed")
	}
	if _, err := Ensure(t.TempDir(), "8.8.8.8"); err == nil {
		t.Fatal("public LAN address was accepted")
	}
}
