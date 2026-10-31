// Package onlinecpu negotiates a frozen native CPU roster without doing I/O.
// Callers serialize access and deliver the returned messages after unlocking.
package onlinecpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	ProtocolVersion = 2
	FrameSize       = 112
	MaxPlayers      = 12
	LoadingGrace    = 90 * time.Second
	HostTimeout     = 5 * time.Second
)

type Policy struct {
	Enabled         bool
	Count           int
	AutoFill        bool
	KnockoutEnabled bool
}

type Member struct {
	ProfileID    uint32
	LocalPlayers uint8
}

type Room struct {
	Name     string
	GroupID  uint32
	OwnerPID uint32
	Members  []Member
}

type Delivery struct {
	To       uint32
	Message  string
	Frame    []byte
	Knockout []byte
}

type Plan struct {
	Epoch           uint32
	OwnerPID        uint32
	HumanCount      int
	CPUCount        int
	Seed            uint32
	Committed       bool
	KnockoutEnabled bool
}

type round struct {
	Plan
	members           []uint32
	acks              map[uint32]bool
	sequences         map[uint8]uint32
	created           time.Time
	lastAlive         time.Time
	knockoutSequence  uint32
	knockoutLap       uint16
	knockoutMask      uint16
	knockoutLastPlace uint8
	knockoutFinalBase uint32
	knockoutPlaces    uint16
}

type roomState struct {
	// The latest lobby membership is used for the next selection. The active
	// round keeps its own frozen participants while late arrivals wait.
	room      Room
	signature string
	prepared  map[uint32]uint32
	active    *round
}

type Engine struct {
	versions  map[uint32]uint32
	lastNonce map[uint32]uint32
	rooms     map[string]*roomState
	nextEpoch uint32
}

func New(initialEpoch uint32) *Engine {
	return &Engine{versions: map[uint32]uint32{}, lastNonce: map[uint32]uint32{}, rooms: map[string]*roomState{}, nextEpoch: initialEpoch}
}

func (e *Engine) Register(profileID, version uint32) {
	e.versions[profileID] = version
}

func (e *Engine) Plan(name string) (Plan, bool) {
	state := e.rooms[name]
	if state == nil || state.active == nil {
		return Plan{}, false
	}
	return state.active.Plan, true
}

func (e *Engine) Rooms() []Room {
	rooms := make([]Room, 0, len(e.rooms))
	for _, state := range e.rooms {
		room := state.room
		room.Members = append([]Member(nil), room.Members...)
		rooms = append(rooms, room)
	}
	return rooms
}

func (e *Engine) Prepare(room Room, profileID, nonce uint32, policy Policy, now time.Time) []Delivery {
	if nonce == 0 || e.versions[profileID] != ProtocolVersion || e.lastNonce[profileID] == nonce {
		return nil
	}
	state, humanCount, capable, allCapable, changes, ok := e.reconcile(room)
	if !ok || !contains(capable, profileID) {
		return changes
	}
	e.lastNonce[profileID] = nonce
	// A new selection supersedes a proposal that never committed. It must
	// collect fresh preparation from everyone rather than mix two selections.
	if state.active != nil && !state.active.Committed {
		changes = append(changes, cancel(state)...)
	}
	state.prepared[profileID] = nonce
	if len(state.prepared) != len(capable) {
		return changes
	}
	for _, pid := range capable {
		if state.prepared[pid] == 0 {
			return changes
		}
	}
	cpuCount := 0
	if policy.Enabled && allCapable {
		if policy.AutoFill {
			cpuCount = MaxPlayers - humanCount
		} else if policy.Count >= 1 && policy.Count <= 11 {
			cpuCount = min(policy.Count, MaxPlayers-humanCount)
		}
	}
	knockout := policy.KnockoutEnabled && allCapable && humanCount+cpuCount >= 2
	for _, member := range state.room.Members {
		// Native spectator HUD/camera allocation is currently one local human.
		knockout = knockout && member.LocalPlayers == 1
	}
	// Every current participant has entered the next selection. Retire the old
	// epoch before delivering its replacement, never when a waiting human joins
	// an otherwise running race.
	changes = append(changes, cancel(state)...)
	e.nextEpoch++
	if e.nextEpoch == 0 {
		e.nextEpoch++
	}
	seed := e.nextEpoch ^ room.GroupID ^ room.OwnerPID*2654435761
	if seed == 0 {
		seed = 1
	}
	state.active = &round{
		Plan:    Plan{Epoch: e.nextEpoch, OwnerPID: room.OwnerPID, HumanCount: humanCount, CPUCount: cpuCount, Seed: seed, KnockoutEnabled: knockout},
		members: append([]uint32(nil), capable...), acks: map[uint32]bool{}, sequences: map[uint8]uint32{}, created: now,
	}
	state.prepared = map[uint32]uint32{}
	ko := 0
	if knockout {
		ko = 1
	}
	message := fmt.Sprintf("LCPU2:P:%d:%d:%d:%d:%d:%d", e.nextEpoch, room.OwnerPID, humanCount, cpuCount, seed, ko)
	return append(changes, deliver(state.active.members, message)...)
}

