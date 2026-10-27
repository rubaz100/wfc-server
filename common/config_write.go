package common

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"wwfc/logging"
)

// editableConfigTags is the allowlist of config.xml fields the admin panel may
// change. Anything not listed here is rejected before any change is applied.
var editableConfigTags = map[string]bool{
	"serverName":                  true,
	"allowDefaultDolphinKeys":     true,
	"allowMultipleDeviceIDs":      true,
	"allowConnectWithoutDeviceID": true,
	"enableHttpsExploitWii":       true,
	"enableHttpsExploitDS":        true,
	"logLevel":                    true,
}

var xmlTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// ApplyConfigChanges validates and persists the changes to config.xml before
// updating the in-memory config. Keys must be XML tag names from
// editableConfigTags; values are the raw (unescaped) string form
// ("true"/"false" for booleans, decimal for ints).
//
// In-memory fields read live via GetConfig() (e.g. allowMultipleDeviceIDs) take
// effect immediately. Fields cached by a server at startup (e.g. the HTTPS
// exploit listeners) are persisted here and apply on the next restart.
func ApplyConfigChanges(fields map[string]string) error {
	cmutex.Lock()
	defer cmutex.Unlock()

	updated := config
	for tag, val := range fields {
		if !editableConfigTags[tag] {
			return fmt.Errorf("field %q is not editable", tag)
		}

		switch tag {
		case "serverName":
			updated.ServerName = val
		case "allowDefaultDolphinKeys", "allowConnectWithoutDeviceID", "enableHttpsExploitWii", "enableHttpsExploitDS":
			if val != "true" && val != "false" {
				return fmt.Errorf("invalid %s: %q", tag, val)
			}
			b := val == "true"
			switch tag {
			case "allowDefaultDolphinKeys":
				updated.AllowDefaultDolphinKeys = b
			case "allowConnectWithoutDeviceID":
				updated.AllowConnectWithoutDeviceID = b
			case "enableHttpsExploitWii":
				updated.EnableHTTPSExploitWii = &b
			case "enableHttpsExploitDS":
				updated.EnableHTTPSExploitDS = &b
			}
		case "allowMultipleDeviceIDs":
			switch val {
			case "never", "always", "SameIPAddress":
				updated.AllowMultipleDeviceIDs = val
			default:
				return fmt.Errorf("invalid allowMultipleDeviceIDs: %q", val)
			}
		case "logLevel":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 4 {
				return fmt.Errorf("invalid logLevel: %q", val)
			}
			updated.LogLevel = &n
		}
	}

	if err := writeConfigFields(ConfigPath(), fields); err != nil {
		return err
	}

	config = updated
	if _, ok := fields["logLevel"]; ok {
		logging.SetLevel(*updated.LogLevel)
	}
	return nil
}

// writeConfigFields rewrites only the given tags in the XML file, preserving
// everything else (comments, secrets, formatting). Missing tags are inserted
// just before the closing </Config>.
func writeConfigFields(path string, fields map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	for tag, val := range fields {
		replacement := "<" + tag + ">" + xmlTextEscaper.Replace(val) + "</" + tag + ">"
		re := regexp.MustCompile(`(?s)<` + tag + `\b[^>]*>.*?</` + tag + `>`)

		if re.MatchString(content) {
			content = re.ReplaceAllLiteralString(content, replacement)
			continue
		}

		idx := strings.LastIndex(content, "</Config>")
		if idx == -1 {
			return fmt.Errorf("malformed config.xml: missing </Config>")
		}
		content = content[:idx] + "     " + replacement + "\n" + content[idx:]
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	// Write atomically: temp file then rename over the original.
	tmp := path + ".tmp"
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(content), info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
