package onlinecpu

// KnockoutStatus is an observational copy of accepted decisions. Race series
// use stable human profile IDs and CPU ordinals; the current native-ID mask is
// also available for Round Knockdown and accepted decisions in this course.
type KnockoutStatus struct {
	Plan
	EliminatedMask  uint16
	HumanPlacements map[uint32]uint8
	CPUPlacements   map[uint8]uint8
}

func (e *Engine) KnockoutStatusForRoom(name string, groupID uint32) (KnockoutStatus, bool) {
	state := e.rooms[name]
	if state == nil || state.room.GroupID != groupID || state.active == nil ||
		!state.active.Committed || !state.active.KnockoutEnabled {
		return KnockoutStatus{}, false
	}
	active := state.active
	status := KnockoutStatus{Plan: active.Plan, EliminatedMask: active.knockoutMask,
		HumanPlacements: map[uint32]uint8{}, CPUPlacements: map[uint8]uint8{}}
	if active.series != nil {
		status.SeriesState = active.series.state()
		for entrant, place := range active.series.placements {
			if entrant.kind == 0 {
				status.HumanPlacements[entrant.profile] = place
			} else {
				status.CPUPlacements[entrant.slot] = place
			}
		}
	}
	return status, true
}

// CommittedPlanForRoom exposes only the committed roster for this exact QR2
// group. Callers use their existing engine lock; observational telemetry must
// not borrow a plan from an earlier group with a reused display name.
func (e *Engine) CommittedPlanForRoom(name string, groupID uint32) (Plan, bool) {
	state := e.rooms[name]
	if state == nil || state.room.GroupID != groupID || state.active == nil || !state.active.Committed {
		return Plan{}, false
	}
	return state.active.Plan, true
}
