package api

import (
	"net/http"
	"strconv"
	"wwfc/gpcm"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

// ProfileUpdateSpec is the POST body for editing a stored account. Optional
// fields are pointers so an omitted key leaves that field unchanged.
type ProfileUpdateSpec struct {
	AuthInfo
	ProfileID uint32  `json:"pid"`
	FirstName *string `json:"firstname"`
	LastName  *string `json:"lastname"`
	OpenHost  *bool   `json:"open_host"`
	// Message is the per-player connect message (shown as wl:motd next time the
	// player connects). Empty clears it; nil (omitted) leaves it unchanged.
	Message *string `json:"message"`
}

// HandleProfile serves GET /api/profile?pid=N (read one account) and
// POST /api/profile (edit first/last name and the open-host flag).
func HandleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleProfileUpdate(w, r)
		return
	}
	handleProfileGet(w, r)
}

func handleProfileGet(w http.ResponseWriter, r *http.Request) {
	query, err := parseGet(r, w, RoleModerator)
	if err != nil {
		return
	}

	profileID, err := strconv.ParseUint(query.Get("pid"), 10, 32)
	if err != nil || profileID == 0 {
		replyError(w, http.StatusBadRequest, APIErrorInvalidProfileID)
		return
	}

	info, ok := gpcm.GetProfileInfo(uint32(profileID))
	if !ok {
		replyError(w, http.StatusNotFound, APIErrorInvalidProfileID)
		return
	}

	replyOK(w, info)
}

func handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	var req ProfileUpdateSpec
	if err := parsePost(r, w, &req, RoleModerator); err != nil {
		return
	}

	if req.ProfileID == 0 {
		replyError(w, http.StatusBadRequest, APIErrorInvalidProfileID)
		return
	}

	// Cap the per-player connect message to keep the login reply small.
	if req.Message != nil {
		if runes := []rune(*req.Message); len(runes) > 512 {
			trimmed := string(runes[:512])
			req.Message = &trimmed
		}
	}

	if !gpcm.UpdateProfileInfo(req.ProfileID, req.FirstName, req.LastName, req.OpenHost, req.Message) {
		replyError(w, http.StatusNotFound, APIErrorInvalidProfileID)
		return
	}

	replyOK(w, nil)

	logging.Notice("API:admin", "Profile edit:", aurora.Cyan(req.ProfileID))
}
