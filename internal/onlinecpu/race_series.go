package onlinecpu

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	KnockoutModeRound      = "round"
	KnockoutModeRace       = "race"
	CourseResultHeaderSize = 12
	CourseResultRowSize    = 16
	MaxCourseResultSize    = CourseResultHeaderSize + MaxPlayers*CourseResultRowSize
)

// Human racers are keyed by authenticated profile, not their transient native
// grid ID. Knockout supports one local human per console. CPU ordinals retain
// their identity while the series' frozen grid and seed are unchanged.
type seriesEntrant struct {
	profile uint32
	kind    uint8
	slot    uint8
}

type raceSeries struct {
	id              uint32
	key             string
	cpuCount        int
	seed            uint32
	placements      map[seriesEntrant]uint8
	completedRounds int
	complete        bool
}

func newRaceSeries(id uint32, key string, cpus int, seed uint32) *raceSeries {
	return &raceSeries{id: id, key: key, cpuCount: cpus, seed: seed, placements: map[seriesEntrant]uint8{}}
}

func raceSeriesKey(signature string, outCount int) string {
	return fmt.Sprintf("%s/race/%d", signature, outCount)
}

func cloneSeriesPlacements(placements map[seriesEntrant]uint8) map[seriesEntrant]uint8 {
	copy := make(map[seriesEntrant]uint8, len(placements))
	for entrant, place := range placements {
		copy[entrant] = place
	}
	return copy
}

func tournamentMessage(active *round) string {
	cpus := make([]string, active.CPUCount)
	for i := range cpus {
		cpus[i] = strconv.Itoa(int(active.seriesPlacements[seriesEntrant{kind: 1, slot: uint8(i)}]))
	}
	cpuText := strings.Join(cpus, ",")
	if cpuText == "" {
		cpuText = "0"
	}
	var humans []string
	for _, pid := range active.frozenHumans {
		if place := active.seriesPlacements[seriesEntrant{profile: pid}]; place != 0 {
			humans = append(humans, fmt.Sprintf("%d.%d", pid, place))
		}
	}
	humanText := strings.Join(humans, ",")
	if humanText == "" {
		humanText = "0"
	}
	return fmt.Sprintf("LCPU3:T:%d:%d:%d:%d:%s:%s", active.Epoch, active.SeriesID, active.SeriesRound, active.KnockoutEliminationCount, cpuText, humanText)
}

func planDeliveries(active *round, recipients []uint32, committed bool) []Delivery {
	var deliveries []Delivery
	for _, pid := range recipients {
		deliveries = append(deliveries, Delivery{To: pid, Message: active.proposal})
		if active.tournament != "" {
			deliveries = append(deliveries, Delivery{To: pid, Message: active.tournament})
		}
		if committed && active.Committed {
			deliveries = append(deliveries, Delivery{To: pid, Message: fmt.Sprintf("LCPU3:C:%d", active.Epoch)})
		}
	}
	return deliveries
}

// PlanMessages recovers an interrupted proposal or tournament-metadata relay.
// It never creates another epoch or changes a committed grid.
func (e *Engine) PlanMessages(room Room, profileID, epoch uint32) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if !ok || state.active == nil || state.active.Epoch != epoch || !contains(state.active.members, profileID) {
		return changes
	}
	changes = append(changes, planDeliveries(state.active, []uint32{profileID}, true)...)
	for _, reply := range state.active.courseReplies {
		if reply.To == profileID {
			changes = append(changes, Delivery{To: profileID, Knockout: append([]byte(nil), reply.Knockout...)})
		}
	}
	return changes
}

type courseRow struct {
	entrant seriesEntrant
	id      uint8
	flags   uint8
	finish  uint32
}

func entrantLess(a, b seriesEntrant) bool {
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	if a.profile != b.profile {
		return a.profile < b.profile
	}
	return a.slot < b.slot
}

func cloneDeliveries(source []Delivery) []Delivery {
	out := make([]Delivery, len(source))
	for i, delivery := range source {
		out[i] = delivery
		out[i].Knockout = append([]byte(nil), delivery.Knockout...)
	}
	return out
}

