package gpcm

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"wwfc/common"
	"wwfc/logging"
)

// A reset is a one-shot request to the selected license, not a server-wide
// penalty exemption. Keep it durable until the client confirms a native save.
type PenaltyResetInfo struct {
	State     string    `json:"state"`
	RequestID uint32    `json:"request"`
	Updated   time.Time `json:"updated"`
	Result    uint32    `json:"result"`
	Supported bool      `json:"supported"`
}

var penaltyMutex sync.Mutex

func penaltyKey(pid uint32) string { return fmt.Sprintf("penalty_reset:%d", pid) }

func readPenaltyReset(pid uint32) (PenaltyResetInfo, error) {
	value, exists, err := db.GetAdminConfig(penaltyKey(pid))
	if err != nil {
		return PenaltyResetInfo{}, err
	}
	info := PenaltyResetInfo{State: "none"}
	if exists {
		if err := json.Unmarshal([]byte(value), &info); err != nil {
			return PenaltyResetInfo{}, err
		}
	}
	return info, nil
}

func writePenaltyReset(pid uint32, info PenaltyResetInfo) error {
	info.Supported = false // Capability belongs to the live session only.
	value, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return db.SetAdminConfig(penaltyKey(pid), string(value))
}

func GetPenaltyReset(pid uint32) (PenaltyResetInfo, error) {
	penaltyMutex.Lock()
	info, err := readPenaltyReset(pid)
	penaltyMutex.Unlock()
	mutex.Lock()
	if session := sessions[pid]; session != nil {
		info.Supported = session.LoggedIn && session.DeviceAuthenticated && session.PenaltyResetVersion == 1
	}
	mutex.Unlock()
	return info, err
}

func QueuePenaltyReset(pid uint32) (PenaltyResetInfo, error) {
	penaltyMutex.Lock()
	info, err := readPenaltyReset(pid)
	if err == nil && info.State != "queued" {
		var nonce [4]byte
		if _, err = rand.Read(nonce[:]); err == nil {
			request := binary.BigEndian.Uint32(nonce[:])
			if request == 0 || request == info.RequestID {
				request = info.RequestID + 1
				if request == 0 {
					request = 1
				}
			}
			info = PenaltyResetInfo{State: "queued", RequestID: request, Updated: time.Now().UTC()}
			err = writePenaltyReset(pid, info)
		}
	}
	penaltyMutex.Unlock()
	if err != nil {
		return PenaltyResetInfo{}, err
	}
	sendPenaltyReset(pid)
	return GetPenaltyReset(pid)
}

func (g *GameSpySession) acceptPenaltyResetCapability(command common.GameSpyCommand) {
	if !g.LoggedIn || !g.DeviceAuthenticated || g.GameName != "mariokartwii" || command.OtherValues["wl:penalty_ver"] != "1" {
		return
	}
	mutex.Lock()
	if sessions[g.User.ProfileId] != g {
		mutex.Unlock()
		return
	}
	g.PenaltyResetVersion = 1
	pid := g.User.ProfileId
	mutex.Unlock()
	sendPenaltyReset(pid)
}

func sendPenaltyReset(pid uint32) {
	info, err := GetPenaltyReset(pid)
	if err != nil {
		logging.Error("GPCM:penalty", "Read reset request:", err)
		return
	}
	if info.State != "queued" || info.RequestID == 0 || !info.Supported {
		return
	}
	mutex.Lock()
	session := sessions[pid]
	if session == nil || !session.LoggedIn || !session.DeviceAuthenticated || session.PenaltyResetVersion != 1 {
		mutex.Unlock()
		return
	}
	index := session.ConnIndex
	mutex.Unlock()
	packet := common.CreateGameSpyMessage(common.GameSpyCommand{Command: "bm", CommandValue: "1", OtherValues: map[string]string{
		"f": strconv.FormatUint(uint64(pid), 10), "msg": fmt.Sprintf("LPR1:C:%d:%d", info.RequestID, pid),
	}})
	if err := common.SendPacket(ServerName, index, []byte(packet)); err != nil {
		logging.Warn("GPCM:penalty", "Reset delivery remains queued:", err)
	}
}

func (g *GameSpySession) handlePenaltyResetReport(value string) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return
	}
	request, err := strconv.ParseUint(parts[0], 10, 32)
	result, resultErr := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || resultErr != nil || request == 0 || result > 3 {
		return
	}
	mutex.Lock()
	pid := g.User.ProfileId
	allowed := g.LoggedIn && g.DeviceAuthenticated && g.GameName == "mariokartwii" && g.PenaltyResetVersion == 1 && sessions[pid] == g
	mutex.Unlock()
	if !allowed {
		return
	}
	penaltyMutex.Lock()
	defer penaltyMutex.Unlock()
	info, err := readPenaltyReset(pid)
	if err != nil || info.State != "queued" || info.RequestID != uint32(request) {
		return
	}
	info.State = "failed"
	if result == 0 {
		info.State = "cleared"
	}
	info.Result, info.Updated = uint32(result), time.Now().UTC()
	if err := writePenaltyReset(pid, info); err != nil {
		logging.Error("GPCM:penalty", "Store reset acknowledgement:", err)
		return
	}
	logging.Notice("GPCM:penalty", "Penalty reset", info.State, "for profile", pid, "result", result)
}
