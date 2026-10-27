package api

import (
	"net/http"
	"strconv"
	"wwfc/common"
	"wwfc/gpcm"
	"wwfc/logging"
)

type ConfigResponseSpec struct {
	ServerName                  string `json:"serverName"`
	AdminAddress                string `json:"adminAddress"`
	GameSpyAddress              string `json:"gsAddress"`
	NASAddress                  string `json:"nasAddress"`
	NASPort                     string `json:"nasPort"`
	NASPortHTTPS                string `json:"nasPortHttps"`
	EnableHTTPS                 bool   `json:"enableHttps"`
	EnableHTTPSExploitWii       bool   `json:"enableHttpsExploitWii"`
	EnableHTTPSExploitDS        bool   `json:"enableHttpsExploitDS"`
	AllowDefaultDolphinKeys     bool   `json:"allowDefaultDolphinKeys"`
	AllowMultipleDeviceIDs      string `json:"allowMultipleDeviceIDs"`
	AllowConnectWithoutDeviceID bool   `json:"allowConnectWithoutDeviceID"`
	LogLevel                    int    `json:"logLevel"`
	LogOutput                   string `json:"logOutput"`
	DatabasePath                string `json:"databasePath"`
	DatabaseName                string `json:"databaseName"`
	DatabaseAddress             string `json:"databaseAddress"`
	PayloadServerAddress        string `json:"payloadServerAddress"`
}

type ConfigUpdateSpec struct {
	AuthInfo
	ServerName                  *string `json:"serverName"`
	AllowDefaultDolphinKeys     *bool   `json:"allowDefaultDolphinKeys"`
	AllowMultipleDeviceIDs      *string `json:"allowMultipleDeviceIDs"`
	AllowConnectWithoutDeviceID *bool   `json:"allowConnectWithoutDeviceID"`
	EnableHTTPSExploitWii       *bool   `json:"enableHttpsExploitWii"`
	EnableHTTPSExploitDS        *bool   `json:"enableHttpsExploitDS"`
	LogLevel                    *int    `json:"logLevel"`
}

// HandleConfig reads (GET) a redacted view of the server config or writes (POST)
// the editable subset, persisting to config.xml. Requires the secret.
func HandleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleSetConfig(w, r)
		return
	}
	handleGetConfig(w, r)
}

func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleModerator); err != nil {
		return
	}

	c := common.GetConfig()
	replyOK(w, ConfigResponseSpec{
		ServerName:                  c.ServerName,
		AdminAddress:                c.AdminAddress,
		GameSpyAddress:              derefString(c.GameSpyAddress),
		NASAddress:                  derefString(c.NASAddress),
		NASPort:                     c.NASPort,
		NASPortHTTPS:                c.NASPortHTTPS,
		EnableHTTPS:                 c.EnableHTTPS,
		EnableHTTPSExploitWii:       derefBool(c.EnableHTTPSExploitWii),
		EnableHTTPSExploitDS:        derefBool(c.EnableHTTPSExploitDS),
		AllowDefaultDolphinKeys:     c.AllowDefaultDolphinKeys,
		AllowMultipleDeviceIDs:      c.AllowMultipleDeviceIDs,
		AllowConnectWithoutDeviceID: c.AllowConnectWithoutDeviceID,
		LogLevel:                    derefInt(c.LogLevel),
		LogOutput:                   c.LogOutput,
		DatabasePath:                c.DatabasePath,
		DatabaseName:                c.DatabaseName,
		DatabaseAddress:             c.DatabaseAddress,
		PayloadServerAddress:        c.PayloadServerAddress,
	})
}

func handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req ConfigUpdateSpec
	if err := parsePost(r, w, &req, RoleModerator); err != nil {
		return
	}

	fields := map[string]string{}

	if req.ServerName != nil {
		if len(*req.ServerName) == 0 || len(*req.ServerName) > 128 {
			replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
			return
		}
		fields["serverName"] = *req.ServerName
	}
	if req.AllowDefaultDolphinKeys != nil {
		fields["allowDefaultDolphinKeys"] = boolStr(*req.AllowDefaultDolphinKeys)
	}
	if req.AllowMultipleDeviceIDs != nil {
		v := *req.AllowMultipleDeviceIDs
		if v != "never" && v != "always" && v != "SameIPAddress" {
			replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
			return
		}
		fields["allowMultipleDeviceIDs"] = v
	}
	if req.AllowConnectWithoutDeviceID != nil {
		fields["allowConnectWithoutDeviceID"] = boolStr(*req.AllowConnectWithoutDeviceID)
	}
	if req.EnableHTTPSExploitWii != nil {
		fields["enableHttpsExploitWii"] = boolStr(*req.EnableHTTPSExploitWii)
	}
	if req.EnableHTTPSExploitDS != nil {
		fields["enableHttpsExploitDS"] = boolStr(*req.EnableHTTPSExploitDS)
	}
	if req.LogLevel != nil {
		if *req.LogLevel < 0 || *req.LogLevel > 4 {
			replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
			return
		}
		fields["logLevel"] = strconv.Itoa(*req.LogLevel)
	}

	if len(fields) == 0 {
		replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
		return
	}

	if err := common.ApplyConfigChanges(fields); err != nil {
		logging.Error("API:admin", "Failed to apply config changes:", err)
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}

	// Apply runtime side-effects not picked up automatically from cached config.
	if req.AllowDefaultDolphinKeys != nil {
		gpcm.SetAllowDefaultDolphinKeys(*req.AllowDefaultDolphinKeys)
	}

	replyOK(w, nil)
	logging.Notice("API:admin", "Config updated")
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
