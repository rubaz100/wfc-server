package gpcm

import (
	"fmt"
	"net"
	"wwfc/common"
)

// PlayerEntry is a public snapshot of a connected GPCM session for the admin API.
type PlayerEntry struct {
	ProfileID  uint32 `json:"pid"`
	Name       string `json:"name"`
	FriendCode string `json:"fc"`
	IP         string `json:"ip"`
	RemoteAddr string `json:"remote_addr"`
	DeviceID   string `json:"device_id"`
	GameName   string `json:"game"`
	GameCode   string `json:"game_code"`
	Restricted bool   `json:"restricted"`
	Status     string `json:"status"`
}

// GetSessions returns a snapshot of all logged-in players.
func GetSessions() []PlayerEntry {
	mutex.Lock()
	defer mutex.Unlock()

	out := make([]PlayerEntry, 0, len(sessions))
	for pid, session := range sessions {
		if !session.LoggedIn {
			continue
		}

		ip := session.RemoteAddr
		if host, _, err := net.SplitHostPort(session.RemoteAddr); err == nil {
			ip = host
		}

		gameID := session.GameCode
		if len(session.User.GsbrCode) >= 4 {
			gameID = session.User.GsbrCode[:4]
		}

		friendCode := ""
		if pid != 0 && len(gameID) >= 4 {
			friendCode = common.CalcFriendCodeString(pid, gameID[:4])
		}

		out = append(out, PlayerEntry{
			ProfileID:  pid,
			Name:       session.InGameName,
			FriendCode: friendCode,
			IP:         ip,
			RemoteAddr: session.RemoteAddr,
			DeviceID:   fmt.Sprintf("%08x", session.DeviceId),
			GameName:   session.GameName,
			GameCode:   session.GameCode,
			Restricted: session.User.Restricted,
			Status:     session.Status,
		})
	}

	return out
}

// SetAllowDefaultDolphinKeys updates the live moderation flag used during login.
func SetAllowDefaultDolphinKeys(v bool) {
	allowDefaultDolphinKeys.Store(v)
}
