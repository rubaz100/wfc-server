package qr2

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"wwfc/common"
	"wwfc/database"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

const (
	QueryRequest            = 0x00
	ChallengeRequest        = 0x01
	EchoRequest             = 0x02
	HeartbeatRequest        = 0x03
	AddErrorRequest         = 0x04
	EchoResponseRequest     = 0x05
	ClientMessageRequest    = 0x06
	ClientMessageAckRequest = 0x07
	KeepAliveRequest        = 0x08
	AvailableRequest        = 0x09
	ClientRegisteredReply   = 0x0A

	// Added by WiiLink
	ClientExploitReply = 0x10
)

var (
	masterConn net.PacketConn
	inShutdown atomic.Bool
	waitGroup  = sync.WaitGroup{}
)

func StartServer(reload bool) {
	if err := StartServerWithPacketConn(reload, nil); err != nil {
		panic(err)
	}
}

func StartServerWithPacketConn(reload bool, conn net.PacketConn) error {
	config := common.GetConfig()
	if conn == nil {
		var err error
		conn, err = net.ListenPacket("udp", *config.GameSpyAddress+":27900")
		if err != nil {
			return err
		}
	}
	if config.EventReporting.LogToDatabase {
		db := database.Start(config)
		db.RegisterEvents(config, []string{
			"group_created", "group_deleted", "group_joined", "group_left",
			"group_host_changed", "natneg_succeeded", "natneg_failed",
		})
	}
	masterConn = conn
	inShutdown.Store(false)
	if reload {
		if err := loadSessions(); err != nil {
			return err
		}
		if err := loadLogins(); err != nil {
			return err
		}
		if err := loadGroups(); err != nil {
			return err
		}
		logging.Notice("QR2", "Loaded sessions, logins and groups")
	}
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		defer conn.Close()
		logging.Notice("QR2", "Listening on", aurora.BrightCyan(conn.LocalAddr().String()))
		for {
			if inShutdown.Load() {
				return
			}
			buffer := make([]byte, 2048)
			n, address, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			if n < 5 {
				continue
			}
			addr, ok := address.(*net.UDPAddr)
			if !ok {
				continue
			}
			handleConnection(conn, *addr, buffer, n)
		}
	}()
	return nil
}

func Shutdown() {
	if masterConn == nil {
		return
	}
	inShutdown.Store(true)
	_ = masterConn.Close()
	waitGroup.Wait()
	masterConn = nil
	mutex.Lock()
	defer mutex.Unlock()
	if err := saveSessions(); err != nil {
		logging.Error("QR2", "Failed to save sessions:", err)
	}
	if err := saveLogins(); err != nil {
		logging.Error("QR2", "Failed to save logins:", err)
	}
	if err := saveGroups(); err != nil {
		logging.Error("QR2", "Failed to save groups:", err)
	}
}

func handleConnection(conn net.PacketConn, addr net.UDPAddr, buffer []byte, n int) {
	packetType := buffer[0]
	moduleName := "QR2:" + addr.String()

	var session *Session
	if packetType != HeartbeatRequest && packetType != AvailableRequest {
		mutex.Lock()

		var ok bool
		session, ok = sessions[makeLookupAddr(addr.String())]
		if !ok {
			mutex.Unlock()
			logging.Error(moduleName, "Cannot find session for this IP address")
			return
		}

		session.SessionID = binary.BigEndian.Uint32(buffer[1:5])

		mutex.Unlock()
	}

	switch packetType {
	case QueryRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("QUERY"))

	case ChallengeRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("CHALLENGE"))

		mutex.Lock()
		if session.Challenge != "" {
			// TODO: Verify the challenge
			session.Authenticated = true
			mutex.Unlock()

			_, _ = conn.WriteTo(createResponseHeader(ClientRegisteredReply, session.SessionID), &addr)
		} else {
			mutex.Unlock()
		}

	case EchoRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("ECHO"))

	case HeartbeatRequest:
		// logging.Info(moduleName, "Command:", aurora.Yellow("HEARTBEAT"))
		heartbeat(moduleName, conn, addr, buffer)

	case AddErrorRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("ADDERROR"))

	case EchoResponseRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("ECHO_RESPONSE"))

	case ClientMessageRequest:
		logging.Info(moduleName, "Command:", aurora.Yellow("CLIENT_MESSAGE"))
		return

	case ClientMessageAckRequest:
		// logging.Info(moduleName, "Command:", aurora.Yellow("CLIENT_MESSAGE_ACK"))

		// In case ClientExploitReply is lost, this can be checked as well
		// This would be sent either after the payload is downloaded, or the client is already patched
		session.ExploitReceived = true
		if login := session.login; login != nil {
			login.NeedsExploit = false
		}

		session.messageAckWaker.Assert()
		return

	case KeepAliveRequest:
		// logging.Info(moduleName, "Command:", aurora.Yellow("KEEPALIVE"))
		_, _ = conn.WriteTo(createResponseHeader(KeepAliveRequest, 0), &addr)

		session.LastKeepAlive = time.Now().UTC().Unix()
		return

	case AvailableRequest:
		// Expect a null terminated string
		if n-1 < 5 || buffer[n-1] != 0 {
			return
		}

		// Only respond if the game name provided in the packet is valid
		gameName := string(buffer[5 : n-1])
		gameInfo := common.GetGameInfoByName(gameName)

		if gameInfo == nil {
			return
		}

		logging.Info("QR2", "Command:", aurora.Yellow("AVAILABLE"))
		_, _ = conn.WriteTo(createResponseHeader(AvailableRequest, 0), &addr)
		return

	case ClientRegisteredReply:
		logging.Info(moduleName, "Command:", aurora.Yellow("CLIENT_REGISTERED"))

	case ClientExploitReply:
		logging.Info(moduleName, "Command:", aurora.Yellow("CLIENT_EXPLOIT_ACK"))

		session.ExploitReceived = true
		if login := session.login; login != nil {
			login.NeedsExploit = false
		}

	default:
		logging.Error(moduleName, "Unknown command:", aurora.Yellow(buffer[0]))
		return
	}
}

func createResponseHeader(command byte, sessionId uint32) []byte {
	return binary.BigEndian.AppendUint32([]byte{0xfe, 0xfd, command}, sessionId)
}
