package database

import (
	"encoding/json"
	"fmt"
)

const hostManagerPreferencesKey = "host_manager_preferences_v1"

// HostManagerPreferences contains display behavior only. Authentication,
// server addresses and gameplay configuration are never stored in this blob.
// The single-admin Host Manager shares these preferences across browsers.
type HostManagerPreferences struct {
	LastView       string `json:"lastView"`
	RefreshSeconds int    `json:"refreshSeconds"`
}

func DefaultHostManagerPreferences() HostManagerPreferences {
	return HostManagerPreferences{LastView: "dashboard", RefreshSeconds: 3}
}

func ValidateHostManagerPreferences(preferences HostManagerPreferences) error {
	switch preferences.LastView {
	case "dashboard", "rooms", "races", "players", "bans", "settings":
	default:
		return fmt.Errorf("invalid Host Manager view")
	}
	switch preferences.RefreshSeconds {
	case 0, 3, 5, 10, 30, 60:
	default:
		return fmt.Errorf("invalid Host Manager refresh interval")
	}
	return nil
}

// EnsureHostManagerPreferences initializes a separate preference key without
// replacing the password hash or any preferences saved by an earlier process.
func (c *Connection) EnsureHostManagerPreferences() error {
	encoded, err := json.Marshal(DefaultHostManagerPreferences())
	if err != nil {
		return err
	}
	_, err = c.SetAdminConfigIfAbsent(hostManagerPreferencesKey, string(encoded))
	return err
}

func (c *Connection) GetHostManagerPreferences() (HostManagerPreferences, error) {
	preferences := DefaultHostManagerPreferences()
	encoded, exists, err := c.GetAdminConfig(hostManagerPreferencesKey)
	if err != nil || !exists {
		return preferences, err
	}
	if len(encoded) > 512 {
		return HostManagerPreferences{}, fmt.Errorf("stored Host Manager preferences exceed the size limit")
	}
	if err := json.Unmarshal([]byte(encoded), &preferences); err != nil {
		return HostManagerPreferences{}, fmt.Errorf("decode Host Manager preferences: %w", err)
	}
	if err := ValidateHostManagerPreferences(preferences); err != nil {
		return HostManagerPreferences{}, err
	}
	return preferences, nil
}

func (c *Connection) SetHostManagerPreferences(preferences HostManagerPreferences) error {
	if err := ValidateHostManagerPreferences(preferences); err != nil {
		return err
	}
	encoded, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	return c.SetAdminConfig(hostManagerPreferencesKey, string(encoded))
}
