// Package racetelemetry holds bounded, short-lived Host Manager race snapshots.
// Telemetry is observational and never drives game or CPU synchronization.
package racetelemetry

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	ProtocolVersion = 1
	LiveHeaderSize  = 24
	RacerSize       = 28
	MaxLiveSize     = LiveHeaderSize + 12*RacerSize
	MapHeaderSize   = 16
	MaxMapSize      = MapHeaderSize + 24*8
	MaxPoints       = 256
	MaxSegments     = 512
	maxRooms        = 256
	Retention       = 2 * time.Minute
	StaleAfter      = 3 * time.Second
)

type Member struct {
	LocalPlayers int
	Names        []string
}

// Room is a copied QR2 membership view; it contains no addresses or credentials.
type Room struct {
	Name          string
	GroupID       uint32
	Created       time.Time
	OwnerPID      uint32
	Members       map[uint32]Member
	KnockdownPool bool
}

func (r Room) Key() string { return fmt.Sprintf("%s/%d/%d", r.Name, r.GroupID, r.Created.UnixNano()) }

// Source is resolved from the authenticated connection and current room/plan.
type Source struct {
	Room       Room
	Reporter   uint32
	Connection uint64
	CPUStart   int
	CPUCount   int
}

type Point struct {
	X float32 `json:"x"`
	Z float32 `json:"z"`
}

type Segment struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

type CourseMap struct {
	Kind     string    `json:"kind"`
	Complete bool      `json:"complete"`
	Points   []Point   `json:"points"`
	Segments []Segment `json:"segments"`
}

type Racer struct {
	ID           uint8   `json:"id"`
	ProfileID    uint32  `json:"profileId"`
	LocalIndex   uint8   `json:"localIndex"`
	Name         string  `json:"name"`
	IsCPU        bool    `json:"isCPU"`
	Position     uint8   `json:"position"`
	Lap          uint16  `json:"lap"`
	Progress     float32 `json:"progress"`
	X            float32 `json:"x"`
	Z            float32 `json:"z"`
	HasPosition  bool    `json:"hasPosition"`
	Finished     bool    `json:"finished"`
	Disconnected bool    `json:"disconnected"`
	Eliminated   bool    `json:"eliminated"`
	FinishMS     uint32  `json:"finishMs"`
}

type Race struct {
	ID                string    `json:"id"`
	GroupID           uint32    `json:"groupId"`
	RoomName          string    `json:"roomName"`
	CourseID          uint8     `json:"courseId"`
	Phase             string    `json:"phase"`
	AgeMS             int64     `json:"ageMs"`
	Stale             bool      `json:"stale"`
	ReporterProfileID uint32    `json:"reporterProfileId"`
	ElapsedMS         uint32    `json:"elapsedMs"`
	Laps              uint8     `json:"laps"`
	Mirror            bool      `json:"mirror"`
	KnockoutEnabled   bool      `json:"knockoutEnabled"`
	KnockdownPool     bool      `json:"knockdownPool"`
	Seed              uint32    `json:"seed"`
	Racers            []Racer   `json:"racers"`
	Map               CourseMap `json:"map"`
}

type frame struct {
	Race
	epoch, sequence uint32
	phase           uint8
	mapExpected     bool
}

type mapState struct {
	pointTotal, segmentTotal int
	points                   []Point
	segments                 []Segment
	pointSeen, segmentSeen   []bool
	pointCount, segmentCount int
}

type entry struct {
	frame    frame
	source   Source
	updated  time.Time
	members  map[uint32]Member
	geometry mapState
}

type Store struct {
	mu     sync.Mutex
	rooms  map[string]*entry
	serial uint64
}

func New() *Store { return &Store{rooms: make(map[string]*entry)} }

func finite(v float32, limit float64) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && math.Abs(float64(v)) <= limit
}
func f32(b []byte) float32 { return math.Float32frombits(binary.BigEndian.Uint32(b)) }

