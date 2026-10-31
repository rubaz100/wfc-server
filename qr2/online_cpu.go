package qr2

import (
	"strconv"
	"wwfc/internal/onlinecpu"
)

// OnlineCPURoomForProfile returns authenticated members of public or private VS
// groups. Per-client capability is checked separately by the CPU coordinator.
// The copy lets CPU coordination run without holding QR2's shared mutex.
func OnlineCPURoomForProfile(profileID uint32) (onlinecpu.Room, bool) {
	mutex.Lock()
	defer mutex.Unlock()
	login := logins[profileID]
	if login == nil || login.session == nil || login.session.groupPointer == nil {
		return onlinecpu.Room{}, false
	}
	group := login.session.groupPointer
	if group.server == nil || group.GameName != "mariokartwii" {
		return onlinecpu.Room{}, false
	}
	owner, err := strconv.ParseUint(group.server.Data["dwc_pid"], 10, 32)
	if err != nil || owner == 0 {
		return onlinecpu.Room{}, false
	}
	room := onlinecpu.Room{Name: group.GroupName, GroupID: group.GroupID, OwnerPID: uint32(owner)}
	for session := range group.players {
		if session.login == nil || !session.Authenticated || !session.login.DeviceAuthenticated {
			return onlinecpu.Room{}, false
		}
		count, err := strconv.ParseUint(session.Data["+localplayers"], 10, 8)
		if err != nil || count < 1 || count > 2 {
			return onlinecpu.Room{}, false
		}
		room.Members = append(room.Members, onlinecpu.Member{ProfileID: session.login.ProfileID, LocalPlayers: uint8(count)})
	}
	return room, true
}
