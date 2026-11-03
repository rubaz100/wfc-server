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
	return onlineCPURoom(group)
}

// OnlineCPURoomByIdentity distinguishes a closed room from one temporarily
// lacking an authenticated host/member snapshot. Series ranks must survive the
// latter while the remaining QR2 members keep the same physical room alive.
func OnlineCPURoomByIdentity(name string, groupID uint32) (room onlinecpu.Room, ready bool, exists bool) {
	mutex.Lock()
	defer mutex.Unlock()
	group := groups[name]
	if group == nil || group.GroupID != groupID || len(group.players) == 0 {
		return onlinecpu.Room{}, false, false
	}
	room, ready = onlineCPURoom(group)
	return room, ready, true
}

// Expects the QR2 mutex to be held.
func onlineCPURoom(group *Group) (onlinecpu.Room, bool) {
	if group.server == nil || group.GameName != "mariokartwii" {
		return onlinecpu.Room{}, false
	}
	owner, err := strconv.ParseUint(group.server.Data["dwc_pid"], 10, 32)
	if err != nil || owner == 0 {
		return onlinecpu.Room{}, false
	}
	room := onlinecpu.Room{Name: group.GroupName, GroupID: group.GroupID, OwnerPID: uint32(owner), KnockdownPool: group.knockdownPool(), Private: group.MatchType == "2" || group.MatchType == "3"}
	for session := range group.players {
		if session.login == nil || !session.Authenticated || !session.login.DeviceAuthenticated {
			return onlinecpu.Room{}, false
		}
		count, err := strconv.ParseUint(session.Data["+localplayers"], 10, 8)
		if err != nil || count < 1 || count > 2 {
			return onlinecpu.Room{}, false
		}
		room.Members = append(room.Members, onlinecpu.Member{ProfileID: session.login.ProfileID, LocalPlayers: uint8(count), KnockdownCompatible: knockdownCompatible(session)})
	}
	return room, true
}
