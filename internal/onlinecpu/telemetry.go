package onlinecpu

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