// CourseResult resolves one completed Race Knockout course. The owner supplies
// the full frozen grid's explicit identities; server sorting never treats QR2
// member order as native racer order. Previously eliminated racers remain in
// the grid and can be unfinished while spectating.
func (e *Engine) CourseResult(room Room, profileID uint32, data []byte, now time.Time) []Delivery {
	state, _, _, _, changes, ok := e.reconcile(room)
	if !ok || state.active == nil || !state.active.Committed || !state.active.KnockoutEnabled ||
		state.active.KnockoutMode != KnockoutModeRace || state.active.OwnerPID != profileID ||
		state.active.series == nil || len(data) < CourseResultHeaderSize || len(data) > MaxCourseResultSize {
		return changes
	}
	active := state.active
	if binary.BigEndian.Uint32(data[:4]) != active.Epoch {
		return changes
	}
	if active.courseReport != nil {
		if bytes.Equal(data, active.courseReport) {
			active.lastAlive = now
			return append(changes, cloneDeliveries(active.courseReplies)...)
		}
		return changes
	}
	total := active.HumanCount + active.CPUCount
	base := binary.BigEndian.Uint32(data[4:8])
	if int(data[8]) != total || len(data) != CourseResultHeaderSize+total*CourseResultRowSize ||
		data[9] != 0 || data[10] != 0 || data[11] != 0 || base == 0 || base > 63*60000-1-MaxPlayers*10 {
		return changes
	}
	humans := make(map[uint32]bool, len(active.frozenHumans))
	for _, pid := range active.frozenHumans {
		humans[pid] = true
	}
	seen := make(map[seriesEntrant]bool, total)
	var ids uint16
	var priorMask uint16
	var dnfMask uint16
	var rows []courseRow
	for i := 0; i < total; i++ {
		row := data[CourseResultHeaderSize+i*CourseResultRowSize : CourseResultHeaderSize+(i+1)*CourseResultRowSize]
		id, kind, slot, flags := row[0], row[1], row[2], row[3]
		pid := binary.BigEndian.Uint32(row[4:8])
		finish := binary.BigEndian.Uint32(row[8:12])
		if int(id) >= total || ids&(1<<id) != 0 || binary.BigEndian.Uint32(row[12:16]) != 0 ||
			(flags != 0 && flags != 1 && flags != 2 && flags != 4) ||
			(flags == 1 && (finish == 0 || finish > base || finish >= 63*60000)) || (flags != 1 && finish != 0) {
			return changes
		}
		entrant := seriesEntrant{profile: pid, kind: kind, slot: slot}
		if kind == 0 {
			if int(id) >= active.HumanCount || slot != 0 || !humans[pid] {
				return changes
			}
		} else if kind == 1 {
			if pid != 0 || int(slot) >= active.CPUCount || int(id) != active.HumanCount+int(slot) {
				return changes
			}
		} else {
			return changes
		}
		if seen[entrant] {
			return changes
		}
		seen[entrant] = true
		ids |= 1 << id
		if flags == 4 {
			dnfMask |= 1 << id
		}
		if active.seriesPlacements[entrant] != 0 {
			priorMask |= 1 << id
			continue
		}
		if flags == 0 {
			return changes
		}
		rows = append(rows, courseRow{entrant: entrant, id: id, flags: flags, finish: finish})
	}
	if ids != uint16(1<<total)-1 || len(rows) < 2 {
		return changes
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.flags == 1) != (b.flags == 1) {
			return a.flags == 1
		}
		if a.flags == 1 && a.finish != b.finish {
			return a.finish < b.finish
		}
		return entrantLess(a.entrant, b.entrant)
	})
	outCount := min(active.KnockoutEliminationCount, len(rows)-1)
	if outCount < 1 || outCount > 2 {
		return changes
	}
	// Generate the same bounded ordered K/F stream used by the native adapter.
	// No client selects victims mid-course in Race Knockout.
	mask := priorMask
	var replies []Delivery
	for i := 0; i < outCount; i++ {
		victim := rows[len(rows)-1-i]
		place := uint8(len(rows) - i)
		mask |= 1 << victim.id
		active.series.placements[victim.entrant] = place
		event := make([]byte, KnockoutEventSize)
		binary.BigEndian.PutUint32(event[:4], active.Epoch)
		binary.BigEndian.PutUint32(event[4:8], uint32(i+1))
		binary.BigEndian.PutUint16(event[8:10], uint16(i+2))
		event[10], event[11] = victim.id, place
		binary.BigEndian.PutUint16(event[12:14], mask)
		for _, pid := range active.members {
			replies = append(replies, Delivery{To: pid, Knockout: append([]byte(nil), event...)})
		}
	}
	final := make([]byte, KnockoutEventSize)
	binary.BigEndian.PutUint32(final[:4], active.Epoch)
	binary.BigEndian.PutUint32(final[4:8], uint32(outCount+1))
	final[10], final[14] = 0xff, 1
	binary.BigEndian.PutUint16(final[12:14], mask)
	binary.BigEndian.PutUint32(final[16:20], base)
	// Capability-3 Race Knockout uses this final tail for observed DNFs.
	// Guests cannot learn a native 99-minute DNF through the bounded CPU
	// finish frame, so they normalize these rows only after course commit.
	binary.BigEndian.PutUint32(final[20:24], uint32(dnfMask))
	for _, pid := range active.members {
		replies = append(replies, Delivery{To: pid, Knockout: append([]byte(nil), final...)})
	}
	active.knockoutMask = mask
	active.knockoutFinalBase = base
	active.knockoutSequence = uint32(outCount + 1)
	active.knockoutLastEvent = append([]byte(nil), final...)
	active.lastAlive = now
	active.courseReport = append([]byte(nil), data...)
	active.courseReplies = cloneDeliveries(replies)
	active.series.completedRounds = active.SeriesRound
	active.series.complete = len(rows)-outCount == 1
	return append(changes, replies...)
}
