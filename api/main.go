package api

import (
	"net/http"
	"wwfc/common"
	"wwfc/database"
)

var (
	db database.Connection

	apiSecret string
)

func StartServer(reload bool) {
	// Get config
	config := common.GetConfig()

	apiSecret = config.APISecret

	// Start SQL
	db = database.Start(config)

	// Cache whether an admin password exists (drives the bootstrap fallback).
	loadAdminState()

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
	mux.HandleFunc("/api/profile", HandleProfile)
	mux.HandleFunc("/api/auth/status", HandleAuthStatus)
	mux.HandleFunc("/api/setup", HandleSetup)
	mux.HandleFunc("/api/login", HandleLogin)
	mux.HandleFunc("/api/logout", HandleLogout)
	mux.HandleFunc("/api/password", HandleChangePassword)
}
