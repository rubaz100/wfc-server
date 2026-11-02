package qr2

import "wwfc/common"

// Group identity stays frozen while clients race or follow a friend. A fresh
// public search uses its advertised region only after leaving the old group.
func (session *Session) knockdownPool() bool {
	if session == nil || session.Data["gamename"] != "mariokartwii" {
		return false
	}
	if group := session.groupPointer; group != nil {
		return group.knockdownPool()
	}
	return common.IsPublicMKWMatchType(session.Data["dwc_mtype"]) && session.Data["rk"] == common.KnockdownMatchRegion
}

func (group *Group) knockdownPool() bool {
	return group != nil && group.GameName == "mariokartwii" && common.IsPublicMKWMatchType(group.MatchType) && group.MKWRegion == common.KnockdownMatchRegion
}

func knockdownCompatible(session *Session) bool {
	return session != nil && session.Authenticated && session.login != nil && session.login.DeviceAuthenticated && session.login.KnockdownPoolVersion == common.KnockdownPoolProtocolVersion
}

// The requester count is authenticated match-command data. The first host's
// local count is not known until RESV_OK and is checked again at that point.
func checkMatchPoolAllowed(sender, destination *Session, localPlayers uint32) string {
	if sender.knockdownPool() != destination.knockdownPool() {
		return "match_pool_mismatch"
	}
	if !destination.knockdownPool() {
		return "ok"
	}
	if !knockdownCompatible(sender) || !knockdownCompatible(destination) {
		return "knockdown_incompatible"
	}
	if localPlayers != 1 {
		return "knockdown_local_players"
	}
	if group := destination.groupPointer; group != nil {
		for member := range group.players {
			if !knockdownCompatible(member) || member.Data["+localplayers"] != "1" {
				return "knockdown_incompatible"
			}
		}
	}
	return "ok"
}
