package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	dataPathMutex sync.RWMutex
	dataPath      = "."
)

func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user configuration directory: %w", err)
	}
	return filepath.Join(base, "mkwfc"), nil
}

func SetDataPath(path string) error {
	if path == "" {
		return fmt.Errorf("data directory cannot be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dataPathMutex.Lock()
	changed := dataPath != absolute
	dataPath = absolute
	dataPathMutex.Unlock()
	if changed {
		cmutex.Lock()
		config = Config{}
		configLoaded = false
		cmutex.Unlock()
	}
	return nil
}

func DataPath(parts ...string) string {
	dataPathMutex.RLock()
	base := dataPath
	dataPathMutex.RUnlock()
	elements := []string{base}
	for _, part := range parts {
		if part != "" {
			elements = append(elements, part)
		}
	}
	return filepath.Join(elements...)
}

func ConfigPath() string { return DataPath("config.xml") }

func ResolveConfigPaths(config *Config) {
	for _, path := range []*string{
		&config.DatabasePath, &config.CertPath, &config.KeyPath,
		&config.CertPathWii, &config.KeyPathWii, &config.CertPathDS,
		&config.WiiCertPathDS, &config.KeyPathDS,
	} {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = DataPath(*path)
		}
	}
}

func ValidatePrivateIPv4(value string) (string, error) {
	ip, err := ParsePrivateIPv4(value)
	if err != nil {
		return "", err
	}
	return ip, nil
}

func ParsePrivateIPv4(value string) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("%q is not an IPv4 address", value)
	}
	var octets [4]int
	for i, part := range parts {
		if part == "" || len(part) > 3 {
			return "", fmt.Errorf("%q is not an IPv4 address", value)
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("%q is not an IPv4 address", value)
			}
		}
		if _, err := fmt.Sscanf(part, "%d", &octets[i]); err != nil || octets[i] > 255 {
			return "", fmt.Errorf("%q is not an IPv4 address", value)
		}
	}
	if octets[0] != 10 && !(octets[0] == 172 && octets[1] >= 16 && octets[1] <= 31) && !(octets[0] == 192 && octets[1] == 168) {
		return "", fmt.Errorf("%q is not an RFC1918 private IPv4 address", value)
	}
	return fmt.Sprintf("%d.%d.%d.%d", octets[0], octets[1], octets[2], octets[3]), nil
}
