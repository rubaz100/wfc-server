package api

import (
	"fmt"
	"net/http"
	"time"
	"wwfc/common"
)

type BanListEntry struct {
	ProfileID    uint32     `json:"pid"`
	FriendCode   string     `json:"fc,omitempty"`
	InGameName   string     `json:"name,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	ReasonHidden string     `json:"reason_hidden,omitempty"`
	Moderator    string     `json:"moderator,omitempty"`
	TOS          bool       `json:"tos"`
	Issued       *time.Time `json:"issued,omitempty"`
	Expires      *time.Time `json:"expires,omitempty"`
	DeviceIDs    []string   `json:"device_ids,omitempty"`
	LastIP       string     `json:"last_ip,omitempty"`
}

// HandleBans lists every user with an active ban. Requires the secret.
func HandleBans(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleModerator); err != nil {
		return
	}

	records, err := db.ListBans()
	if err != nil {
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}

	entries := make([]BanListEntry, 0, len(records))
	for _, rec := range records {
		gsbr := rec.GsbrCode
		if len(gsbr) > 4 {
			gsbr = gsbr[:4]
		}

		fc := ""
		if rec.ProfileID != 0 && len(gsbr) == 4 {
			fc = common.CalcFriendCodeString(rec.ProfileID, gsbr)
		}

		deviceIDs := make([]string, 0, len(rec.NgDeviceIds))
		for _, id := range rec.NgDeviceIds {
			if id == 0 {
				continue
			}
			deviceIDs = append(deviceIDs, fmt.Sprintf("%08x", id))
		}

		entries = append(entries, BanListEntry{
			ProfileID:    rec.ProfileID,
			FriendCode:   fc,
			InGameName:   rec.InGameName,
			Reason:       rec.Reason,
			ReasonHidden: rec.ReasonHidden,
			Moderator:    rec.Moderator,
			TOS:          rec.TOS,
			Issued:       rec.Issued,
			Expires:      rec.Expires,
			DeviceIDs:    deviceIDs,
			LastIP:       rec.LastIPAddress,
		})
	}

	if len(entries) == 0 {
		replyOK(w, "[]")
		return
	}

	replyOK(w, entries)
}
