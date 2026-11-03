package gpcm

import (
	"encoding/binary"
	"wwfc/common"
	"wwfc/logging"
)

// These reports observe native client decisions; they never kick a player,
// excuse inactivity or change the committed CPU/Knockout plan.
func (g *GameSpySession) handleVSWatchdog(key, value string) {
	mutex.Lock()
	allowed := g.LoggedIn && g.DeviceAuthenticated && g.GameName == "mariokartwii" && g.CPUVersion == 3 && sessions[g.User.ProfileId] == g
	pid := g.User.ProfileId
	mutex.Unlock()
	if !allowed || len(value) != common.Base64DwcEncoding.EncodedLen(32) {
		return
	}
	data, err := common.Base64DwcEncoding.DecodeString(value)
	if err != nil || len(data) != 32 || data[0] != 1 || data[13] > 1 {
		return
	}
	reasons := map[string]byte{"wl:vs_idle": 1, "wl:vs_low": 2, "wl:vs_progress": 3}
	if reasons[key] == 0 || data[1] != reasons[key] ||
		(data[2] >= 12 && data[2] != 0xff) || (data[3] >= 12 && data[3] != 0xff) ||
		(data[12] > 3 && data[12] != 0xff) {
		return
	}
	names := map[byte]string{1: "input-idle", 2: "low-speed", 3: "no-progress"}
	fields := map[string]any{
		"profile_id": pid, "reason": names[data[1]],
		"epoch": binary.BigEndian.Uint32(data[4:8]), "race_frame": binary.BigEndian.Uint32(data[8:12]),
		"local_racer_0": data[2], "local_racer_1": data[3], "controller": data[12],
		"native_result": data[13], "disconnect_before": data[14], "disconnect_after": data[15],
		"unchanged_input_frames": binary.BigEndian.Uint16(data[16:18]), "disconnected_controller_frames": binary.BigEndian.Uint16(data[18:20]),
		"local_0_warmup": binary.BigEndian.Uint16(data[20:22]), "local_0_low_speed": binary.BigEndian.Uint16(data[22:24]),
		"local_0_no_progress": binary.BigEndian.Uint16(data[24:26]), "local_1_warmup": binary.BigEndian.Uint16(data[26:28]),
		"local_1_low_speed": binary.BigEndian.Uint16(data[28:30]), "local_1_no_progress": binary.BigEndian.Uint16(data[30:32]),
	}
	logging.Notice(g.ModuleName, "Native VS watchdog:", fields)
	logging.Event("vs_watchdog", fields)
}
