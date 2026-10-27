package common

import (
	"fmt"
	"io"
	"net"
	"sync"
)

type directConnection struct {
	conn      net.Conn
	writeLock sync.Mutex
	closeOnce sync.Once
}

type DirectTransport struct {
	mutex       sync.RWMutex
	connections map[string]map[uint64]*directConnection
}

func NewDirectTransport() *DirectTransport {
	return &DirectTransport{connections: map[string]map[uint64]*directConnection{}}
}

func (t *DirectTransport) Add(server string, index uint64, conn net.Conn) error {
	if conn == nil {
		return fmt.Errorf("nil connection")
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.connections[server] == nil {
		t.connections[server] = map[uint64]*directConnection{}
	}
	if _, exists := t.connections[server][index]; exists {
		return fmt.Errorf("connection %s/%d already exists", server, index)
	}
	t.connections[server][index] = &directConnection{conn: conn}
	return nil
}

func (t *DirectTransport) SendPacket(server string, index uint64, data []byte) error {
	t.mutex.RLock()
	connection := t.connections[server][index]
	t.mutex.RUnlock()
	if connection == nil {
		return net.ErrClosed
	}
	connection.writeLock.Lock()
	defer connection.writeLock.Unlock()
	for len(data) > 0 {
		n, err := connection.conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (t *DirectTransport) CloseConnection(server string, index uint64) error {
	t.mutex.Lock()
	connection := t.connections[server][index]
	t.mutex.Unlock()
	if connection == nil {
		return nil
	}
	connection.closeOnce.Do(func() {
		t.mutex.Lock()
		if t.connections[server][index] == connection {
			delete(t.connections[server], index)
		}
		t.mutex.Unlock()
		_ = connection.conn.Close()
	})
	return nil
}

func (t *DirectTransport) CloseAll() {
	t.mutex.RLock()
	var connections []*directConnection
	for _, byIndex := range t.connections {
		for _, connection := range byIndex {
			connections = append(connections, connection)
		}
	}
	t.mutex.RUnlock()
	for _, connection := range connections {
		connection.closeOnce.Do(func() {
			_ = connection.conn.Close()
		})
	}
	t.mutex.Lock()
	t.connections = map[string]map[uint64]*directConnection{}
	t.mutex.Unlock()
}
