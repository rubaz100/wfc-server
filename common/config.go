package common

import (
	"encoding/xml"
	"os"
	"wwfc/logging"

	"github.com/linkdata/deadlock"
)

type Config struct {
	// DatabasePath is the SQLite database file. The server is single-binary and
	// embeds SQLite, so the Postgres fields below are retained only for backward
	// compatibility with older config files and are no longer used.
	DatabasePath string `xml:"databasePath"`

	Username        string `xml:"username"`
	Password        string `xml:"password"`
	DatabaseAddress string `xml:"databaseAddress"`
	DatabaseName    string `xml:"databaseName"`

	DefaultAddress       string  `xml:"address"`
	GameSpyAddress       *string `xml:"gsAddress,omitempty"`
	NASAddress           *string `xml:"nasAddress,omitempty"`
	NASPort              string  `xml:"nasPort"`
	NASAddressHTTPS      *string `xml:"nasAddressHttps,omitempty"`
	NASPortHTTPS         string  `xml:"nasPortHttps"`
	PayloadServerAddress string  `xml:"payloadServerAddress"`
	LocalDomain          string  `xml:"localDomain"`
	LANAddress           string  `xml:"lanAddress"`
	DNSAddress           string  `xml:"dnsAddress"`
	DNSUpstream          string  `xml:"dnsUpstream"`
	AllowUnsafeBind      bool    `xml:"allowUnsafeBind"`

	FrontendAddress        string `xml:"frontendAddress"`
	FrontendBackendAddress string `xml:"frontendBackendAddress"`
	BackendAddress         string `xml:"backendAddress"`
	BackendFrontendAddress string `xml:"backendFrontendAddress"`

	EnableHTTPS           bool  `xml:"enableHttps"`
	EnableHTTPSExploitWii *bool `xml:"enableHttpsExploitWii,omitempty"`
	EnableHTTPSExploitDS  *bool `xml:"enableHttpsExploitDS,omitempty"`

	LogLevel  *int   `xml:"logLevel"`
	LogOutput string `xml:"logOutput"`

	CertPath      string `xml:"certPath"`
	KeyPath       string `xml:"keyPath"`
	CertPathWii   string `xml:"certDerPathWii"`
	KeyPathWii    string `xml:"keyPathWii"`
	CertPathDS    string `xml:"certDerPathDS"`
	WiiCertPathDS string `xml:"wiiCertDerPathDS"`
	KeyPathDS     string `xml:"keyPathDS"`

	APISecret string `xml:"apiSecret"`

	// AdminAddress is where the Host Manager web panel + API listen.
	AdminAddress string `xml:"adminAddress"`

	AllowDefaultDolphinKeys     bool   `xml:"allowDefaultDolphinKeys"`
	AllowMultipleDeviceIDs      string `xml:"allowMultipleDeviceIDs"`
	AllowConnectWithoutDeviceID bool   `xml:"allowConnectWithoutDeviceID"`

	ServerName string `xml:"serverName,omitempty"`

	EventReporting EventReportingConfig `xml:"eventReporting"`
}

type EventReportingConfig struct {
	LogToDatabase bool                    `xml:"logToDatabase"`
	Webhooks      []logging.WebhookConfig `xml:"discord>webhook"`
}

var (
	config       Config
	configLoaded bool
	cmutex       = deadlock.Mutex{}
)

func GetConfig() Config {
	config, err := LoadConfig()
	if err != nil {
		panic(err)
	}
	return config
}

func LoadConfig() (Config, error) {
	cmutex.Lock()
	defer cmutex.Unlock()

	if configLoaded {
		return config, nil
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return Config{}, err
	}
	loaded := Config{
		AllowDefaultDolphinKeys: true,
		AllowMultipleDeviceIDs:  "never",
		ServerName:              "mkwfc",
		LocalDomain:             "mkwfc.lan",
	}
	if err := xml.Unmarshal(data, &loaded); err != nil {
		return Config{}, err
	}
	if loaded.GameSpyAddress == nil {
		loaded.GameSpyAddress = &loaded.DefaultAddress
	}
	if loaded.NASAddress == nil {
		loaded.NASAddress = &loaded.DefaultAddress
	}
	if loaded.NASAddressHTTPS == nil {
		loaded.NASAddressHTTPS = loaded.NASAddress
	}
	if loaded.EnableHTTPSExploitWii == nil {
		enable := true
		loaded.EnableHTTPSExploitWii = &enable
	}
	if loaded.EnableHTTPSExploitDS == nil {
		enable := false
		loaded.EnableHTTPSExploitDS = &enable
	}
	if loaded.LogLevel == nil {
		level := 4
		loaded.LogLevel = &level
	}
	if loaded.LogOutput == "" {
		loaded.LogOutput = "StdOutAndFile"
	}
	if loaded.FrontendAddress == "" {
		loaded.FrontendAddress = "127.0.0.1:29998"
	}
	if loaded.BackendAddress == "" {
		loaded.BackendAddress = "127.0.0.1:29999"
	}
	if loaded.FrontendBackendAddress == "" {
		loaded.FrontendBackendAddress = loaded.BackendAddress
	}
	if loaded.BackendFrontendAddress == "" {
		loaded.BackendFrontendAddress = loaded.FrontendAddress
	}
	if loaded.AdminAddress == "" {
		loaded.AdminAddress = "127.0.0.1:29990"
	}
	if loaded.DatabasePath == "" {
		loaded.DatabasePath = "mkwfc.db"
	}
	if loaded.LANAddress == "" {
		loaded.LANAddress = loaded.DefaultAddress
	}
	if loaded.DNSAddress == "" {
		loaded.DNSAddress = loaded.LANAddress + ":53"
	}
	loaded.AllowMultipleDeviceIDs = normalizeMultipleDeviceIDs(loaded.AllowMultipleDeviceIDs)
	ResolveConfigPaths(&loaded)
	config = loaded
	configLoaded = true
	return config, nil
}

func normalizeMultipleDeviceIDs(value string) string {
	switch value {
	case "true", "yes", "always":
		return "always"
	case "SameIPAddress":
		return value
	default:
		return "never"
	}
}

func (c Config) RegisterWebhooks() {
	for _, webhook := range c.EventReporting.Webhooks {
		webhook.RegisterWebhook()
	}
}
