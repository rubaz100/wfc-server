package bootstrap

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
	"wwfc/common"
	"wwfc/internal/assets"
	"wwfc/internal/certs"
	"wwfc/internal/payload"
)

type Result struct {
	Config  common.Config
	Catalog *payload.Catalog
	Created bool
}

func Ensure(dataDir, lanAddress string) (Result, error) {
	if err := common.SetDataPath(dataDir); err != nil {
		return Result{}, err
	}
	absolute := common.DataPath()
	info, err := os.Stat(absolute)
	createdDirectory := os.IsNotExist(err)
	if createdDirectory {
		if err := os.MkdirAll(absolute, 0700); err != nil {
			return Result{}, fmt.Errorf("create data directory: %w", err)
		}
		info, err = os.Stat(absolute)
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("data path %s is not a directory", absolute)
	}
	if info.Mode().Perm() != 0700 {
		return Result{}, fmt.Errorf("data directory %s must have mode 0700; no permissions were changed", absolute)
	}
	created := createdDirectory
	if !created {
		entries, err := os.ReadDir(absolute)
		if err != nil {
			return Result{}, err
		}
		created = len(entries) == 0
	}
	if lanAddress == "" {
		if created {
			return Result{}, fmt.Errorf("a LAN IPv4 address is required for first-run initialization")
		}
	} else if _, err := common.ParsePrivateIPv4(lanAddress); err != nil {
		return Result{}, err
	}
	if created && lanAddress == "" {
		return Result{}, fmt.Errorf("no private LAN address; use --lan-ip")
	}
	paths := certs.DefaultPaths(absolute)
	keyPath := filepath.Join(absolute, "payload-key.pem")
	bundle, err := assets.Open()
	if err != nil {
		return Result{}, err
	}
	common.SetGameList(bundle.Files["game_list.tsv"])
	if !created {
		if _, err := os.Stat(common.ConfigPath()); err != nil {
			return Result{}, fmt.Errorf("data directory is partially initialized or unrecognized: %w", err)
		}
		config, err := common.LoadConfig()
		if err != nil {
			return Result{}, fmt.Errorf("load existing configuration: %w", err)
		}
		if config.APISecret == "" {
			return Result{}, fmt.Errorf("apiSecret must not be empty")
		}
		paths = certs.Paths{ModernCert: config.CertPath, ModernKey: config.KeyPath, WiiCert: config.CertPathWii, WiiKey: config.KeyPathWii}
		for _, path := range []string{paths.ModernCert, paths.ModernKey, paths.WiiCert, paths.WiiKey, keyPath} {
			if info, err := os.Stat(path); err != nil || info.IsDir() {
				return Result{}, fmt.Errorf("data directory is missing required file %s; it will not be replaced", path)
			}
		}
		if err := certs.Validate(paths); err != nil {
			return Result{}, fmt.Errorf("validate certificates: %w", err)
		}
		key, err := payload.LoadPrivateKey(keyPath)
		if err != nil {
			return Result{}, err
		}
		catalog, err := payload.NewCatalog(bundle, key)
		if err != nil {
			return Result{}, fmt.Errorf("load payload catalog: %w", err)
		}
		if err := catalog.ExportGecko(filepath.Join(absolute, "gecko")); err != nil {
			return Result{}, err
		}
		return Result{Config: config, Catalog: catalog}, nil
	}

	ip, err := common.ParsePrivateIPv4(lanAddress)
	if err != nil {
		return Result{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Result{}, err
	}
	configText := fmt.Sprintf(`<Config>
    <address>%[1]s</address>
    <lanAddress>%[1]s</lanAddress>
    <gsAddress>%[1]s</gsAddress>
    <nasAddress>%[1]s</nasAddress>
    <nasPort>80</nasPort>
    <nasAddressHttps>%[1]s</nasAddressHttps>
    <nasPortHttps>443</nasPortHttps>
    <enableHttps>true</enableHttps>
    <enableHttpsExploitWii>true</enableHttpsExploitWii>
    <enableHttpsExploitDS>false</enableHttpsExploitDS>
    <localDomain>mkwfc.lan</localDomain>
    <dnsAddress>%[1]s:53</dnsAddress>
    <dnsUpstream></dnsUpstream>
    <payloadServerAddress></payloadServerAddress>
    <databasePath>mkwfc.db</databasePath>
    <certPath>cert.pem</certPath>
    <keyPath>key.pem</keyPath>
    <certDerPathWii>naswii-cert.der</certDerPathWii>
    <keyPathWii>naswii-key.pem</keyPathWii>
    <apiSecret>%[2]s</apiSecret>
    <adminAddress>127.0.0.1:29990</adminAddress>
    <allowDefaultDolphinKeys>true</allowDefaultDolphinKeys>
    <allowMultipleDeviceIDs>SameIPAddress</allowMultipleDeviceIDs>
    <allowConnectWithoutDeviceID>false</allowConnectWithoutDeviceID>
    <serverName>mkwfc LAN</serverName>
    <logLevel>4</logLevel>
    <logOutput>StdOut</logOutput>
    <allowUnsafeBind>false</allowUnsafeBind>
    <eventReporting><logToDatabase>true</logToDatabase></eventReporting>
</Config>
`, ip, base64.RawURLEncoding.EncodeToString(secret))
	generated := []string{keyPath, paths.ModernKey, paths.ModernCert, paths.WiiKey, paths.WiiCert, common.ConfigPath(), filepath.Join(absolute, "motd.txt")}
	cleanup := func() {
		for _, path := range generated {
			_ = os.Remove(path)
		}
		_ = os.RemoveAll(filepath.Join(absolute, "gecko"))
		if createdDirectory {
			_ = os.Remove(absolute)
		}
	}
	fail := func(err error) (Result, error) {
		cleanup()
		return Result{}, err
	}
	if _, err := payload.GeneratePrivateKey(keyPath); err != nil {
		return fail(err)
	}
	if err := certs.Generate(paths, "mkwfc.lan", net.ParseIP(ip), time.Now()); err != nil {
		return fail(err)
	}
	if err := writeNew(common.ConfigPath(), []byte(configText), 0600); err != nil {
		return fail(err)
	}
	if err := writeNew(filepath.Join(absolute, "motd.txt"), []byte("Welcome to mkwfc LAN"), 0644); err != nil {
		return fail(err)
	}
	catalog, err := payload.LoadCatalog(keyPath)
	if err != nil {
		return fail(err)
	}
	if err := catalog.ExportGecko(filepath.Join(absolute, "gecko")); err != nil {
		return fail(err)
	}
	config, err := common.LoadConfig()
	if err != nil {
		return fail(err)
	}
	return Result{Config: config, Catalog: catalog, Created: true}, nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
