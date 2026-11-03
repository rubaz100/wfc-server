package gpcm

import (
	"strconv"
	"wwfc/common"
	"wwfc/logging"
)

// This policy is independent of CPU rosters and Knockout. Old payloads do not
// advertise the capability, and retain their native watchdog behavior.
func (g *GameSpySession) acceptInactivityCapability(command common.GameSpyCommand) {
	if !g.LoggedIn || !g.DeviceAuthenticated || g.GameName != "mariokartwii" || command.OtherValues["wl:afk_ver"] != "1" {
		return
	}
	mutex.Lock()
	if sessions[g.User.ProfileId] != g {
		mutex.Unlock()
		return
	}
	g.InactivityVersion = 1
	pid := g.User.ProfileId
	mutex.Unlock()
	sendInactivitySettings(pid)
}

func sendInactivitySettings(pid uint32) {
	disabled := common.GetConfig().DisableInactivityDisconnect
	mutex.Lock()
	session := sessions[pid]
	if session == nil || !session.LoggedIn || !session.DeviceAuthenticated || session.GameName != "mariokartwii" || session.InactivityVersion != 1 {
		mutex.Unlock()
		return
	}
	index := session.ConnIndex
	mutex.Unlock()
	value := "0"
	if disabled {
		value = "1"
	}
	packet := common.CreateGameSpyMessage(common.GameSpyCommand{Command: "bm", CommandValue: "1", OtherValues: map[string]string{
		"f": strconv.FormatUint(uint64(pid), 10), "msg": "LAFK1:S:" + value,
	}})
	if err := common.SendPacket(ServerName, index, []byte(packet)); err != nil {
		logging.Warn("GPCM:inactivity", "Policy delivery failed:", err)
	}
	evValue := "0"
	if common.GetConfig().ExcludeEliminatedVotes {
		evValue = "1"
	}
	evPacket := common.CreateGameSpyMessage(common.GameSpyCommand{Command: "bm", CommandValue: "1", OtherValues: map[string]string{
		"f": strconv.FormatUint(uint64(pid), 10), "msg": "LEV1:S:" + evValue,
	}})
	if err := common.SendPacket(ServerName, index, []byte(evPacket)); err != nil {
		logging.Warn("GPCM:inactivity", "Voting policy delivery failed:", err)
	}
}

// Runtime changes apply to authenticated capable clients immediately, even
// when no CPU/Knockout plan exists. New sessions receive the saved policy.
func BroadcastInactivitySettings() {
	mutex.Lock()
	var recipients []uint32
	for pid, session := range sessions {
		if session.LoggedIn && session.DeviceAuthenticated && session.GameName == "mariokartwii" && session.InactivityVersion == 1 {
			recipients = append(recipients, pid)
		}
	}
	mutex.Unlock()
	for _, pid := range recipients {
		sendInactivitySettings(pid)
	}
}
