package onlinecpu

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"
)

var testTime = time.Unix(1000, 0)

func testRoom() Room {
	return Room{Name: "room", GroupID: 99, OwnerPID: 10, Members: []Member{{ProfileID: 10, LocalPlayers: 1}, {ProfileID: 20, LocalPlayers: 1}}}
}

func preparedEngine(t *testing.T, count int) (*Engine, Room, Plan) {
	t.Helper()
	e, room := New(100), testRoom()
	e.Register(10, 1)
	e.Register(20, 1)
	if messages := e.Prepare(room, 10, 1, Policy{Enabled: true, Count: count}, testTime); len(messages) != 0 {
		t.Fatal("proposed before every capable member prepared")
	}
	if messages := e.Prepare(room, 20, 1, Policy{Enabled: true, Count: count}, testTime); len(messages) != 2 || !strings.HasPrefix(messages[0].Message, "LCPU1:P:") {
		t.Fatalf("proposal deliveries = %+v", messages)
	}
	plan, ok := e.Plan(room.Name)
	if !ok {
		t.Fatal("missing proposed plan")
	}
	return e, room, plan
}

func commit(t *testing.T, e *Engine, room Room, plan Plan) {
	t.Helper()
	if messages := e.Ack(room, 10, plan.Epoch, testTime); len(messages) != 0 {
		t.Fatal("committed before all ACKs")
	}
	if messages := e.Ack(room, 20, plan.Epoch, testTime); len(messages) != 2 || !strings.HasPrefix(messages[0].Message, "LCPU1:C:") {
		t.Fatalf("commit deliveries = %+v", messages)
	}
}

func cpuFrame(plan Plan, id uint8, sequence uint32) []byte {
	frame := make([]byte, FrameSize)
	binary.BigEndian.PutUint32(frame[:4], plan.Epoch)
	binary.BigEndian.PutUint32(frame[4:8], sequence)
	frame[8], frame[9], frame[38] = id, 1, 1
	binary.BigEndian.PutUint16(frame[10:12], 2)
	binary.BigEndian.PutUint32(frame[12:16], math.Float32bits(2.5))
	binary.BigEndian.PutUint32(frame[16:20], math.Float32bits(2.5))
	return frame
}

func TestNegotiationRequiresFreshPreparationAndAllACKs(t *testing.T) {
	e, room, plan := preparedEngine(t, 4)
	if plan.OwnerPID != 10 || plan.HumanCount != 2 || plan.CPUCount != 4 || plan.Committed || plan.Seed == 0 {
		t.Fatalf("proposal = %+v", plan)
	}
	if messages := e.Frame(room, 10, cpuFrame(plan, 2, 1), testTime); len(messages) != 0 {
		t.Fatal("relayed a frame before commit")
	}
	if messages := e.Ack(room, 30, plan.Epoch, testTime); len(messages) != 0 {
		t.Fatal("outsider ACK affected negotiation")
	}
	if messages := e.Ack(room, 10, plan.Epoch+1, testTime); len(messages) != 0 {
		t.Fatal("stale ACK affected negotiation")
	}
	commit(t, e, room, plan)
	if messages := e.Prepare(room, 10, 1, Policy{}, testTime); len(messages) != 0 {
		t.Fatal("replayed prepare created a selection")
	}
	if messages := e.Ack(room, 20, plan.Epoch, testTime); len(messages) != 0 {
		t.Fatal("duplicate ACK recommitted the plan")
	}
	// A settings change takes effect only after all members prepare a new
	// selection. The existing round continues accepting its owner's frames.
	e.Prepare(room, 10, 2, Policy{}, testTime)
	if messages := e.Frame(room, 10, cpuFrame(plan, 2, 1), testTime); len(messages) != 1 || messages[0].To != 20 {
		t.Fatalf("in-flight policy changed before the next selection: %+v", messages)
	}
	e.Prepare(room, 20, 2, Policy{}, testTime)
	next, _ := e.Plan(room.Name)
	if next.Epoch == plan.Epoch || next.CPUCount != 0 {
		t.Fatalf("next off selection = %+v", next)
	}
	if messages := e.Frame(room, 10, cpuFrame(plan, 2, 2), testTime); len(messages) != 0 {
		t.Fatal("old epoch frame reached the next selection")
	}
}

