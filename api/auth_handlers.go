package api

import (
	"crypto/subtle"
	"net/http"
	"wwfc/logging"
)

const minPasswordLength = 8

// AuthStatus is the public auth state the panel reads before showing a gate.
type AuthStatus struct {
	PasswordSet bool `json:"password_set"`
}

// HandleAuthStatus (GET /api/auth/status) tells the panel whether to show the
// first-run "set a password" flow or the normal login. No auth required.
func HandleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := parseGet(r, w, RoleNone); err != nil {
		return
	}
	replyOK(w, AuthStatus{PasswordSet: IsAdminPasswordSet()})
}

type loginSpec struct {
	AuthInfo
	Password string `json:"password"`
}

// HandleLogin (POST /api/login) verifies the admin password and returns a
// session token the panel sends as its secret thereafter.
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginSpec
	if err := parsePost(r, w, &req, RoleNone); err != nil {
		return
	}
	if !IsAdminPasswordSet() {
		replyError(w, http.StatusConflict, APIErrorSetupRequired)
		return
	}
	if !CheckAdminPassword(req.Password) {
		replyError(w, http.StatusUnauthorized, APIErrorFailedAuthentication)
		return
	}
	replyOK(w, map[string]string{"token": issueToken()})
}

type setupSpec struct {
	AuthInfo
	Password string `json:"password"`
}

// HandleSetup (POST /api/setup) sets the admin password on first run. It only
// works while no password exists and requires the config.xml apiSecret as the
// bootstrap credential, so only the operator who installed the server can
// claim it. Returns a session token on success.
func HandleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupSpec
	if err := parsePost(r, w, &req, RoleNone); err != nil {
		return
	}
	if IsAdminPasswordSet() {
		replyError(w, http.StatusConflict, APIErrorSetupAlreadyDone)
		return
	}
	if len(apiSecret) < 16 || subtle.ConstantTimeCompare([]byte(req.Secret), []byte(apiSecret)) != 1 {
		replyError(w, http.StatusUnauthorized, APIErrorFailedAuthentication)
		return
	}
	if len(req.Password) < minPasswordLength {
		replyError(w, http.StatusBadRequest, APIErrorInvalidPassword)
		return
	}
	if err := SetAdminPassword(req.Password); err != nil {
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}
	logging.Notice("API:admin", "Admin password set (first-run setup)")
	replyOK(w, map[string]string{"token": issueToken()})
}

type passwordChangeSpec struct {
	AuthInfo
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// HandleChangePassword (POST /api/password) changes the admin password. The
// caller must already be authenticated (token/secret) and must also supply the
// current password once one is set.
func HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req passwordChangeSpec
	if err := parsePost(r, w, &req, RoleModerator); err != nil {
		return
	}
	if IsAdminPasswordSet() && !CheckAdminPassword(req.CurrentPassword) {
		replyError(w, http.StatusUnauthorized, APIErrorInvalidPassword)
		return
	}
	if len(req.NewPassword) < minPasswordLength {
		replyError(w, http.StatusBadRequest, APIErrorInvalidPassword)
		return
	}
	if err := SetAdminPassword(req.NewPassword); err != nil {
		replyError(w, http.StatusInternalServerError, APIErrorInternal)
		return
	}
	logging.Notice("API:admin", "Admin password changed")
	replyOK(w, nil)
}

// HandleLogout (POST /api/logout) revokes the caller's session token.
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	var req AuthInfo
	if err := parsePost(r, w, &req, RoleNone); err != nil {
		return
	}
	revokeToken(req.Secret)
	replyOK(w, nil)
}
