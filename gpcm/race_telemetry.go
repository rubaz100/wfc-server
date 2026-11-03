package gpcm

import (
	"time"
	"wwfc/common"
	"wwfc/internal/assets"
	"wwfc/internal/racetelemetry"
	"wwfc/qr2"
)

var raceTelemetry = racetelemetry.New()

// telemetrySource accepts the current network host or the frozen CPU owner
// while that owner remains authenticated in this same group and committed plan.
func telemetrySource(room racetelemetry.Room, pid uint32, connection uint64) (racetelemetry.Source, bool) {
	source := racetelemetry.Source{Room: room, Reporter: pid, Connection: connection}
	_, member := room.Members[pid]
	if !member {
		return source, false
	}
	cpuRelay.Lock()
	plan, planned := cpuRelay.engine.CommittedPlanForRoom(room.Name, room.GroupID)
	cpuRelay.Unlock()
	if planned && plan.Committed {
		// Plan room identity is reconciled before allowing its frozen owner.
		cpuRoom, ok := qr2.OnlineCPURoomForProfile(plan.OwnerPID)
		if ok && cpuRoom.Name == room.Name && cpuRoom.GroupID == room.GroupID {
			source.PlanEpoch = plan.Epoch
			source.CPUStart = plan.HumanCount
			source.CPUCount = plan.CPUCount
			if plan.KnockoutEnabled {
				source.KnockoutMode = plan.KnockoutMode
				source.KnockoutEliminationCount = plan.KnockoutEliminationCount
				source.SeriesID = plan.SeriesID
				source.SeriesRound = plan.SeriesRound
			}
			if plan.CPUCount > 0 || plan.KnockoutEnabled {
				return source, pid == plan.OwnerPID
			}
		}
	}
	return source, pid == room.OwnerPID
}

func (g *GameSpySession) handleRaceTelemetryReport(key, value string) {
	if assets.RaceTelemetryProtocolVersion() != racetelemetry.ProtocolVersion {
		return
	}
	mutex.Lock()
	pid, connection := g.User.ProfileId, g.ConnIndex
	allowed := g.LoggedIn && g.DeviceAuthenticated && g.GameName == "mariokartwii" && sessions[pid] == g
	mutex.Unlock()
	if !allowed {
		return
	}
	limit, minimum := racetelemetry.MaxLiveSize, racetelemetry.LiveHeaderSize
	if key == "wl:race_map" {
		limit, minimum = racetelemetry.MaxMapSize, racetelemetry.MapHeaderSize
	}
	if len(value) > common.Base64DwcEncoding.EncodedLen(limit) || len(value) < common.Base64DwcEncoding.EncodedLen(minimum) {
		return
	}
	data, err := common.Base64DwcEncoding.DecodeString(value)
	if err != nil || len(data) > limit || len(data) < minimum {
		return
	}
	room, ok := qr2.RaceTelemetryRoomForProfile(pid)
	if !ok {
		return
	}
	source, ok := telemetrySource(room, pid, connection)
	if !ok {
		return
	}
	if key == "wl:race_live" {
		raceTelemetry.AcceptLive(source, data, time.Now())
	} else {
		raceTelemetry.AcceptMap(source, data, time.Now())
	}
}

// GetRaceTelemetry returns short-lived observational snapshots, never secrets
// or peer addresses. The API applies moderator authentication before calling.
func GetRaceTelemetry() []racetelemetry.Race {
	active := make(map[string]map[uint32]uint64)
	for _, room := range qr2.GetRaceTelemetryRooms() {
		owners := make(map[uint32]uint64)
		for pid := range room.Members {
			mutex.Lock()
			session := sessions[pid]
			connected := session != nil && session.LoggedIn && session.DeviceAuthenticated && session.GameName == "mariokartwii"
			var connection uint64
			if connected {
				connection = session.ConnIndex
			}
			mutex.Unlock()
			if !connected {
				continue
			}
			if _, ok := telemetrySource(room, pid, connection); ok {
				owners[pid] = connection
			}
		}
		active[room.Key()] = owners
	}
	races := raceTelemetry.Snapshot(time.Now(), active)
	for i := range races {
		race := &races[i]
		cpuRelay.Lock()
		status, ok := cpuRelay.engine.KnockoutStatusForRoom(race.RoomName, race.GroupID)
		cpuRelay.Unlock()
		if !ok || !race.KnockoutEnabled || race.PlanEpoch != status.Epoch ||
			len(race.Racers) != status.HumanCount+status.CPUCount || race.SeriesID != status.SeriesID {
			continue
		}
		// Apply accepted decisions to the detached observational snapshot. This
		// keeps elimination badges through delayed finish/position reports.
		race.SeriesState = status.SeriesState
		race.Remaining = 0
		last := -1
		for j := range race.Racers {
			racer := &race.Racers[j]
			var placement uint8
			if status.KnockoutMode == "race" {
				if racer.IsCPU && int(racer.ID) >= status.HumanCount {
					placement = status.CPUPlacements[racer.ID-uint8(status.HumanCount)]
				} else if !racer.IsCPU {
					placement = status.HumanPlacements[racer.ProfileID]
				}
			}
			if placement != 0 || status.EliminatedMask&(1<<racer.ID) != 0 {
				racer.Eliminated = true
			}
			if placement == 0xff {
				racer.Position = 0 // Joined as an unranked spectator.
			} else if placement != 0 && placement <= 12 {
				racer.Position = placement
			}
			racer.Winner = false
			if !racer.Eliminated && !racer.Disconnected {
				race.Remaining++
				last = j
			}
		}
		if race.Remaining == 1 && last >= 0 && (status.SeriesState == 2 ||
			(race.Racers[last].Finished && race.Racers[last].FinishMS > 0)) {
			race.Racers[last].Winner = true
		}
	}
	return races
}