func (e *Engine) Ack(room Room, profileID, epoch uint32, now time.Time) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if !ok || state.active == nil || state.active.Epoch != epoch || state.active.Committed || !contains(state.active.members, profileID) {
		return changes
	}
	state.active.acks[profileID] = true
	if len(state.active.acks) != len(state.active.members) {
		return changes
	}
	state.active.Committed = true
	state.active.created = now
	return append(changes, deliver(state.active.members, fmt.Sprintf("LCPU2:C:%d", epoch))...)
}

func (e *Engine) Ping(room Room, profileID, epoch uint32, now time.Time) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if ok && state.active != nil && state.active.Committed && state.active.Epoch == epoch && state.active.OwnerPID == profileID {
		state.active.lastAlive = now
		if state.active.CPUCount > 0 || state.active.KnockoutEnabled {
			// Finished CPUs may stop producing kart packets while humans are
			// still racing. Guests must distinguish that from a lost owner.
			changes = append(changes, deliver(state.active.members, fmt.Sprintf("LCPU2:H:%d", epoch))...)
		}
	}
	return changes
}

func (e *Engine) Frame(room Room, profileID uint32, frame []byte, now time.Time) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if !ok || state.active == nil || !state.active.Committed || state.active.OwnerPID != profileID || !validFrame(frame, state.active.Plan) {
		return changes
	}
	sequence := binary.BigEndian.Uint32(frame[4:8])
	id := frame[8]
	if last, exists := state.active.sequences[id]; exists && int32(sequence-last) <= 0 {
		return changes
	}
	state.active.sequences[id] = sequence
	state.active.lastAlive = now
	for _, pid := range state.active.members {
		if pid != profileID {
			changes = append(changes, Delivery{To: pid, Frame: append([]byte(nil), frame...)})
		}
	}
	return changes
}

// KnockoutEvent accepts only the frozen owner's ordered decision stream.
// All fields are big endian: epoch:u32 sequence:u32 lap:u16 id:u8 place:u8
// mask:u16 kind:u8 reserved:u8 finalBaseMs:u32 reserved:u32. Kind 0 eliminates;
// kind 1 finalizes all retired racers after survivors finish, using one clock.
const KnockoutEventSize = 24

