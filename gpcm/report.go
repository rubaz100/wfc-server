package gpcm

import (
	"strconv"
	"wwfc/common"
	"wwfc/logging"
	"wwfc/qr2"

	"github.com/logrusorgru/aurora/v3"
)

func (g *GameSpySession) handleWWFCReport(command common.GameSpyCommand) {
	for key, value := range command.OtherValues {
		switch key {
		case "wl:vs_idle", "wl:vs_low", "wl:vs_progress":
			g.handleVSWatchdog(key, value)
			continue
		case "wl:penalty_ack":
			g.handlePenaltyResetReport(value)
			continue
		case "wl:race_live", "wl:race_map":
			g.handleRaceTelemetryReport(key, value)
			continue
		case "wl:cpu_prepare", "wl:cpu_ack", "wl:cpu_plan", "wl:cpu_frame", "wl:cpu_item", "wl:cpu_ping", "wl:cpu_grid_error", "wl:ko_event", "wl:ko_course":
			g.handleOnlineCPUReport(key, value)
			continue
		}
		logging.Info(g.ModuleName, "WiiLink Report:", aurora.Yellow(key))

		keyColored := aurora.BrightCyan(key).String()

		switch key {
		default:
			logging.Error(g.ModuleName, "Unknown record", aurora.Cyan(key).String()+":", aurora.Cyan(value))

		case "wl:speed":
			// Existing report-first speedometer: hundredths of native speed.
			// It is diagnostic data, not a server error or authoritative input.
			speed, err := strconv.ParseUint(value, 10, 32)
			if g.GameName == "mariokartwii" && err == nil && speed <= 100000 {
				logging.Info(g.ModuleName, "Kart speed x100:", speed)
			}

		case "wl:ko_pos", "wl:ko_eliminate", "wl:ko_apply", "wl:ko_spectate", "wl:ko_result":
			// Informational client diagnostics; authoritative decisions use wl:ko_event.

		case "wl:ko_afk":
			// Diagnostic only: the native client suppressed its own AFK kick
			// in a negotiated Knockout race. This never changes server policy.
			slot, err := strconv.ParseUint(value, 10, 8)
			if g.GameName != "mariokartwii" || err != nil || slot > 3 {
				continue
			}
			logging.Notice(g.ModuleName, "Knockout retained idle local input slot:", aurora.Cyan(slot))

		case "wl:ko_stall_guard":
			// Informational only: an eliminated local spectator bypassed both
			// motion watchdogs. This does not mean either timeout had expired.
			record, err := strconv.ParseUint(value, 10, 16)
			if g.GameName != "mariokartwii" || err != nil || record>>8 != 3 || record&0xff >= 12 {
				continue
			}
			logging.Notice(g.ModuleName, "Knockout spectator motion watchdogs bypassed for racer:", aurora.Cyan(record&0xff))

		case "wl:bad_packet":
			profileId, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				logging.Error(g.ModuleName, "Error decoding", keyColored+":", err.Error())
				continue
			}

			logging.Warn(g.ModuleName, "Report bad packet from", aurora.BrightCyan(strconv.FormatUint(profileId, 10)))
			logging.Event("reported_bad_packet", map[string]any{
				"profile_id": g.User.ProfileId,
				"sender_id":  profileId,
			})

		case "wl:stall":
			profileId, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				logging.Error(g.ModuleName, "Error decoding", keyColored+":", err.Error())
				continue
			}

			logging.Warn(g.ModuleName, "Room stall caused by", aurora.BrightCyan(strconv.FormatUint(profileId, 10)))
			logging.Event("reported_stall", map[string]any{
				"profile_id":  g.User.ProfileId,
				"stalling_id": profileId,
			})

		case "wl:mkw_user":
			if g.GameName != "mariokartwii" {
				logging.Warn(g.ModuleName, "Ignoring", keyColored+":", "from wrong game")
				continue
			}

			packet, err := common.Base64DwcEncoding.DecodeString(value)
			if err != nil {
				logging.Error(g.ModuleName, "Error decoding", keyColored+":", err.Error())
				continue
			}

			if len(packet) != 0xC0 {
				logging.Error(g.ModuleName, "Invalid", keyColored, "record length:", len(packet))
				continue
			}

			qr2.ProcessUSER(g.User.ProfileId, g.QR2IP, packet)

		case "wl:mkw_select_course", "wl:mkw_select_cc":
			if g.GameName != "mariokartwii" {
				logging.Warn(g.ModuleName, "Ignoring", keyColored, "from wrong game")
				continue
			}

			qr2.ProcessMKWSelectRecord(g.User.ProfileId, key, value)
		}
	}
}
