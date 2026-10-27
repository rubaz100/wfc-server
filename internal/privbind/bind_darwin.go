//go:build darwin

package privbind

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type helperGreeting struct {
	Token string `json:"token"`
	Error string `json:"error,omitempty"`
}

func privilegedBind(ctx context.Context, specs []Spec, runtimeDir string) (*Sockets, error) {
	if err := validateRuntimeDir(runtimeDir, os.Getuid()); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	request := Request{Version: 1, ParentUID: os.Getuid(), Token: base64.RawURLEncoding.EncodeToString(tokenBytes), Sockets: specs}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 8192 {
		return nil, errors.New("privileged bind request is too large")
	}
	socketPath := filepath.Join(runtimeDir, fmt.Sprintf("bind-%d.sock", os.Getpid()))
	if len(socketPath) > 100 {
		return nil, errors.New("runtime directory path is too long for a macOS Unix socket")
	}
	_ = os.Remove(socketPath)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("create protected descriptor socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return nil, err
	}
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-stopWatch:
		}
	}()
	encodedRequest := base64.RawURLEncoding.EncodeToString(encoded)
	command := shellQuote(executable) + " __bind_helper --socket " + shellQuote(socketPath) + " --request " + shellQuote(encodedRequest)
	script := `do shell script "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(command) + `" with administrator privileges`
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start macOS authorization prompt: %w", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	sockets, err := receiveSockets(listener, request)
	if err != nil {
		_ = listener.Close()
		select {
		case <-wait:
		case <-time.After(2 * time.Second):
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if output.Len() != 0 {
			return nil, fmt.Errorf("privileged binder: %w: %s", err, strings.TrimSpace(output.String()))
		}
		return nil, err
	}
	// The helper stays alive holding the descriptors until we close the control
	// connection (receiveSockets registered that close on the Sockets); closing
	// the listening sockets early would drain their accept queues and break the
	// sockets we just received. Reap the helper when the Sockets are closed, and
	// close the Sockets if the context is cancelled.
	sockets.addCloser(func() {
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
		}
	})
	go func() {
		<-ctx.Done()
		sockets.CloseAll()
	}()
	return sockets, nil
}

func receiveSockets(listener *net.UnixListener, request Request) (*Sockets, error) {
	_ = listener.SetDeadline(time.Now().Add(120 * time.Second))
	connection, err := listener.AcceptUnix()
	if err != nil {
		return nil, fmt.Errorf("accept descriptor connection: %w", err)
	}
	// On success the control connection is kept open and handed to the Sockets
	// (closing it tells the helper to release the descriptors); close it here
	// only if we fail before that hand-off.
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, err
	}
	var greeting helperGreeting
	line := make([]byte, 0, 256)
	for len(line) == 0 || line[len(line)-1] != '\n' {
		if len(line) >= 2048 {
			return nil, errors.New("invalid privileged helper greeting")
		}
		var next [1]byte
		if _, err := io.ReadFull(connection, next[:]); err != nil {
			return nil, errors.New("invalid privileged helper greeting")
		}
		line = append(line, next[0])
	}
	if err := json.Unmarshal(line, &greeting); err != nil || subtle.ConstantTimeCompare([]byte(greeting.Token), []byte(request.Token)) != 1 {
		return nil, errors.New("privileged helper authentication failed")
	}
	if greeting.Error != "" {
		return nil, errors.New(greeting.Error)
	}
	payload := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4*len(request.Sockets)))
	_, oobLength, _, _, err := connection.ReadMsgUnix(payload, oob)
	if err != nil {
		return nil, fmt.Errorf("receive bound descriptors: %w", err)
	}
	messages, err := unix.ParseSocketControlMessage(oob[:oobLength])
	if err != nil || len(messages) != 1 {
		return nil, errors.New("invalid descriptor control message")
	}
	if messages[0].Header.Level != unix.SOL_SOCKET || messages[0].Header.Type != unix.SCM_RIGHTS {
		return nil, errors.New("helper did not send file descriptors")
	}
	descriptors, err := unix.ParseUnixRights(&messages[0])
	if err != nil || len(descriptors) != len(request.Sockets) {
		return nil, errors.New("incorrect number of bound descriptors")
	}
	sockets := newSockets()
	closeDescriptors := func() {
		for _, descriptor := range descriptors {
			if descriptor >= 0 {
				_ = unix.Close(descriptor)
			}
		}
	}
	for index, spec := range request.Sockets {
		file := os.NewFile(uintptr(descriptors[index]), spec.Name)
		if file == nil {
			closeDescriptors()
			sockets.CloseAll()
			return nil, errors.New("invalid received descriptor")
		}
		var socket boundSocket
		if spec.Network == "tcp" {
			socket.listener, err = net.FileListener(file)
		} else {
			socket.packet, err = net.FilePacketConn(file)
		}
		_ = file.Close()
		descriptors[index] = -1
		if err != nil || socketAddress(socket) == "" || !matchingAddress(spec.Address, socketAddress(socket)) {
			closeDescriptors()
			sockets.CloseAll()
			return nil, fmt.Errorf("received descriptor does not match %s %s", spec.Network, spec.Address)
		}
		if err := sockets.add(spec.Name, socket); err != nil {
			closeDescriptors()
			sockets.CloseAll()
			return nil, err
		}
	}
	// Hand the control connection to the caller via the Sockets: closing it on
	// CloseAll signals the helper to release the listening sockets and exit.
	// Clear the deadline so holding it open does not later time out.
	_ = connection.SetDeadline(time.Time{})
	sockets.addCloser(func() { _ = connection.Close() })
	success = true
	return sockets, nil
}

