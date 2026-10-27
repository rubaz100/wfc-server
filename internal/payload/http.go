package payload

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (c *Catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/payload":
		c.servePayload(w, r)
	case "/payload/deployment":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := json.Marshal(c.Deployment())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	default:
		if r.Method == http.MethodGet && len(r.URL.Path) == 3 && strings.HasPrefix(r.URL.Path, "/w") {
			version, err := strconv.Atoi(r.URL.Path[2:])
			if err == nil && version >= 0 && version <= 9 {
				body, exists := c.Stage1(version)
				if exists {
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					_, _ = w.Write(body)
					return
				}
			}
		}
		http.NotFound(w, r)
	}
}

func (c *Catalog) servePayload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || len(r.URL.RequestURI()) > 4096 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	query, err := parseSingleQuery(r.URL.Query())
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	game, ok := query["g"]
	if !ok || !validGameID(game) {
		http.Error(w, "invalid game ID", http.StatusBadRequest)
		return
	}
	channel := "prod"
	if value, exists := query["c"]; exists {
		channel = value
	}
	versions, exists := c.deployment[channel]
	if !exists {
		http.NotFound(w, r)
		return
	}
	version := 0xff
	if value, exists := query["v"]; exists {
		parsed, err := strconv.ParseUint(value, 16, 8)
		if err != nil || len(value) != 2 {
			http.Error(w, "invalid version", http.StatusBadRequest)
			return
		}
		version = int(parsed)
	}
	if query["k"] != "" && query["k"] != "0" && query["k"] != "1" {
		http.Error(w, "invalid key ID", http.StatusBadRequest)
		return
	}
	if _, hasChannel := query["c"]; !hasChannel {
		if !validConsoleParameters(query) {
			http.Error(w, "invalid console parameters", http.StatusBadRequest)
			return
		}
	} else if _, hasVersion := query["v"]; !hasVersion {
		if !validConsoleParameters(query) {
			http.Error(w, "invalid console parameters", http.StatusBadRequest)
			return
		}
	}
	selected := selectVersion(versions, version)
	if selected == "" {
		http.NotFound(w, r)
		return
	}
	template, exists := c.stage2[game]
	if !exists {
		http.NotFound(w, r)
		return
	}
	body := append([]byte(nil), template...)
	salt, salted := query["s"]
	if salted {
		providedHash, hashExists := query["h"]
		if !hashExists || len(salt) != 64 || len(providedHash) != 8 {
			http.Error(w, "invalid salt", http.StatusBadRequest)
			return
		}
		if _, err := hex.DecodeString(salt); err != nil {
			http.Error(w, "invalid salt", http.StatusBadRequest)
			return
		}
		expectedHash, err := hex.DecodeString(providedHash)
		if err != nil {
			http.Error(w, "invalid salt hash", http.StatusBadRequest)
			return
		}
		raw := r.URL.RawQuery
		suffix := "&h=" + providedHash
		if !strings.HasSuffix(raw, suffix) {
			http.Error(w, "salt hash must be last", http.StatusBadRequest)
			return
		}
		signedQuery := "payload?" + strings.TrimSuffix(raw, suffix)
		saltHash := sha256.Sum256([]byte(signedQuery))
		if !bytes.Equal(expectedHash, saltHash[:4]) {
			http.Error(w, "salt hash mismatch", http.StatusBadRequest)
			return
		}
		copy(body[0x110:0x130], saltHash[:])
		digest := sha256.Sum256(body[0x110:])
		signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
		if err != nil {
			http.Error(w, "signature failed", http.StatusInternalServerError)
			return
		}
		body = append(append(append([]byte(nil), body[:0x10]...), signature...), body[0x110:]...)
	} else if _, exists := query["h"]; exists {
		http.Error(w, "salt hash without salt", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func parseSingleQuery(query map[string][]string) (map[string]string, error) {
	result := map[string]string{}
	for key, values := range query {
		switch key {
		case "c", "v", "d", "f", "k", "g", "s", "h":
		default:
			return nil, fmt.Errorf("unknown query parameter %q", key)
		}
		if len(values) != 1 {
			return nil, fmt.Errorf("duplicate query parameter %q", key)
		}
		if (key == "c" || key == "g") && values[0] == "" {
			return nil, fmt.Errorf("empty query parameter %q", key)
		}
		result[key] = values[0]
	}
	return result, nil
}

func validGameID(game string) bool {
	if len(game) != 7 && len(game) != 9 {
		return false
	}
	if (len(game) == 7 && game[4] != 'D') || (len(game) == 9 && game[4] != 'N') {
		return false
	}
	for i := 0; i < 4; i++ {
		if (game[i] < 'A' || game[i] > 'Z') && (game[i] < '0' || game[i] > '9') {
			return false
		}
	}
	for i := 5; i < len(game); i++ {
		if (game[i] < '0' || game[i] > '9') && (game[i] < 'a' || game[i] > 'f') {
			return false
		}
	}
	return true
}

func validConsoleParameters(query map[string]string) bool {
	if value, exists := query["d"]; exists && value != "" && value != "0" {
		if len(value) != 8 {
			return false
		}
		if _, err := strconv.ParseUint(value, 16, 32); err != nil {
			return false
		}
		return true
	}
	if value, exists := query["f"]; exists && value != "" && value != "0" {
		if len(value) != 14 {
			return false
		}
		if _, err := strconv.ParseUint(value, 16, 64); err != nil {
			return false
		}
	}
	return true
}

func selectVersion(versions []string, requested int) string {
	selected := ""
	selectedMajor := 0
	for _, version := range versions {
		major, err := strconv.Atoi(strings.Split(version, ".")[0])
		if err != nil || major < 0 || major > 255 {
			return ""
		}
		if strings.HasPrefix(version, fmt.Sprintf("%d.", requested)) {
			return version
		}
		if selected == "" || (major > requested && major < selectedMajor) || (major < requested && major > selectedMajor) {
			selected, selectedMajor = version, major
		}
	}
	return selected
}
