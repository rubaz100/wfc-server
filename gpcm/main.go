package gpcm

import (
	"bytes"
	"encoding/gob"
	"os"
	"strings"
	"sync/atomic"
	"wwfc/common"
	"wwfc/database"
	"wwfc/logging"
	"wwfc/qr2"

	"github.com/linkdata/deadlock"
	"github.com/logrusorgru/aurora/v3"
)

const ServerName = "gpcm"

type GameSpySession struct {
	ConnIndex           uint64
	RemoteAddr          string
	User                database.User
	ModuleName          string
	LoggedIn            bool
	DeviceAuthenticated bool
	Challenge           string
	AuthToken           string
	LoginTicket         string
	SessionKey          int32

	LoginInfoSet      bool
	GameName          string
	GameCode          string
	Region            byte
	Language          byte
	InGameName        string
	ConsoleFriendCode uint64
	DeviceId          uint32
	HostPlatform      string
	UnitCode          byte

	StatusSet      bool
	Status         string
	LocString      string
	FriendList     []uint32
	AuthFriendList []uint32
	// For syncing with local GS SDK buddy list
	RecvStatusFromList []uint32

	QR2IP          uint64
	Reservation    common.MatchCommandData
	ReservationPID uint32

	NeedsExploit         bool
	CPUVersion           uint32
	KnockdownPoolVersion uint32
	PenaltyResetVersion  uint32
	InactivityVersion    uint32

	ReadBuffer  []byte
	WriteBuffer string
}

var (
	db database.Connection

	// I would use a sync.Map instead of the map mutex combo, but this performs better.
	sessions            = map[uint32]*GameSpySession{}
	sessionsByConnIndex = map[uint64]*GameSpySession{}
	mutex               = deadlock.Mutex{}

	allowDefaultDolphinKeys atomic.Bool
)

func StartServer(reload bool) {
	qr2.SetGPErrorCallback(KickPlayer)
	startOnlineCPURelay()

	// Get config
	config := common.GetConfig()

	// Start SQL
	db = database.Start(config)
	db.UpdateTables()

	allowDefaultDolphinKeys.Store(config.AllowDefaultDolphinKeys)

	if reload {
		err := loadState()
		if err != nil {
			logging.Error("GPCM", "Failed to load state:", err)
			os.Exit(1)
		}

		logging.Notice("GPCM", "Loaded", aurora.Cyan(len(sessions)), "sessions")
	}

	db.RegisterEvents(config, []string{
		"profile_created",
		"logged_in",
		"logged_out",
		"received_login_info",
		"device_authenticated",
		"reported_bad_packet",
		"reported_stall",
		"vs_watchdog",
		"gpcm_returned_error",
	})
}

func Shutdown() {
	stopOnlineCPURelay()
	err := saveState()
	if err != nil {
		logging.Error("GPCM", "Failed to save state:", err)
	}

	db.Close()

	logging.Notice("GPCM", "Saved", aurora.Cyan(len(sessions)), "sessions")
}

func CloseConnection(index uint64) {
	mutex.Lock()
	session := sessionsByConnIndex[index]
	mutex.Unlock()

	if session == nil {
		logging.Error("GPCM", "Cannot find session for this connection index:", aurora.Cyan(index))
		return
	}

	logging.Notice(session.ModuleName, "Connection closed")

	if session.LoggedIn {
		disconnectOnlineCPU(session.User.ProfileId)
		qr2.Logout(session.User.ProfileId)
		if session.QR2IP != 0 {
			qr2.ProcessGPStatusUpdate(session.User.ProfileId, session.QR2IP, "0")
		}
		session.sendLogoutStatus()

		logging.Event("logged_out", map[string]any{
			"profile_id": session.User.ProfileId,
		})
	}

	mutex.Lock()
	defer mutex.Unlock()

	if session.LoggedIn {
		session.LoggedIn = false
		delete(sessions, session.User.ProfileId)
	}
}