func socketAddress(socket boundSocket) string {
	if socket.listener != nil {
		return socket.listener.Addr().String()
	}
	if socket.packet != nil {
		return socket.packet.LocalAddr().String()
	}
	return ""
}

func matchingAddress(requested, actual string) bool {
	requestHost, requestPort, requestErr := net.SplitHostPort(requested)
	actualHost, actualPort, actualErr := net.SplitHostPort(actual)
	if requestErr != nil || actualErr != nil || !net.ParseIP(requestHost).Equal(net.ParseIP(actualHost)) {
		return false
	}
	return requestPort == actualPort || requestPort == "0"
}

func HelperMain(arguments []string) int {
	flags := flag.NewFlagSet("__bind_helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketPath := flags.String("socket", "", "")
	encoded := flags.String("request", "", "")
	if flags.Parse(arguments) != nil || flags.NArg() != 0 || len(*encoded) > 12000 || os.Geteuid() != 0 {
		return 1
	}
	data, err := base64.RawURLEncoding.DecodeString(*encoded)
	if err != nil || len(data) > 8192 {
		return 1
	}
	var request Request
	if json.Unmarshal(data, &request) != nil || request.Validate() != nil || validateRuntimeDir(filepath.Dir(*socketPath), request.ParentUID) != nil {
		return 1
	}
	if err := runHelper(request, *socketPath); err != nil {
		return 1
	}
	return 0
}

func runHelper(request Request, socketPath string) error {
	connection, err := net.DialTimeout("unix", socketPath, 10*time.Second)
	if err != nil {
		return err
	}
	unixConnection := connection.(*net.UnixConn)
	defer unixConnection.Close()
	_ = unixConnection.SetDeadline(time.Now().Add(30 * time.Second))
	files := make([]*os.File, 0, len(request.Sockets))
	for _, spec := range request.Sockets {
		file, err := bindRaw(spec)
		if err != nil {
			for _, opened := range files {
				_ = opened.Close()
			}
			message, _ := json.Marshal(helperGreeting{Token: request.Token, Error: err.Error()})
			_, _ = unixConnection.Write(append(message, '\n'))
			return err
		}
		files = append(files, file)
	}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	message, _ := json.Marshal(helperGreeting{Token: request.Token})
	if _, err := unixConnection.Write(append(message, '\n')); err != nil {
		return err
	}
	rights := make([]byte, 4*len(files))
	for index, file := range files {
		binary.NativeEndian.PutUint32(rights[index*4:], uint32(file.Fd()))
	}
	if _, _, err = unixConnection.WriteMsgUnix([]byte{byte(len(files))}, unix.UnixRights(intSlice(rights)...), nil); err != nil {
		return err
	}
	// Keep the listening sockets open until the parent closes the control
	// connection. On macOS/BSD, closing a listening socket drains and RSTs its
	// accept queue, and the dup the parent received does not keep that queue
	// alive; releasing the descriptors now would break the parent's sockets.
	// Block (holding the descriptors via the deferred close above) until the
	// parent drops the connection, then return so the deferred closes run.
	_ = unixConnection.SetReadDeadline(time.Time{})
	hold := make([]byte, 1)
	for {
		if _, err := unixConnection.Read(hold); err != nil {
			return nil
		}
	}
}

func intSlice(data []byte) []int {
	result := make([]int, len(data)/4)
	for index := range result {
		result[index] = int(int32(binary.NativeEndian.Uint32(data[index*4:])))
	}
	return result
}

func bindRaw(spec Spec) (*os.File, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}
	host, portText, _ := net.SplitHostPort(spec.Address)
	ip := net.ParseIP(host).To4()
	port, _ := strconv.Atoi(portText)
	kind := unix.SOCK_DGRAM
	if spec.Network == "tcp" {
		kind = unix.SOCK_STREAM
	}
	descriptor, err := unix.Socket(unix.AF_INET, kind, 0)
	if err == nil {
		unix.CloseOnExec(descriptor)
	}
	if err != nil {
		return nil, fmt.Errorf("bind %s %s: %w", spec.Network, spec.Address, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = unix.Close(descriptor)
		}
	}()
	address := &unix.SockaddrInet4{Port: port}
	copy(address.Addr[:], ip)
	if err := unix.Bind(descriptor, address); err != nil {
		return nil, fmt.Errorf("bind %s %s: %w", spec.Network, spec.Address, err)
	}
	if spec.Network == "tcp" {
		if err := unix.Listen(descriptor, 128); err != nil {
			return nil, err
		}
	}
	closeOnError = false
	return os.NewFile(uintptr(descriptor), spec.Name), nil
}

func validateRuntimeDir(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect protected runtime directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("runtime directory must be a real directory with mode 0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return errors.New("runtime directory must be owned by the unprivileged user")
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
