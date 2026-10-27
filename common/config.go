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
	cmutex.Lock()
	defer cmutex.Unlock()

	if configLoaded {
		return config
	}

	data, err := os.ReadFile("config.xml")
	if err != nil {
		panic(err)
	}

	config.AllowDefaultDolphinKeys = true
	config.AllowMultipleDeviceIDs = "never"
	config.AllowConnectWithoutDeviceID = false
	config.ServerName = "WiiLink"

	err = xml.Unmarshal(data, &config)
	if err != nil {
		panic(err)
	}

	if config.GameSpyAddress == nil {
		config.GameSpyAddress = &config.DefaultAddress
	}

	if config.NASAddress == nil {
		config.NASAddress = &config.DefaultAddress
	}

	if config.NASAddressHTTPS == nil {
		if config.NASAddress != nil {
			config.NASAddressHTTPS = config.NASAddress
		} else {
			config.NASAddressHTTPS = &config.DefaultAddress
		}
	}

	if config.EnableHTTPSExploitWii == nil {
		enable := true
		config.EnableHTTPSExploitWii = &enable
	}

	if config.EnableHTTPSExploitDS == nil {
		enable := true
		config.EnableHTTPSExploitDS = &enable
	}

	if config.LogLevel == nil {
		level := 4
		config.LogLevel = &level
	}

	if config.LogOutput == "" {
		config.LogOutput = "StdOutAndFile"
	}

	if config.FrontendAddress == "" {
		config.FrontendAddress = "127.0.0.1:29998"
	}

	if config.BackendAddress == "" {
		config.BackendAddress = "127.0.0.1:29999"
	}

	if config.FrontendBackendAddress == "" {
		config.FrontendBackendAddress = config.BackendAddress
	}

	if config.BackendFrontendAddress == "" {
		config.BackendFrontendAddress = config.FrontendAddress
	}

	if config.AdminAddress == "" {
		config.AdminAddress = "127.0.0.1:29990"
	}

	if config.DatabasePath == "" {
		config.DatabasePath = "mkwfc.db"
	}

	config.AllowMultipleDeviceIDs = normalizeMultipleDeviceIDs(config.AllowMultipleDeviceIDs)

	configLoaded = true

	return config
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
