package api

import (
	"net/http"
	"sort"
	"wwfc/gpcm"
)

// HandlePlayers returns the list of currently connected (logged-in) players,
// including in-game name, friend code, IP and NG device ID. Requires the secret.
func HandlePlayers(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleModerator); err != nil {
		return
	}

	players := gpcm.GetSessions()
	sort.Slice(players, func(i, j int) bool {
		return players[i].ProfileID < players[j].ProfileID
	})

	if len(players) == 0 {
		replyOK(w, "[]")
		return
	}

	replyOK(w, players)
}
