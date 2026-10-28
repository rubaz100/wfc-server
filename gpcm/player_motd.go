package gpcm

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"wwfc/common"
)

// Per-player connect messages. These are delivered as the client's wl:motd in
// the login reply, so the shipped payload shows them on the Nintendo WFC menu
// when that player connects — no client change needed. A per-player message
// takes precedence over the global motd.txt for that player. Stored as a small
// JSON file in the data directory so it survives restarts.
var (
	playerMessageMutex sync.Mutex
	playerMessages     map[uint32]string
)

func playerMessagePath() string { return common.DataPath("player_motd.json") }

// ensurePlayerMessages lazily loads the store on first use. Caller holds the mutex.
func ensurePlayerMessages() {
	if playerMessages != nil {
		return
	}
	playerMessages = map[uint32]string{}
	data, err := os.ReadFile(playerMessagePath())
	if err != nil {
		return
	}
	raw := map[string]string{}
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	for k, v := range raw {
		if pid, err := strconv.ParseUint(k, 10, 32); err == nil {
			playerMessages[uint32(pid)] = v
		}
	}
}

// savePlayerMessages writes the store atomically. Caller holds the mutex.
func savePlayerMessages() {
	raw := make(map[string]string, len(playerMessages))
	for pid, msg := range playerMessages {
		raw[strconv.FormatUint(uint64(pid), 10)] = msg
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return
	}
	tmp := playerMessagePath() + ".tmp"
	if os.WriteFile(tmp, data, 0644) == nil {
		_ = os.Rename(tmp, playerMessagePath())
	}
}

// GetPlayerMessage returns the per-player connect message, or "" if none is set.
func GetPlayerMessage(profileId uint32) string {
	playerMessageMutex.Lock()
	defer playerMessageMutex.Unlock()
	ensurePlayerMessages()
	return playerMessages[profileId]
}

// SetPlayerMessage sets the per-player connect message, or clears it when msg is
// empty. It takes effect the next time that player connects (wl:motd is sent in
// the login reply), so an online player sees it after reconnecting.
func SetPlayerMessage(profileId uint32, msg string) {
	playerMessageMutex.Lock()
	defer playerMessageMutex.Unlock()
	ensurePlayerMessages()
	if msg == "" {
		delete(playerMessages, profileId)
	} else {
		playerMessages[profileId] = msg
	}
	savePlayerMessages()
}
