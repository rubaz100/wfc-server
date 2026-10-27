package privbind

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"sync"
)

type Spec struct {
	Name    string `json:"name"`
	Network string `json:"network"`
	Address string `json:"address"`
	Unsafe  bool   `json:"unsafe,omitempty"`
}

type Request struct {
	Version   int    `json:"version"`
	ParentUID int    `json:"parent_uid"`
	Token     string `json:"token"`
	Sockets   []Spec `json:"sockets"`
}

type boundSocket struct {
	listener net.Listener
	packet   net.PacketConn
}

type Sockets struct {
	mutex   sync.Mutex
	byName  map[string]boundSocket
	closed  bool
	closers []func()
}

func newSockets() *Sockets { return &Sockets{byName: map[string]boundSocket{}} }

// addCloser registers a function to run on CloseAll, after the sockets
// themselves are closed, in registration order. Used to release the privileged
// bind helper (close its control connection, then reap it). If the sockets are
// already closed, the closer runs immediately.
func (s *Sockets) addCloser(closer func()) {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		closer()
		return
	}
	s.closers = append(s.closers, closer)
	s.mutex.Unlock()
}

func (s *Sockets) add(name string, socket boundSocket) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		if socket.listener != nil {
			_ = socket.listener.Close()
		}
		if socket.packet != nil {
			_ = socket.packet.Close()
		}
		return net.ErrClosed
	}
	if _, exists := s.byName[name]; exists {
		return fmt.Errorf("duplicate socket %q", name)
	}
	s.byName[name] = socket
	return nil
}

func (s *Sockets) Listener(name string) (net.Listener, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	socket, exists := s.byName[name]
	if !exists || socket.listener == nil {
		return nil, fmt.Errorf("TCP listener %q is not available", name)
	}
	return socket.listener, nil
}

func (s *Sockets) PacketConn(name string) (net.PacketConn, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	socket, exists := s.byName[name]
	if !exists || socket.packet == nil {
		return nil, fmt.Errorf("UDP socket %q is not available", name)
	}
	return socket.packet, nil
}

func (s *Sockets) CloseAll() {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return
	}
	s.closed = true
	sockets := s.byName
	s.byName = map[string]boundSocket{}
	closers := s.closers
	s.closers = nil
	s.mutex.Unlock()
	for _, socket := range sockets {
		if socket.listener != nil {
			_ = socket.listener.Close()
		}
		if socket.packet != nil {
			_ = socket.packet.Close()
		}
	}
	for _, closer := range closers {
		closer()
	}
}

func (r Request) Validate() error {
	if r.Version != 1 || r.ParentUID < 0 || len(r.Token) < 32 || len(r.Token) > 128 {
		return fmt.Errorf("invalid privileged bind request")
	}
	if len(r.Sockets) == 0 || len(r.Sockets) > 16 {
		return fmt.Errorf("privileged bind requires between 1 and 16 sockets")
	}
	names := map[string]bool{}
	for _, spec := range r.Sockets {
		if err := ValidateSpec(spec); err != nil {
			return err
		}
		if names[spec.Name] {
			return fmt.Errorf("duplicate socket name %q", spec.Name)
		}
		names[spec.Name] = true
	}
	return nil
}

func ValidateSpec(spec Spec) error {
	if len(spec.Name) > 64 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(spec.Name) {
		return fmt.Errorf("invalid socket name %q", spec.Name)
	}
	if spec.Network != "tcp" && spec.Network != "udp" {
		return fmt.Errorf("socket %q must use tcp or udp", spec.Name)
	}
	if len(spec.Address) > 128 {
		return fmt.Errorf("socket address is too long")
	}
	host, port, err := net.SplitHostPort(spec.Address)
	if err != nil {
		return fmt.Errorf("socket %q address: %w", spec.Name, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("socket %q requires a numeric IPv4 address", spec.Name)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("socket %q has invalid port %q", spec.Name, port)
	}
	if !spec.Unsafe && !ip.IsPrivate() && !ip.IsLoopback() {
		return fmt.Errorf("socket %q refuses a public or wildcard address", spec.Name)
	}
	return nil
}
