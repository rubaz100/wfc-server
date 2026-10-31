package gpcm

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
	"wwfc/common"
	"wwfc/database"
	"wwfc/internal/onlinecpu"
)

// Capture the real GameSpy serialization through the normal transport without
// opening a network socket or depending on a running game/server.
type cpuTestConnection struct{ bytes.Buffer }

func (c *cpuTestConnection) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *cpuTestConnection) Close() error                     { return nil }
func (c *cpuTestConnection) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *cpuTestConnection) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *cpuTestConnection) SetDeadline(time.Time) error      { return nil }
func (c *cpuTestConnection) SetReadDeadline(time.Time) error  { return nil }
func (c *cpuTestConnection) SetWriteDeadline(time.Time) error { return nil }

func setupCPUWireTest(t *testing.T) (*GameSpySession, *cpuTestConnection) {
	t.Helper()
	oldPath, oldProtocol := common.DataPath(), common.OnlineCPUProtocolVersion
	oldSessions, oldEngine := sessions, cpuRelay.engine
	if err := common.SetDataPath(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(common.ConfigPath(), []byte("<Config><onlineCPUEnabled>true</onlineCPUEnabled><onlineCPUCount>4</onlineCPUCount></Config>"), 0600); err != nil {
		t.Fatal(err)
	}
	common.OnlineCPUProtocolVersion = ""
	cpuRelay.engine = onlinecpu.New(100)
	session := &GameSpySession{ConnIndex: 1, User: database.User{ProfileId: 10}, LoggedIn: true, GameName: "mariokartwii", GameCode: "RMCPD00"}
	sessions = map[uint32]*GameSpySession{10: session}
	connection := &cpuTestConnection{}
	transport := common.NewDirectTransport()
	if err := transport.Add(ServerName, 1, connection); err != nil {
		t.Fatal(err)
	}
	common.UseDirectTransport(transport)
	t.Cleanup(func() {
		common.UseDirectTransport(nil)
		transport.CloseAll()
		sessions, cpuRelay.engine = oldSessions, oldEngine
		common.OnlineCPUProtocolVersion = oldProtocol
		_ = common.SetDataPath(oldPath)
	})
	return session, connection
}

func takeCPUMessage(t *testing.T, connection *cpuTestConnection) string {
	t.Helper()
	commands, err := common.ParseGameSpyMessage(connection.String())
	connection.Reset()
	if err != nil || len(commands) != 1 {
		t.Fatalf("CPU wire message = %+v, error:%v", commands, err)
	}
	command := commands[0]
	if command.Command != "bm" || command.CommandValue != "1" || command.OtherValues["f"] != "10" {
		t.Fatalf("CPU message did not use the recipient's self sender: %+v", command)
	}
	return command.OtherValues["msg"]
}

func TestCPUCapabilityRequiresVerifiedAuthenticationAndAdvertisesBuildGate(t *testing.T) {
	session, connection := setupCPUWireTest(t)
	command := common.GameSpyCommand{OtherValues: map[string]string{"wl:cpu_ver": "1"}}
	session.acceptOnlineCPUCapability(command)
	if session.CPUVersion != 0 || connection.Len() != 0 {
		t.Fatal("unverified capability was accepted")
	}
	session.DeviceAuthenticated = true
	session.acceptOnlineCPUCapability(command)
	if session.CPUVersion != 1 || takeCPUMessage(t, connection) != "LCPU1:S:0:1:4" {
		t.Fatal("normal server failed to advertise protocol unavailable")
	}
	common.OnlineCPUProtocolVersion = "1"
	if err := common.ApplyConfigChanges(map[string]string{"onlineCPUEnabled": "false", "onlineCPUCount": "6"}); err != nil {
		t.Fatal(err)
	}
	BroadcastOnlineCPUSettings()
	if message := takeCPUMessage(t, connection); message != "LCPU1:S:1:0:6" {
		t.Fatalf("candidate settings broadcast = %q", message)
	}
}

func TestCPUFrameWireUsesDWCBase64AndRejectsUnsafeMessages(t *testing.T) {
	session, connection := setupCPUWireTest(t)
	session.DeviceAuthenticated, session.CPUVersion = true, 1
	frame := make([]byte, onlinecpu.FrameSize)
	binary.BigEndian.PutUint32(frame[:4], 123)
	frame[8], frame[9] = 2, 1
	deliverOnlineCPU([]onlinecpu.Delivery{{To: 10, Frame: frame}})
	message := takeCPUMessage(t, connection)
	if !strings.HasPrefix(message, "LCPU1:F:") || len(message) >= 256 {
		t.Fatalf("invalid CPU frame envelope %q", message)
	}
	decoded, err := common.Base64DwcEncoding.DecodeString(strings.TrimPrefix(message, "LCPU1:F:"))
	if err != nil || !bytes.Equal(decoded, frame) {
		t.Fatalf("CPU frame changed during relay: %v", err)
	}
	for _, unsafe := range []string{"LCPU1:X:1\\final\\", "LCPU1:X:1\x00", strings.Repeat("x", 256)} {
		deliverOnlineCPU([]onlinecpu.Delivery{{To: 10, Message: unsafe}})
		if connection.Len() != 0 {
			t.Fatal("unsafe control message was serialized")
		}
	}
}