func NewConnection(index uint64, address string) {
	session := &GameSpySession{
		ConnIndex:      index,
		RemoteAddr:     address,
		User:           database.User{},
		ModuleName:     "GPCM:" + address,
		LoggedIn:       false,
		Challenge:      common.RandomString(10),
		StatusSet:      false,
		Status:         "",
		LocString:      "",
		FriendList:     []uint32{},
		AuthFriendList: []uint32{},
	}

	payload := common.CreateGameSpyMessage(common.GameSpyCommand{
		Command:      "lc",
		CommandValue: "1",
		OtherValues: map[string]string{
			"challenge": session.Challenge,
			"id":        "1",
		},
	})
	if err := common.SendPacket(ServerName, index, []byte(payload)); err != nil {
		logging.Error("GPCM", "Failed to send login challenge packet:", err)
		_ = common.CloseConnection(ServerName, index)
		return
	}

	logging.Notice(session.ModuleName, "Connection established from", address)

	mutex.Lock()
	sessionsByConnIndex[index] = session
	mutex.Unlock()
}

// recordKind identifies only protocol names for malformed-input diagnostics.
// Values are excluded because login records and messages can contain secrets.
func recordKind(record []byte) string {
	if len(record) < 2 || record[0] != '\\' {
		return "invalid"
	}
	end := bytes.IndexByte(record[1:], '\\')
	if end < 1 || end > 32 {
		return "invalid"
	}
	command := string(record[1 : end+1])
	switch command {
	case "login", "wl:exlogin", "status", "bm", "ka", "logout", "updatepro", "getprofile", "addbuddy", "delbuddy", "authadd":
		return command
	case "wl:report":
		prefix := []byte("\\wl:report\\\\")
		if !bytes.HasPrefix(record, prefix) {
			return command
		}
		rest := record[len(prefix):]
		end = bytes.IndexByte(rest, '\\')
		if end < 1 || end > 48 {
			return command
		}
		key := string(rest[:end])
		switch key {
		case "wl:cpu_prepare", "wl:cpu_ack", "wl:cpu_frame", "wl:cpu_item", "wl:cpu_ping", "wl:cpu_grid_error", "wl:ko_event", "wl:race_live", "wl:race_map", "wl:race_diag", "wl:ko_pos", "wl:ko_eliminate", "wl:ko_apply", "wl:ko_spectate", "wl:ko_result", "wl:ko_afk", "wl:mkw_finish_time":
			return command + "/" + key
		}
		return command + "/other"
	default:
		return "other"
	}
}

