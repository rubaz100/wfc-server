package database

import (
	"path/filepath"
	"testing"
	"wwfc/common"
)

func TestAdminConfigInitializationPreservesExistingValue(t *testing.T) {
	c := Start(common.Config{DatabasePath: filepath.Join(t.TempDir(), "admin.db")})
	defer c.Close()
	if _, ok, err := c.GetAdminConfig("admin_password"); err != nil || ok {
		t.Fatalf("fresh password lookup: ok=%v err=%v", ok, err)
	}
	if created, err := c.SetAdminConfigIfAbsent("admin_password", "existing-password-hash"); err != nil || !created {
		t.Fatalf("initial insert: created=%v err=%v", created, err)
	}
	if created, err := c.SetAdminConfigIfAbsent("admin_password", "default-password-hash"); err != nil || created {
		t.Fatalf("repeat initialization: created=%v err=%v", created, err)
	}
	if value, ok, err := c.GetAdminConfig("admin_password"); err != nil || !ok || value != "existing-password-hash" {
		t.Fatalf("existing password overwritten: value=%q ok=%v err=%v", value, ok, err)
	}
	c.Close()
	if _, _, err := c.GetAdminConfig("admin_password"); err == nil {
		t.Fatal("closed database returned no read error")
	}
}