func decodeLive(data []byte) (frame, bool) {
	if len(data) < LiveHeaderSize || len(data) > MaxLiveSize || data[0] != ProtocolVersion || data[1] > 3 || data[2] < 1 || data[2] > 12 || data[3] >= 32 || len(data) != LiveHeaderSize+int(data[2])*RacerSize || data[16] < 1 || data[17]&^byte(7) != 0 || data[18] != 0 || data[19] != 0 {
		return frame{}, false
	}
	f := frame{epoch: binary.BigEndian.Uint32(data[4:8]), sequence: binary.BigEndian.Uint32(data[8:12]), phase: data[1]}
	if f.epoch == 0 {
		return frame{}, false
	}
	f.CourseID = data[3]
	f.ElapsedMS = binary.BigEndian.Uint32(data[12:16])
	f.Laps = data[16]
	f.Mirror = data[17]&1 != 0
	f.KnockoutEnabled = data[17]&2 != 0
	f.mapExpected = data[17]&4 != 0
	f.Seed = binary.BigEndian.Uint32(data[20:24])
	f.Phase = []string{"loading", "countdown", "racing", "results"}[f.phase]
	f.Racers = make([]Racer, int(data[2]))
	identities := make(map[[2]uint32]bool)
	for i := range f.Racers {
		b := data[LiveHeaderSize+i*RacerSize : LiveHeaderSize+(i+1)*RacerSize]
		if int(b[0]) != i || b[1]&^byte(31) != 0 || b[8] > 1 || b[9] > data[2] || b[10] != 0 || b[11] != 0 {
			return frame{}, false
		}
		r := Racer{ID: b[0], IsCPU: b[1]&1 != 0, Finished: b[1]&2 != 0, Disconnected: b[1]&4 != 0, Eliminated: b[1]&8 != 0, HasPosition: b[1]&16 != 0, Lap: binary.BigEndian.Uint16(b[2:4]), ProfileID: binary.BigEndian.Uint32(b[4:8]), LocalIndex: b[8], Position: b[9], Progress: f32(b[12:16]), X: f32(b[16:20]), Z: f32(b[20:24]), FinishMS: binary.BigEndian.Uint32(b[24:28])}
		if r.Lap > 255 || !finite(r.Progress, 256) || r.Progress < 0 || !finite(r.X, 20000000) || !finite(r.Z, 20000000) || r.FinishMS > 24*60*60*1000 || r.IsCPU && (r.ProfileID != 0 || r.LocalIndex != 0) || !r.IsCPU && r.ProfileID == 0 {
			return frame{}, false
		}
		if !r.IsCPU {
			identity := [2]uint32{r.ProfileID, uint32(r.LocalIndex)}
			if identities[identity] {
				return frame{}, false
			}
			identities[identity] = true
		}
		f.Racers[i] = r
	}
	return f, true
}

func (s *Store) prune(now time.Time) {
	for key, e := range s.rooms {
		if now.Sub(e.updated) > Retention {
			delete(s.rooms, key)
		}
	}
}

func cloneMembers(m map[uint32]Member) map[uint32]Member {
	out := make(map[uint32]Member, len(m))
	for pid, member := range m {
		member.Names = append([]string(nil), member.Names...)
		out[pid] = member
	}
	return out
}