func HandlePacket(index uint64, data []byte) {
	mutex.Lock()
	session := sessionsByConnIndex[index]
	mutex.Unlock()

	if session == nil {
		logging.Error("GPCM", "Cannot find session for this connection index:", aurora.Cyan(index))
		_ = common.CloseConnection(ServerName, index)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			logging.Error(session.ModuleName, "Panic:", r)
		}
	}()

	session.ReadBuffer = append(session.ReadBuffer, data...)
	const maxRecordSize = 0x4000
	const terminator = "\\final\\"
	completeEnd := 0
	for start := 0; start < len(session.ReadBuffer); {
		record := session.ReadBuffer[start:]
		end := bytes.Index(record, []byte(terminator))
		if end >= 0 {
			record = record[:end+len(terminator)]
		}
		// Limit logical records, not a coalesced TCP batch. An incomplete
		// following report must not hold already-complete telemetry back.
		if len(record) > maxRecordSize {
			logging.Error(session.ModuleName, "GameSpy record exceeds byte limit:", recordKind(record), "bytes:", len(record))
			session.ReadBuffer = nil
			session.replyError(ErrParse)
			return
		}
		if offset := bytes.IndexByte(record, 0); offset >= 0 {
			logging.Error(session.ModuleName, "Null byte in GameSpy record:", recordKind(record), "record offset:", offset, "record bytes:", len(record), "buffer offset:", start+offset, "TCP chunk bytes:", len(data))
			session.ReadBuffer = nil
			session.replyError(ErrParse)
			return
		}
		if record[0] != '\\' {
			logging.Error(session.ModuleName, "Invalid GameSpy record start; record bytes:", len(record), "buffer offset:", start)
			session.ReadBuffer = nil
			session.replyError(ErrParse)
			return
		}
		if end < 0 {
			break
		}
		completeEnd = start + len(record)
		start = completeEnd
	}
	if completeEnd == 0 {
		return
	}
	complete := session.ReadBuffer[:completeEnd]
	// Retain only the unfinished tail in its own allocation. Parsed data may
	// include credentials and must not remain in the session buffer.
	session.ReadBuffer = append([]byte(nil), session.ReadBuffer[completeEnd:]...)

	builder := strings.Builder{}
	builder.Grow(len(complete))

	// Copy one rune at a time to enforce ASCII (rather than UTF-8)
	for _, value := range complete {
		builder.WriteRune(rune(value))
	}

	message := builder.String()

	commands, err := common.ParseGameSpyMessage(message)
	if err != nil {
		logging.Error(session.ModuleName, "Error parsing GameSpy records:", err.Error(), "complete bytes:", len(complete), "first record:", recordKind(complete))
		session.ReadBuffer = nil
		session.replyError(ErrParse)
		return
	}

	// Commands must be handled in a certain order, not in the order supplied by the client

	commands = session.handleCommand("ka", commands, func(command common.GameSpyCommand) {
		_ = common.SendPacket(ServerName, session.ConnIndex, []byte(`\ka\\final\`))
	})
	commands = session.handleCommand("login", commands, session.login)
	commands = session.handleCommand("wl:exlogin", commands, session.exLogin)
	commands = session.ignoreCommand("logout", commands)

	if len(commands) != 0 && !session.LoggedIn {
		logging.Error(session.ModuleName, "Attempt to run command before login:", aurora.Cyan(commands[0]))
		session.replyError(ErrNotLoggedIn)
		return
	}

	commands = session.handleCommand("wl:report", commands, session.handleWWFCReport)
	commands = session.handleCommand("updatepro", commands, session.updateProfile)
	commands = session.handleCommand("status", commands, session.setStatus)
	commands = session.handleCommand("addbuddy", commands, session.addFriend)
	commands = session.handleCommand("delbuddy", commands, session.removeFriend)
	commands = session.handleCommand("authadd", commands, session.authAddFriend)
	commands = session.handleCommand("bm", commands, session.bestieMessage)
	commands = session.handleCommand("getprofile", commands, session.getProfile)

	for _, command := range commands {
		logging.Error(session.ModuleName, "Unknown command:", aurora.Cyan(command))
	}

	if session.WriteBuffer != "" {
		data := []byte{}
		logged := false
		for c := 0; c < len(session.WriteBuffer); c++ {
			if session.WriteBuffer[c] == 0x00 {
				if !logged {
					logging.Warn(session.ModuleName, "Non-char or null byte in response packet:", session.WriteBuffer)
					logged = true
				}
				continue
			}

			data = append(data, session.WriteBuffer[c])
		}

		if err := common.SendPacket(ServerName, session.ConnIndex, data); err != nil {
			logging.Error(session.ModuleName, "Failed to send response packet:", err)
		} else {
			session.WriteBuffer = ""
		}
	}
}

func (g *GameSpySession) handleCommand(name string, commands []common.GameSpyCommand, handler func(command common.GameSpyCommand)) []common.GameSpyCommand {
	var unhandled []common.GameSpyCommand

	for _, command := range commands {
		if command.Command != name {
			unhandled = append(unhandled, command)
			continue
		}

		logging.Info(g.ModuleName, "Command:", aurora.Yellow(command.Command))
		handler(command)
	}

	return unhandled
}

func (g *GameSpySession) ignoreCommand(name string, commands []common.GameSpyCommand) []common.GameSpyCommand {
	var unhandled []common.GameSpyCommand

	for _, command := range commands {
		if command.Command != name {
			unhandled = append(unhandled, command)
		}
	}

	return unhandled
}

func saveState() error {
	file, err := os.OpenFile(common.DataPath("state/gpcm_sessions.gob"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	encoder := gob.NewEncoder(file)

	mutex.Lock()
	defer mutex.Unlock()

	err = encoder.Encode(sessions)
	common.ShouldNotError(file.Close())
	return err
}

func loadState() error {
	file, err := os.Open(common.DataPath("state/gpcm_sessions.gob"))
	if err != nil {
		return err
	}

	decoder := gob.NewDecoder(file)

	mutex.Lock()
	defer mutex.Unlock()

	err = decoder.Decode(&sessions)
	common.ShouldNotError(file.Close())
	if err != nil {
		return err
	}

	for _, session := range sessions {
		sessionsByConnIndex[session.ConnIndex] = session
	}

	return nil
}
