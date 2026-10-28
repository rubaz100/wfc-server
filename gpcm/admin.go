package gpcm

import (
	"fmt"
	"net"
	"wwfc/common"
	"wwfc/database"
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

// ProfileInfo is a public snapshot of a stored account for the admin API,
// merged with the live session when the player is currently online.
type ProfileInfo struct {
	ProfileID    uint32 `json:"pid"`
	FriendCode   string `json:"fc"`
	Name         string `json:"name"`
	FirstName    string `json:"firstname"`
	LastName     string `json:"lastname"`
	Email        string `json:"email"`
	UniqueNick   string `json:"unique_nick"`
	GameCode     string `json:"game_code"`
	OpenHost     bool   `json:"open_host"`
	Restricted   bool   `json:"restricted"`
	Online       bool   `json:"online"`
	LastIP       string `json:"last_ip"`
	LastInGameSn string `json:"last_ingamesn"`
	Message      string `json:"message"`
}

func profileInfoFromUser(user database.User, online bool, liveName string) ProfileInfo {
	gameID := ""
	if len(user.GsbrCode) >= 4 {
		gameID = user.GsbrCode[:4]
	}
	friendCode := ""
	if user.ProfileId != 0 && len(gameID) >= 4 {
		friendCode = common.CalcFriendCodeString(user.ProfileId, gameID)
	}
	name := liveName
	if name == "" {
		name = user.LastInGameSn
	}
	return ProfileInfo{
		ProfileID: user.ProfileId, FriendCode: friendCode, Name: name,
		FirstName: user.FirstName, LastName: user.LastName, Email: user.Email,
		UniqueNick: user.UniqueNick, GameCode: gameID, OpenHost: user.OpenHost,
		Restricted: user.Restricted, Online: online,
		LastIP: user.LastIPAddress, LastInGameSn: user.LastInGameSn,
	}
}

// GetProfileInfo returns the stored account for a profile ID, preferring the
// live session when the player is online. The bool is false if no such account.
func GetProfileInfo(profileId uint32) (ProfileInfo, bool) {
	mutex.Lock()
	defer mutex.Unlock()

	var info ProfileInfo
	if session, ok := sessions[profileId]; ok && session.LoggedIn {
		info = profileInfoFromUser(session.User, true, session.InGameName)
	} else {
		user, ok := db.GetProfile(profileId)
		if !ok {
			return ProfileInfo{}, false
		}
		info = profileInfoFromUser(user, false, "")
	}
	info.Message = GetPlayerMessage(profileId)
	return info, true
}

// UpdateProfileInfo edits the stored account fields that are safe to change from
// the panel: display first/last name and the open-host flag. A nil pointer
// leaves that field untouched. Returns false if the account does not exist.
// If the player is online, the change is applied to the live session too.
func UpdateProfileInfo(profileId uint32, firstName, lastName *string, openHost *bool, message *string) bool {
	// The per-player message has its own store and lock; set it outside the
	// session mutex. A nil pointer leaves it unchanged; empty clears it.
	if message != nil {
		SetPlayerMessage(profileId, *message)
	}

	mutex.Lock()
	defer mutex.Unlock()

	data := map[string]string{}
	if firstName != nil {
		data["firstname"] = *firstName
	}
	if lastName != nil {
		data["lastname"] = *lastName
	}
	if openHost != nil {
		if *openHost {
			data["wl:oh"] = "1"
		} else {
			data["wl:oh"] = "0"
		}
	}
	if len(data) == 0 {
		return true
	}

	if session, ok := sessions[profileId]; ok && session.LoggedIn {
		db.UpdateProfile(&session.User, data)
		return true
	}

	user, ok := db.GetProfile(profileId)
	if !ok {
		return false
	}
	db.UpdateProfile(&user, data)
	return true
}