// AcceptLive requires the caller to resolve reporter authority before calling.
// It additionally validates racer identity against QR2 and the committed CPU tail.
func (s *Store) AcceptLive(source Source, data []byte, now time.Time) bool {
	f, ok := decodeLive(data)
	if !ok || source.Reporter == 0 || source.Room.Name == "" || source.CPUStart < 0 || source.CPUCount < 0 || source.CPUStart+source.CPUCount > 12 {
		return false
	}
	if _, ok := source.Room.Members[source.Reporter]; !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	key := source.Room.Key()
	old := s.rooms[key]
	same := old != nil && old.source.Reporter == source.Reporter && old.source.Connection == source.Connection
	continuing := same && old.frame.epoch == f.epoch
	if same && !continuing && int32(f.epoch-old.frame.epoch) <= 0 {
		return false
	}
	if continuing && (int32(f.sequence-old.frame.sequence) <= 0 || f.phase < old.frame.phase || f.CourseID != old.frame.CourseID || f.Laps != old.frame.Laps || f.Mirror != old.frame.Mirror || f.Seed != old.frame.Seed || f.KnockoutEnabled != old.frame.KnockoutEnabled || len(f.Racers) != len(old.frame.Racers)) {
		return false
	}
	for i := range f.Racers {
		r := &f.Racers[i]
		if source.CPUCount > 0 && r.IsCPU != (int(r.ID) >= source.CPUStart && int(r.ID) < source.CPUStart+source.CPUCount) {
			return false
		}
		if continuing {
			previous := old.frame.Racers[i]
			if r.ProfileID != previous.ProfileID || r.LocalIndex != previous.LocalIndex || r.IsCPU != previous.IsCPU {
				return false
			}
		}
		if r.IsCPU {
			if source.CPUCount < 1 || int(r.ID) < source.CPUStart || int(r.ID) >= source.CPUStart+source.CPUCount {
				return false
			}
			r.Name = fmt.Sprintf("CPU %d", int(r.ID)-source.CPUStart+1)
			continue
		}
		member, present := source.Room.Members[r.ProfileID]
		if !present && continuing && r.Disconnected {
			member, present = old.members[r.ProfileID]
		}
		if !present || int(r.LocalIndex) >= member.LocalPlayers {
			return false
		}
		if int(r.LocalIndex) < len(member.Names) {
			r.Name = member.Names[r.LocalIndex]
		}
		if r.Name == "" {
			r.Name = fmt.Sprintf("Player %d", r.ID+1)
		}
	}
	if source.CPUCount > 0 && len(f.Racers) != source.CPUStart+source.CPUCount {
		return false
	}
	if !continuing {
		if old == nil && len(s.rooms) >= maxRooms {
			var oldestKey string
			var oldest time.Time
			for k, v := range s.rooms {
				if oldest.IsZero() || v.updated.Before(oldest) {
					oldestKey, oldest = k, v.updated
				}
			}
			delete(s.rooms, oldestKey)
		}
		s.serial++
		old = &entry{members: cloneMembers(source.Room.Members), geometry: mapState{pointTotal: -1, segmentTotal: -1}}
		f.ID = fmt.Sprintf("%s:%d:%d", source.Room.Name, s.serial, f.epoch)
	} else {
		f.ID = old.frame.ID
	}
	f.RoomName = source.Room.Name
	f.KnockdownPool = source.Room.KnockdownPool
	f.GroupID = source.Room.GroupID
	f.ReporterProfileID = source.Reporter
	old.frame = f
	old.source = source
	old.updated = now
	s.rooms[key] = old
	return true
}