func (e *Engine) KnockoutEvent(room Room, profileID uint32, event []byte, now time.Time) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if !ok || state.active == nil || !state.active.Committed || !state.active.KnockoutEnabled || state.active.OwnerPID != profileID || len(event) != KnockoutEventSize {
		return changes
	}
	round := state.active
	epoch := binary.BigEndian.Uint32(event[:4])
	sequence := binary.BigEndian.Uint32(event[4:8])
	lap := binary.BigEndian.Uint16(event[8:10])
	id, place, kind := event[10], event[11], event[14]
	mask := binary.BigEndian.Uint16(event[12:14])
	base := binary.BigEndian.Uint32(event[16:20])
	total := round.HumanCount + round.CPUCount
	if epoch != round.Epoch || sequence != round.knockoutSequence+1 || event[15] != 0 || binary.BigEndian.Uint32(event[20:24]) != 0 || round.knockoutFinalBase != 0 {
		return changes
	}
	switch kind {
	case 0:
		if lap < 2 || lap > 255 || lap <= round.knockoutLap || int(id) >= total || place < 2 || int(place) > total || base != 0 || (round.knockoutLastPlace != 0 && place >= round.knockoutLastPlace) {
			return changes
		}
		bit := uint16(1) << id
		placeBit := uint16(1) << (place - 1)
		if round.knockoutMask&bit != 0 || round.knockoutPlaces&placeBit != 0 || mask != round.knockoutMask|bit {
			return changes
		}
		round.knockoutLap, round.knockoutMask, round.knockoutLastPlace = lap, mask, place
		round.knockoutPlaces |= placeBit
	case 1:
		if round.knockoutMask == 0 || lap != 0 || id != 0xff || place != 0 || mask != round.knockoutMask || base == 0 || base > 63*60000-1-MaxPlayers*10 {
			return changes
		}
		round.knockoutFinalBase = base
	default:
		return changes
	}
	round.knockoutSequence = sequence
	round.lastAlive = now
	for _, pid := range round.members {
		changes = append(changes, Delivery{To: pid, Knockout: append([]byte(nil), event...)})
	}
	return changes
}

func (e *Engine) GridError(room Room, profileID, epoch uint32) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if ok && state.active != nil && state.active.Epoch == epoch && contains(state.active.members, profileID) {
		changes = append(changes, cancel(state)...)
	}
	return changes
}

// Reconcile updates the next-race lobby even when a player's GPCM stays online.
// A committed race keeps its frozen CPU slots until its owner or room is lost.
func (e *Engine) Reconcile(room Room) []Delivery {
	_, _, _, _, changes, _ := e.reconcile(room)
	return changes
}

func (e *Engine) Leave(profileID uint32) []Delivery {
	var changes []Delivery
	for name, state := range e.rooms {
		if state.room.OwnerPID == profileID || (state.active != nil && state.active.OwnerPID == profileID) {
			changes = append(changes, cancel(state)...)
			delete(e.rooms, name)
			continue
		}
		room := state.room
		room.Members = nil
		for _, member := range state.room.Members {
			if member.ProfileID != profileID {
				room.Members = append(room.Members, member)
			}
		}
		if len(room.Members) != len(state.room.Members) {
			_, _, _, _, deliveries, _ := e.reconcile(room)
			changes = append(changes, deliveries...)
		}
	}
	return changes
}

func (e *Engine) Disconnect(profileID uint32) []Delivery {
	changes := e.Leave(profileID)
	delete(e.versions, profileID)
	delete(e.lastNonce, profileID)
	return changes
}

func (e *Engine) RemoveRoom(name string) []Delivery {
	state := e.rooms[name]
	if state == nil {
		return nil
	}
	changes := cancel(state)
	delete(e.rooms, name)
	return changes
}

func (e *Engine) Expire(now time.Time) []Delivery {
	var changes []Delivery
	for _, state := range e.rooms {
		active := state.active
		if active == nil {
			continue
		}
		if !active.Committed {
			if now.Sub(active.created) > 15*time.Second {
				changes = append(changes, cancel(state)...)
			}
			continue
		}
		if active.CPUCount == 0 && !active.KnockoutEnabled {
			continue
		}
		if (active.lastAlive.IsZero() && now.Sub(active.created) > LoadingGrace) ||
			(!active.lastAlive.IsZero() && now.Sub(active.lastAlive) > HostTimeout) {
			changes = append(changes, cancel(state)...)
		}
	}
	return changes
}

