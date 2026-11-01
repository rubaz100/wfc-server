package api

import (
	"net/http"
	"wwfc/gpcm"
	"wwfc/internal/assets"
	"wwfc/internal/racetelemetry"
)

func HandleRaces(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleModerator); err != nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	replyOK(w, struct {
		Available bool                 `json:"available"`
		Races     []racetelemetry.Race `json:"races"`
	}{Available: assets.RaceTelemetryProtocolVersion() == racetelemetry.ProtocolVersion, Races: gpcm.GetRaceTelemetry()})
}