// AcceptMap only fills immutable fragments for the current authenticated race.
// Map reports do not extend the live snapshot's lifetime.
func (s *Store) AcceptMap(source Source, data []byte, now time.Time) bool {
	if len(data) < MapHeaderSize || len(data) > MaxMapSize || data[0] != ProtocolVersion || (data[1] != 1 && data[1] != 2) || data[3] != 0 || data[14] != 0 || data[15] != 0 {
		return false
	}
	kind := data[1]
	total, offset, count := int(binary.BigEndian.Uint16(data[8:10])), int(binary.BigEndian.Uint16(data[10:12])), int(binary.BigEndian.Uint16(data[12:14]))
	unit, limit, chunk := 8, MaxPoints, 24
	if kind == 2 {
		unit, limit, chunk = 4, MaxSegments, 48
	}
	if total > limit || offset > total || count > chunk || count > total-offset || len(data) != MapHeaderSize+count*unit || count == 0 && (total != 0 || offset != 0) || kind == 1 && total == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	e := s.rooms[source.Room.Key()]
	if e == nil || e.source.Connection != source.Connection || e.source.Reporter != source.Reporter || e.frame.epoch != binary.BigEndian.Uint32(data[4:8]) || e.frame.CourseID != data[2] {
		return false
	}
	m := &e.geometry
	if kind == 1 {
		if m.pointTotal != -1 && m.pointTotal != total {
			return false
		}
		points := make([]Point, count)
		for i := range points {
			b := data[MapHeaderSize+i*8:]
			points[i] = Point{f32(b[:4]), f32(b[4:8])}
			if !finite(points[i].X, 20000000) || !finite(points[i].Z, 20000000) {
				return false
			}
			if m.pointTotal >= 0 && m.pointSeen[offset+i] && m.points[offset+i] != points[i] {
				return false
			}
		}
		if m.pointTotal < 0 {
			for _, edge := range m.segments {
				if int(edge.From) >= total || int(edge.To) >= total {
					return false
				}
			}
			m.pointTotal = total
			m.points = make([]Point, total)
			m.pointSeen = make([]bool, total)
		}
		for i, p := range points {
			if !m.pointSeen[offset+i] {
				m.pointCount++
				m.pointSeen[offset+i] = true
			}
			m.points[offset+i] = p
		}
	} else {
		if m.segmentTotal != -1 && m.segmentTotal != total {
			return false
		}
		edges := make([]Segment, count)
		pointLimit := MaxPoints
		if m.pointTotal >= 0 {
			pointLimit = m.pointTotal
		}
		for i := range edges {
			b := data[MapHeaderSize+i*4:]
			edges[i] = Segment{binary.BigEndian.Uint16(b[:2]), binary.BigEndian.Uint16(b[2:4])}
			if int(edges[i].From) >= pointLimit || int(edges[i].To) >= pointLimit || edges[i].From == edges[i].To {
				return false
			}
			if m.segmentTotal >= 0 && m.segmentSeen[offset+i] && m.segments[offset+i] != edges[i] {
				return false
			}
		}
		if m.segmentTotal < 0 {
			m.segmentTotal = total
			m.segments = make([]Segment, total)
			m.segmentSeen = make([]bool, total)
		}
		for i, edge := range edges {
			if !m.segmentSeen[offset+i] {
				m.segmentCount++
				m.segmentSeen[offset+i] = true
			}
			m.segments[offset+i] = edge
		}
	}
	return true
}

// Snapshot returns detached copies and marks missing/migrated reporters stale.
// active maps current rooms/reporters to their live authenticated connection.
func (s *Store) Snapshot(now time.Time, active map[string]map[uint32]uint64) []Race {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	out := make([]Race, 0, len(s.rooms))
	for key, e := range s.rooms {
		r := e.frame.Race
		r.AgeMS = now.Sub(e.updated).Milliseconds()
		if r.AgeMS < 0 {
			r.AgeMS = 0
		}
		connection, connected := active[key][e.source.Reporter]
		r.Stale = now.Sub(e.updated) > StaleAfter || !connected || connection != e.source.Connection
		r.Racers = append([]Racer(nil), r.Racers...)
		r.Map = CourseMap{Kind: "none", Points: []Point{}, Segments: []Segment{}}
		m := &e.geometry
		if e.frame.mapExpected || m.pointTotal >= 0 || m.segmentTotal >= 0 {
			r.Map.Kind = "enemy-path"
		}
		if m.pointTotal > 0 && m.pointCount == m.pointTotal && m.segmentTotal >= 0 && m.segmentCount == m.segmentTotal {
			r.Map = CourseMap{Kind: "enemy-path", Complete: true, Points: append([]Point(nil), m.points...), Segments: append([]Segment{}, m.segments...)}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RoomName < out[j].RoomName })
	return out
}
