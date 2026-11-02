package api

import (
	"net/http"
	"strconv"
	"wwfc/gpcm"
	"wwfc/logging"
)

func HandlePenaltyReset(w http.ResponseWriter, r *http.Request) {
	var pid uint32
	if r.Method == http.MethodPost {
		var req struct {
			AuthInfo
			ProfileID uint32 `json:"pid"`
		}
		if err := parsePost(r, w, &req, RoleModerator); err != nil {
			return
		}
		pid = req.ProfileID
	} else {
		query, err := parseGet(r, w, RoleModerator)
		if err != nil {
			return
		}
		parsed, err := strconv.ParseUint(query.Get("pid"), 10, 32)
		if err != nil {
			replyError(w, http.StatusBadRequest, APIErrorInvalidProfileID)
			return
		}
		pid = uint32(parsed)
	}
	profile, exists := gpcm.GetProfileInfo(pid)
	if pid == 0 || !exists {
		replyError(w, http.StatusNotFound, APIErrorInvalidProfileID)
		return
	}
	if profile.GameCode != "RMCP" && profile.GameCode != "RMCE" && profile.GameCode != "RMCJ" && profile.GameCode != "RMCK" {
		replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
		return
	}
	info, err := gpcm.GetPenaltyReset(pid)
	if r.Method == http.MethodPost && err == nil {
		info, err = gpcm.QueuePenaltyReset(pid)
	}
	if err != nil {
		logging.Error("API:admin", "Penalty reset:", err)
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}
	replyOK(w, info)
}
