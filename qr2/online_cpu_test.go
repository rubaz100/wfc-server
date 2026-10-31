package qr2

import "testing"

func TestOnlineCPURoomSnapshotIncludesPublicAndPrivateGroups(t *testing.T) {
	previous := logins
	t.Cleanup(func() { logins = previous })
	for _, matchType := range []string{"0", "1", "2", "3"} {
		t.Run(matchType, func(t *testing.T) {
			ownerLogin := &LoginInfo{ProfileID: 10, DeviceAuthenticated: true}
			guestLogin := &LoginInfo{ProfileID: 20, DeviceAuthenticated: true}
			owner := &Session{Authenticated: true, login: ownerLogin, Data: map[string]string{"dwc_pid": "10", "+localplayers": "1"}}
			guest := &Session{Authenticated: true, login: guestLogin, Data: map[string]string{"dwc_pid": "20", "+localplayers": "2"}}
			group := &Group{GroupName: "test", GroupID: 99, GameName: "mariokartwii", MatchType: matchType, server: owner, players: map[*Session]bool{owner: true, guest: true}}
			owner.groupPointer, guest.groupPointer = group, group
			ownerLogin.session, guestLogin.session = owner, guest
			logins = map[uint32]*LoginInfo{10: ownerLogin, 20: guestLogin}
			room, ok := OnlineCPURoomForProfile(20)
			if !ok || room.OwnerPID != 10 || room.GroupID != 99 || len(room.Members) != 2 {
				t.Fatalf("match type %s snapshot = %+v, valid:%v", matchType, room, ok)
			}
			count := 0
			for _, member := range room.Members {
				count += int(member.LocalPlayers)
			}
			if count != 3 {
				t.Fatalf("human count = %d, want 3", count)
			}
			guest.Data["+localplayers"] = "0"
			if _, ok := OnlineCPURoomForProfile(20); ok {
				t.Fatal("invalid local-player count was accepted")
			}
			guest.Data["+localplayers"] = "1"
			guest.Authenticated = false
			if _, ok := OnlineCPURoomForProfile(10); ok {
				t.Fatal("unauthenticated member was accepted")
			}
		})
	}
}
