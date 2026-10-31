package api

import (
	"net/http"
	"sync"
	"wwfc/database"
	"wwfc/logging"
)

var preferencesMutex sync.Mutex

type PreferencesUpdateSpec struct {
	AuthInfo
	LastView       *string `json:"lastView"`
	RefreshSeconds *int    `json:"refreshSeconds"`
}

// HandlePreferences exposes a bounded set of non-secret panel preferences.
// Partial updates preserve other stored fields, including changes from tabs.
func HandlePreferences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if _, err := parseGet(r, w, RoleModerator); err != nil {
			return
		}
		preferences, err := db.GetHostManagerPreferences()
		if err != nil {
			logging.Error("API:admin", "Read panel preferences:", err)
			replyError(w, http.StatusInternalServerError, APIErrorInternal)
			return
		}
		replyOK(w, preferences)
		return
	}

	var request PreferencesUpdateSpec
	if err := parsePost(r, w, &request, RoleModerator); err != nil {
		return
	}
	if request.LastView == nil && request.RefreshSeconds == nil {
		replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
		return
	}
	preferencesMutex.Lock()
	defer preferencesMutex.Unlock()
	preferences, err := db.GetHostManagerPreferences()
	if err != nil {
		logging.Error("API:admin", "Read panel preferences for update:", err)
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}
	if request.LastView != nil {
		preferences.LastView = *request.LastView
	}
	if request.RefreshSeconds != nil {
		preferences.RefreshSeconds = *request.RefreshSeconds
	}
	if err := database.ValidateHostManagerPreferences(preferences); err != nil {
		replyError(w, http.StatusBadRequest, APIErrorInvalidQuery)
		return
	}
	if err := db.SetHostManagerPreferences(preferences); err != nil {
		logging.Error("API:admin", "Save panel preferences:", err)
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}
	replyOK(w, preferences)
}