func TestLegacyOffAndGridCapacity(t *testing.T) {
	for _, test := range []struct {
		name    string
		version uint32
		policy  Policy
		want    int
	}{
		{name: "legacy", version: 0, policy: Policy{Enabled: true, Count: 4}},
		{name: "unsupported protocol", version: 2, policy: Policy{Enabled: true, Count: 4}},
		{name: "off", version: 1, policy: Policy{Count: 4}},
		{name: "clamped at twelve", version: 1, policy: Policy{Enabled: true, Count: 11}, want: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, room := New(1), testRoom()
			e.Register(10, 1)
			e.Register(20, test.version)
			messages := e.Prepare(room, 10, 1, test.policy, testTime)
			if test.version == 1 {
				messages = e.Prepare(room, 20, 1, test.policy, testTime)
			}
			if len(messages) == 0 {
				t.Fatal("zero-CPU compatibility plan was not proposed")
			}
			plan, _ := e.Plan(room.Name)
			if plan.CPUCount != test.want {
				t.Fatalf("CPU count = %d, want %d", plan.CPUCount, test.want)
			}
		})
	}
	room := testRoom()
	room.Members = nil
	e := New(1)
	for i := uint32(1); i <= 6; i++ {
		pid := i * 10
		room.Members = append(room.Members, Member{ProfileID: pid, LocalPlayers: 2})
		e.Register(pid, 1)
	}
	for _, member := range room.Members {
		e.Prepare(room, member.ProfileID, 1, Policy{Enabled: true, Count: 11}, testTime)
	}
	if plan, _ := e.Plan(room.Name); plan.HumanCount != 12 || plan.CPUCount != 0 {
		t.Fatalf("full human grid = %+v", plan)
	}
}

func TestMembershipChangeAndDisconnectCancelFrozenPlan(t *testing.T) {
	e, room, plan := preparedEngine(t, 4)
	commit(t, e, room, plan)
	room.Members = append(room.Members, Member{ProfileID: 30, LocalPlayers: 1})
	if messages := e.Reconcile(room); len(messages) != 2 || !strings.HasPrefix(messages[0].Message, "LCPU1:X:") {
		t.Fatalf("membership change did not cancel: %+v", messages)
	}
	if _, ok := e.Plan(room.Name); ok {
		t.Fatal("old plan survived membership change")
	}
	e, room, plan = preparedEngine(t, 4)
	commit(t, e, room, plan)
	if messages := e.Disconnect(10); len(messages) != 2 {
		t.Fatalf("owner disconnect did not cancel: %+v", messages)
	}
	if messages := e.Frame(room, 10, cpuFrame(plan, 2, 1), testTime); len(messages) != 0 {
		t.Fatal("disconnected owner relayed frames")
	}
}

