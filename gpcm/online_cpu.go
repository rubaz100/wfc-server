package gpcm

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"wwfc/common"
	"wwfc/internal/assets"
	"wwfc/internal/onlinecpu"
	"wwfc/logging"
	"wwfc/qr2"
)

var cpuRelay = struct {
	sync.Mutex
	engine *onlinecpu.Engine
	stop   chan struct{}
	done   chan struct{}
}{engine: onlinecpu.New(uint32(time.Now().UnixNano()))}

func OnlineCPUAvailable() bool {
	return common.OnlineCPUProtocolVersion == "3" && assets.OnlineRaceProtocolVersion() == onlinecpu.ProtocolVersion
}

func KnockdownPoolAvailable() bool {
	return OnlineCPUAvailable() && assets.KnockdownPoolProtocolVersion() == common.KnockdownPoolProtocolVersion
}

func cpuPolicy() onlinecpu.Policy {
	config := common.GetConfig()
	return onlinecpu.Policy{Enabled: config.OnlineCPUEnabled, AutoFill: config.OnlineCPUAutoFill, Count: config.OnlineCPUCount, KnockoutEnabled: config.KnockoutEnabled}
}

func (g *GameSpySession) acceptOnlineCPUCapability(command common.GameSpyCommand) {
	if !g.LoggedIn || !g.DeviceAuthenticated || g.GameName != "mariokartwii" || command.OtherValues["wl:cpu_ver"] != "3" {
		return
	}
	mutex.Lock()
	if sessions[g.User.ProfileId] != g {
		mutex.Unlock()
		return
	}
	g.CPUVersion = onlinecpu.ProtocolVersion
	g.KnockdownPoolVersion = 0
	if requested := command.OtherValues["wl:ko_pool_ver"]; requested == "1" || requested == "2" {
		version, _ := strconv.ParseUint(requested, 10, 32)
		g.KnockdownPoolVersion = uint32(version)
	}
	pid := g.User.ProfileId
	poolVersion := g.KnockdownPoolVersion
	mutex.Unlock()
	if !KnockdownPoolAvailable() || poolVersion != common.KnockdownPoolProtocolVersion {
		poolVersion = 0
	}
	qr2.SetKnockdownPoolVersion(pid, poolVersion)
	cpuRelay.Lock()
	cpuRelay.engine.Register(pid, onlinecpu.ProtocolVersion)
	cpuRelay.Unlock()
	sendOnlineCPUSettings(pid)
}

func sendOnlineCPUSettings(pid uint32) {
	policy := cpuPolicy()
	protocol, enabled := 0, 0
	if OnlineCPUAvailable() {
		protocol = onlinecpu.ProtocolVersion
	}
	if policy.Enabled {
		enabled = 1
	}
	count := policy.Count
	if policy.AutoFill {
		// Preserve the existing native settings envelope. Its count is only
		// an upper bound; the actual roster always comes from the proposal.
		count = onlinecpu.MaxPlayers - 1
	}
	deliveries := []onlinecpu.Delivery{{To: pid, Message: fmt.Sprintf("LCPU3:S:%d:%d:%d", protocol, enabled, count)}}
	mutex.Lock()
	poolVersion := uint32(0)
	requested := uint32(0)
	if session := sessions[pid]; session != nil {
		requested = session.KnockdownPoolVersion
		if requested == common.KnockdownPoolProtocolVersion && KnockdownPoolAvailable() {
			poolVersion = 1
		}
	}
	mutex.Unlock()
	// A separate envelope preserves the original CPU settings parser and also
	// reaches DNS/SBCM clients whose authenticated capability arrives at exlogin.
	if requested == 1 || requested == 2 {
		deliveries = append(deliveries, onlinecpu.Delivery{To: pid, Message: fmt.Sprintf("LKD%d:S:%d", requested, poolVersion)})
	}
	deliverOnlineCPU(deliveries)
}

// BroadcastOnlineCPUSettings advertises policy for the next selection only;
// the engine never changes a roster that has already been proposed.
func BroadcastOnlineCPUSettings() {
	mutex.Lock()
	var recipients []uint32
	for pid, session := range sessions {
		if session.LoggedIn && session.DeviceAuthenticated && session.CPUVersion == onlinecpu.ProtocolVersion {
			recipients = append(recipients, pid)
		}
	}
	mutex.Unlock()
	for _, pid := range recipients {
		sendOnlineCPUSettings(pid)
	}
}

