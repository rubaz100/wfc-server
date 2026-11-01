package qr2

import (
	"strconv"
	"wwfc/internal/racetelemetry"
)

// raceTelemetryRoom copies only authenticated identity/name data under QR2's
// mutex. The caller must already hold it; no telemetry or GPCM lock is taken.
func raceTelemetryRoom(group *Group) (racetelemetry.Room, bool) {
	if group == nil || group.GameName != "mariokartwii" || group.server == nil || group.server.login == nil || !group.server.Authenticated || !group.server.login.DeviceAuthenticated {
		return racetelemetry.Room{}, false
	}
	owner, err := strconv.ParseUint(group.server.Data["dwc_pid"], 10, 32)
	if err != nil || owner == 0 || uint32(owner) != group.server.login.ProfileID {
		return racetelemetry.Room{}, false
	}
	room := racetelemetry.Room{Name: group.GroupName, GroupID: group.GroupID, Created: group.CreateTime, OwnerPID: uint32(owner), Members: make(map[uint32]racetelemetry.Member)}
	for session := range group.players {
		if session.login == nil || !session.Authenticated || !session.login.DeviceAuthenticated {
			continue
		}
		count, err := strconv.Atoi(session.Data["+localplayers"])
		if err != nil || count < 1 || count > 2 {
			continue
		}
		member := racetelemetry.Member{LocalPlayers: count, Names: make([]string, count)}
		for i := range member.Names {
			member.Names[i] = session.Data["+mii_name"+strconv.Itoa(i)]
			if member.Names[i] == "" && i == 0 {
				member.Names[i] = session.login.InGameName
			}
		}
		room.Members[session.login.ProfileID] = member
	}
	_, ownerPresent := room.Members[room.OwnerPID]
	return room, ownerPresent
}

func RaceTelemetryRoomForProfile(profileID uint32) (racetelemetry.Room, bool) {
	mutex.Lock()
	defer mutex.Unlock()
	login := logins[profileID]
	if login == nil || !login.DeviceAuthenticated || login.session == nil || !login.session.Authenticated {
		return racetelemetry.Room{}, false
	}
	return raceTelemetryRoom(login.session.groupPointer)
}

func GetRaceTelemetryRooms() []racetelemetry.Room {
	mutex.Lock()
	defer mutex.Unlock()
	rooms := make([]racetelemetry.Room, 0, len(groups))
	for _, group := range groups {
		if room, ok := raceTelemetryRoom(group); ok {
			rooms = append(rooms, room)
		}
	}
	return rooms
}