func TestFramesRejectWrongOwnerReplayAndInvalidMetadata(t *testing.T) {
	e, room, plan := preparedEngine(t, 4)
	commit(t, e, room, plan)
	if messages := e.Frame(room, 20, cpuFrame(plan, 2, 1), testTime); len(messages) != 0 {
		t.Fatal("non-owner relayed a frame")
	}
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"short", func(f []byte) []byte { return f[:111] }},
		{"old epoch", func(f []byte) []byte { binary.BigEndian.PutUint32(f[:4], plan.Epoch-1); return f }},
		{"human slot", func(f []byte) []byte { f[8] = 1; return f }},
		{"outside grid", func(f []byte) []byte { f[8] = 6; return f }},
		{"invalid flags", func(f []byte) []byte { f[9] = 5; return f }},
		{"no data", func(f []byte) []byte { f[9] = 0; return f }},
		{"reserved", func(f []byte) []byte { f[39] = 1; return f }},
		{"invalid position", func(f []byte) []byte { f[38] = 0; return f }},
		{"position too large", func(f []byte) []byte { f[38] = 13; return f }},
		{"lap too large", func(f []byte) []byte { binary.BigEndian.PutUint16(f[10:12], 256); return f }},
		{"NaN", func(f []byte) []byte {
			binary.BigEndian.PutUint32(f[12:16], math.Float32bits(float32(math.NaN())))
			return f
		}},
		{"infinity", func(f []byte) []byte {
			binary.BigEndian.PutUint32(f[20:24], math.Float32bits(float32(math.Inf(1))))
			return f
		}},
		{"out of bounds", func(f []byte) []byte { binary.BigEndian.PutUint32(f[28:32], math.Float32bits(1e7)); return f }},
		{"zero finish", func(f []byte) []byte { f[9] = 3; return f }},
		{"unencodable finish", func(f []byte) []byte { f[9] = 3; binary.BigEndian.PutUint32(f[44:48], 63*60000); return f }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if messages := e.Frame(room, 10, test.mutate(cpuFrame(plan, 2, 1)), testTime); len(messages) != 0 {
				t.Fatal("invalid frame was relayed")
			}
		})
	}
	for _, seq := range []uint32{0, 1} {
		messages := e.Frame(room, 10, cpuFrame(plan, 2, seq), testTime)
		if len(messages) != 1 || messages[0].To != 20 || len(messages[0].Frame) != FrameSize {
			t.Fatalf("valid owner frame = %+v", messages)
		}
		if duplicate := e.Frame(room, 10, cpuFrame(plan, 2, seq), testTime); len(duplicate) != 0 {
			t.Fatal("replayed frame was relayed")
		}
	}
	if messages := e.Frame(room, 10, cpuFrame(plan, 3, 0), testTime); len(messages) != 1 {
		t.Fatal("one CPU's sequence suppressed another CPU")
	}
}

func TestFrameSequenceWrapMatchesClient(t *testing.T) {
	e, room, plan := preparedEngine(t, 1)
	commit(t, e, room, plan)
	for _, sequence := range []uint32{0xfffffffe, 0xffffffff, 0, 1} {
		if got := e.Frame(room, 10, cpuFrame(plan, 2, sequence), testTime); len(got) != 1 {
			t.Fatalf("sequence %d rejected at wrap: %+v", sequence, got)
		}
	}
	for _, sequence := range []uint32{1, 0, 0xffffffff} {
		if got := e.Frame(room, 10, cpuFrame(plan, 2, sequence), testTime); len(got) != 0 {
			t.Fatalf("replayed sequence %d accepted: %+v", sequence, got)
		}
	}
}

func TestHostHeartbeatLoadingGraceAndGridErrors(t *testing.T) {
	e, room, plan := preparedEngine(t, 4)
	commit(t, e, room, plan)
	if messages := e.Expire(testTime.Add(LoadingGrace)); len(messages) != 0 {
		t.Fatal("loading grace expired early")
	}
	if messages := e.Ping(room, 10, plan.Epoch, testTime.Add(LoadingGrace)); len(messages) != 2 || !strings.HasPrefix(messages[0].Message, "LCPU1:H:") {
		t.Fatalf("owner heartbeat not forwarded to the committed grid: %+v", messages)
	}
	if messages := e.Expire(testTime.Add(LoadingGrace + HostTimeout)); len(messages) != 0 {
		t.Fatal("heartbeat expired early")
	}
	if messages := e.Ping(room, 20, plan.Epoch, testTime.Add(LoadingGrace+HostTimeout)); len(messages) != 0 {
		t.Fatal("guest forged an owner heartbeat")
	}
	if messages := e.Expire(testTime.Add(LoadingGrace + HostTimeout + time.Second)); len(messages) != 2 {
		t.Fatal("guest heartbeat extended the owner's liveness")
	}
	e, room, plan = preparedEngine(t, 4)
	commit(t, e, room, plan)
	if messages := e.GridError(room, 99, plan.Epoch); len(messages) != 0 {
		t.Fatal("outsider cancelled a plan")
	}
	if messages := e.GridError(room, 20, plan.Epoch); len(messages) != 2 {
		t.Fatal("member grid failure did not cancel")
	}
	e, room, plan = preparedEngine(t, 4)
	commit(t, e, room, plan)
	if messages := e.Expire(testTime.Add(LoadingGrace + time.Second)); len(messages) != 2 {
		t.Fatal("silent owner survived the loading grace")
	}
}