func (e *Engine) reconcile(room Room) (*roomState, int, []uint32, bool, []Delivery, bool) {
	if room.Name == "" || room.OwnerPID == 0 || len(room.Members) == 0 || len(room.Members) > MaxPlayers {
		return nil, 0, nil, false, nil, false
	}
	room.Members = append([]Member(nil), room.Members...)
	sort.Slice(room.Members, func(i, j int) bool { return room.Members[i].ProfileID < room.Members[j].ProfileID })
	humanCount := 0
	ownerPresent := false
	var capable []uint32
	var signature strings.Builder
	fmt.Fprintf(&signature, "%d/%d", room.GroupID, room.OwnerPID)
	for i, member := range room.Members {
		if member.ProfileID == 0 || member.LocalPlayers < 1 || member.LocalPlayers > 2 || (i > 0 && member.ProfileID == room.Members[i-1].ProfileID) {
			return nil, 0, nil, false, nil, false
		}
		humanCount += int(member.LocalPlayers)
		ownerPresent = ownerPresent || member.ProfileID == room.OwnerPID
		version := e.versions[member.ProfileID]
		fmt.Fprintf(&signature, "/%d:%d:%d", member.ProfileID, member.LocalPlayers, version)
		if version == ProtocolVersion {
			capable = append(capable, member.ProfileID)
		}
	}
	if humanCount > MaxPlayers || !ownerPresent {
		return nil, 0, nil, false, nil, false
	}
	state := e.rooms[room.Name]
	var changes []Delivery
	if state == nil {
		state = &roomState{room: room, signature: signature.String(), prepared: map[uint32]uint32{}}
		e.rooms[room.Name] = state
	} else if state.signature != signature.String() {
		// Joining humans wait for the next native selection; a departed human
		// is handled by the game's ordinary disconnect path. Neither change
		// can replace already allocated CPU objects in the running race.
		preserve := state.active != nil && state.active.Committed &&
			state.room.GroupID == room.GroupID && state.active.OwnerPID == room.OwnerPID &&
			((state.active.CPUCount == 0 && !state.active.KnockoutEnabled) || contains(capable, room.OwnerPID))
		if !preserve {
			changes = cancel(state)
		}
		state.room = room
		state.signature = signature.String()
		// Membership changes invalidate preparation for the next roster even
		// when the current race can continue. Everybody must prepare afresh.
		state.prepared = map[uint32]uint32{}
	}
	return state, humanCount, capable, len(capable) == len(room.Members), changes, true
}

func cancel(state *roomState) []Delivery {
	var deliveries []Delivery
	if state.active != nil {
		deliveries = deliver(state.active.members, fmt.Sprintf("LCPU2:X:%d", state.active.Epoch))
	}
	state.active = nil
	state.prepared = map[uint32]uint32{}
	return deliveries
}

func deliver(members []uint32, message string) []Delivery {
	deliveries := make([]Delivery, 0, len(members))
	for _, pid := range members {
		deliveries = append(deliveries, Delivery{To: pid, Message: message})
	}
	return deliveries
}

func contains(members []uint32, pid uint32) bool {
	for _, member := range members {
		if member == pid {
			return true
		}
	}
	return false
}

func validFrame(frame []byte, plan Plan) bool {
	if len(frame) != FrameSize || binary.BigEndian.Uint32(frame[:4]) != plan.Epoch ||
		int(frame[8]) < plan.HumanCount || int(frame[8]) >= plan.HumanCount+plan.CPUCount ||
		frame[9]&^byte(3) != 0 || frame[9]&1 == 0 || binary.BigEndian.Uint16(frame[10:12]) > 255 ||
		frame[38] == 0 || frame[38] > MaxPlayers || frame[39] != 0 {
		return false
	}
	for offset := 12; offset <= 28; offset += 4 {
		value := float64(math.Float32frombits(binary.BigEndian.Uint32(frame[offset : offset+4])))
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e6 {
			return false
		}
	}
	if frame[9]&2 != 0 {
		ms := binary.BigEndian.Uint32(frame[44:48])
		if ms == 0 || ms >= 63*60000 {
			return false
		}
	}
	return true
}
