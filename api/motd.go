package api

import (
	"net/http"
	"wwfc/gpcm"
	"wwfc/logging"
)

type MOTDResponseSpec struct {
	Text string `json:"text"`
}

type MOTDRequestSpec struct {
	AuthInfo
	Text string `json:"text"`
}

// HandleMOTD reads (GET) or writes (POST) the message of the day. Requires the secret.
func HandleMOTD(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleSetMOTD(w, r)
		return
	}
	handleGetMOTD(w, r)
}

func handleGetMOTD(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleModerator); err != nil {
		return
	}

	text, _ := gpcm.GetMessageOfTheDay()
	replyOK(w, MOTDResponseSpec{Text: text})
}

func handleSetMOTD(w http.ResponseWriter, r *http.Request) {
	var req MOTDRequestSpec
	if err := parsePost(r, w, &req, RoleModerator); err != nil {
		return
	}

	if err := gpcm.SetMessageOfTheDay(req.Text); err != nil {
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}

	replyOK(w, nil)
	logging.Notice("API:admin", "Message of the day updated")
}