func (g *GameSpySession) handleOnlineCPUReport(key, value string) {
	mutex.Lock()
	pid := g.User.ProfileId
	allowed := g.LoggedIn && g.DeviceAuthenticated && g.GameName == "mariokartwii" && g.CPUVersion == onlinecpu.ProtocolVersion && sessions[pid] == g
	mutex.Unlock()
	if !allowed || !OnlineCPUAvailable() {
		return
	}
	room, ok := qr2.OnlineCPURoomForProfile(pid)
	if !ok {
		return
	}
	now := time.Now()
	var value32 uint64
	var frame []byte
	var err error
	if key == "wl:cpu_frame" || key == "wl:ko_event" || key == "wl:cpu_item" {
		// Reject oversized reports before allocating/decoding attacker input.
		size := onlinecpu.FrameSize
		if key == "wl:ko_event" {
			size = onlinecpu.KnockoutEventSize
		} else if key == "wl:cpu_item" {
			size = onlinecpu.ItemStateSize
		}
		if len(value) != common.Base64DwcEncoding.EncodedLen(size) {
			return
		}
		frame, err = common.Base64DwcEncoding.DecodeString(value)
	} else {
		value32, err = strconv.ParseUint(value, 10, 32)
	}
	if err != nil {
		return
	}
	policy := cpuPolicy()
	cpuRelay.Lock()
	var deliveries []onlinecpu.Delivery
	switch key {
	case "wl:cpu_prepare":
		deliveries = cpuRelay.engine.Prepare(room, pid, uint32(value32), policy, now)
	case "wl:cpu_ack":
		deliveries = cpuRelay.engine.Ack(room, pid, uint32(value32), now)
	case "wl:cpu_frame":
		deliveries = cpuRelay.engine.Frame(room, pid, frame, now)
	case "wl:cpu_item":
		deliveries = cpuRelay.engine.ItemState(room, pid, frame, now)
	case "wl:ko_event":
		deliveries = cpuRelay.engine.KnockoutEvent(room, pid, frame, now)
	case "wl:cpu_ping":
		deliveries = cpuRelay.engine.Ping(room, pid, uint32(value32), now)
	case "wl:cpu_grid_error":
		deliveries = cpuRelay.engine.GridError(room, pid, uint32(value32))
	}
	cpuRelay.Unlock()
	deliverOnlineCPU(deliveries)
}

func deliverOnlineCPU(deliveries []onlinecpu.Delivery) {
	for _, delivery := range deliveries {
		message := delivery.Message
		if delivery.Frame != nil {
			message = "LCPU3:F:" + common.Base64DwcEncoding.EncodeToString(delivery.Frame)
		}
		if delivery.Knockout != nil {
			message = "LCPU3:E:" + common.Base64DwcEncoding.EncodeToString(delivery.Knockout)
		}
		if delivery.ItemState != nil {
			message = "LCPU3:I:" + common.Base64DwcEncoding.EncodeToString(delivery.ItemState)
		}
		if len(message) >= 256 || strings.ContainsAny(message, "\x00\\|") {
			continue
		}
		if strings.HasPrefix(message, "LCPU3:P:") || strings.HasPrefix(message, "LCPU3:C:") || strings.HasPrefix(message, "LCPU3:X:") || strings.HasPrefix(message, "LCPU3:E:") {
			logging.Info("GPCM:CPU", "Control:", message, "recipient:", delivery.To)
		}
		mutex.Lock()
		session := sessions[delivery.To]
		if session == nil || !session.LoggedIn || !session.DeviceAuthenticated || session.CPUVersion != onlinecpu.ProtocolVersion {
			mutex.Unlock()
			continue
		}
		index := session.ConnIndex
		mutex.Unlock()
		// Self-sender BM is reserved for this server channel: ordinary clients
		// cannot authorize themselves as a buddy or forge another connection's PID.
		packet := common.CreateGameSpyMessage(common.GameSpyCommand{Command: "bm", CommandValue: "1", OtherValues: map[string]string{
			"f": strconv.FormatUint(uint64(delivery.To), 10), "msg": message,
		}})
		if err := common.SendPacket(ServerName, index, []byte(packet)); err != nil {
			logging.Warn("GPCM:CPU", "Failed to deliver CPU control message:", err)
		}
	}
}

func disconnectOnlineCPU(pid uint32) {
	cpuRelay.Lock()
	deliveries := cpuRelay.engine.Disconnect(pid)
	cpuRelay.Unlock()
	deliverOnlineCPU(deliveries)
}

func startOnlineCPURelay() {
	if !OnlineCPUAvailable() {
		return
	}
	stopOnlineCPURelay()
	cpuRelay.Lock()
	cpuRelay.stop, cpuRelay.done = make(chan struct{}), make(chan struct{})
	stop, done := cpuRelay.stop, cpuRelay.done
	cpuRelay.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				cpuRelay.Lock()
				rooms := cpuRelay.engine.Rooms()
				cpuRelay.Unlock()
				var deliveries []onlinecpu.Delivery
				for _, old := range rooms {
					room, ok := qr2.OnlineCPURoomForProfile(old.OwnerPID)
					cpuRelay.Lock()
					if ok && room.Name == old.Name {
						deliveries = append(deliveries, cpuRelay.engine.Reconcile(room)...)
					} else {
						deliveries = append(deliveries, cpuRelay.engine.RemoveRoom(old.Name)...)
					}
					cpuRelay.Unlock()
				}
				cpuRelay.Lock()
				deliveries = append(deliveries, cpuRelay.engine.Expire(now)...)
				cpuRelay.Unlock()
				deliverOnlineCPU(deliveries)
			}
		}
	}()
}

func stopOnlineCPURelay() {
	cpuRelay.Lock()
	stop, done := cpuRelay.stop, cpuRelay.done
	cpuRelay.stop, cpuRelay.done = nil, nil
	cpuRelay.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}
