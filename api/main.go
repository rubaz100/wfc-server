package api

import (
	"fmt"
	"net/http"
	"wwfc/common"
	"wwfc/database"
	"wwfc/logging"
)

var db database.Connection

func StartServer(reload bool) {
	// Get config
	config := common.GetConfig()

	// Start SQL
	db = database.Start(config)

	if err := loadAdminState(); err != nil {
		db.Close()
		panic(fmt.Errorf("initialize admin authentication: %w", err))
	}
	if err := db.EnsureHostManagerPreferences(); err != nil {
		// Display preferences are optional; do not interrupt console service
		// or replace existing values when the preference store is unavailable.
		logging.Error("API:admin", "Initialize panel preferences:", err)
	}

	db.RegisterEvents(config, []string{
		"profile_kicked",
		"profile_banned",
		"profile_unbanned",
	})
}

func Shutdown() {
	db.Close()
}

func RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/groups", HandleGroups)
	mux.HandleFunc("/api/stats", HandleStats)
	mux.HandleFunc("/api/ban", HandleBan)
	mux.HandleFunc("/api/unban", HandleUnban)
	mux.HandleFunc("/api/kick", HandleKick)
	mux.HandleFunc("/api/baninfo", HandleBanInfo)
}

// RegisterExtendedHandlers registers the base API plus the Host Manager
// endpoints used by the admin web panel. These expose player IPs, ban lists,
// MOTD and config, so they are intended for the localhost/LAN admin server
// rather than the public console-facing NAS server.
func RegisterExtendedHandlers(mux *http.ServeMux) {
	RegisterHandlers(mux)
	mux.HandleFunc("/api/players", HandlePlayers)
	mux.HandleFunc("/api/bans", HandleBans)
	mux.HandleFunc("/api/motd", HandleMOTD)
	mux.HandleFunc("/api/config", HandleConfig)
	mux.HandleFunc("/api/preferences", HandlePreferences)
	mux.HandleFunc("/api/profile", HandleProfile)
	mux.HandleFunc("/api/auth/status", HandleAuthStatus)
	mux.HandleFunc("/api/login", HandleLogin)
	mux.HandleFunc("/api/logout", HandleLogout)
	mux.HandleFunc("/api/password", HandleChangePassword)
}
