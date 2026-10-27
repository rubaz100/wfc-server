package gpsp

import (
	"bytes"
	"sync"
	"wwfc/common"
	"wwfc/gpcm"
	"wwfc/logging"
)

var ServerName = "gpsp"

var (
	bufferMutex sync.Mutex
	buffers     = map[uint64][]byte{}
)

func StartServer(reload bool) {
	if !reload {
		buffers = map[uint64][]byte{}
	}
}

func Shutdown() {
	bufferMutex.Lock()
	buffers = map[uint64][]byte{}
	bufferMutex.Unlock()
}

func NewConnection(index uint64, address string) {
	bufferMutex.Lock()
	delete(buffers, index)
	bufferMutex.Unlock()
}

func CloseConnection(index uint64) {
	bufferMutex.Lock()
	delete(buffers, index)
	bufferMutex.Unlock()
}

func HandlePacket(index uint64, data []byte) {
	bufferMutex.Lock()
	if len(buffers[index])+len(data) > 0x4000 {
		delete(buffers, index)
		bufferMutex.Unlock()
		_ = common.CloseConnection(ServerName, index)
		return
	}
	buffers[index] = append(buffers[index], data...)
	var messages []string
	for {
		end := bytes.Index(buffers[index], []byte(`\final\`))
		if end < 0 {
			break
		}
		messages = append(messages, string(buffers[index][:end+7]))
		buffers[index] = buffers[index][end+7:]
	}
	bufferMutex.Unlock()
	for _, message := range messages {
		handleMessage(index, message)
	}
}

func handleMessage(index uint64, message string) {
	moduleName := "GPSP"
	commands, err := common.ParseGameSpyMessage(message)
	if err != nil {
		logging.Error(moduleName, "Error parsing message:", err.Error())
		logging.Error(moduleName, "Raw data:", message)
		replyError(moduleName, index, gpcm.ErrParse)
		return
	}
	for _, command := range commands {
		switch command.Command {
		default:
			logging.Error(moduleName, "Unknown command:", command.Command)
			logging.Error(moduleName, "Raw data:", message)
			replyError(moduleName, index, gpcm.ErrParse)
		case "ka":
			err = common.SendPacket(ServerName, index, []byte(`\ka\\final\`))
		case "otherslist":
			err = common.SendPacket(ServerName, index, []byte(handleOthersList(command)))
		case "search":
			err = common.SendPacket(ServerName, index, []byte(handleSearch(command)))
		}
	}
	if err != nil {
		logging.Error(moduleName, "Failed to send packet:", err)
	}
}
