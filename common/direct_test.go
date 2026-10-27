package common

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestDirectTransportConcurrentWritesAndClose(t *testing.T) {
	server, client := net.Pipe()
	transport := NewDirectTransport()
	if err := transport.Add("test", 1, server); err != nil {
		t.Fatal(err)
	}
	if err := transport.Add("test", 1, server); err == nil {
		t.Fatal("duplicate connection registration succeeded")
	}
	const count = 40
	payload := bytes.Repeat([]byte("0123456789abcdef"), 128)
	received := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, len(payload)*count)
		_, _ = io.ReadFull(client, buffer)
		received <- buffer
	}()
	var writers sync.WaitGroup
	for index := 0; index < count; index++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			if err := transport.SendPacket("test", 1, payload); err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	data := <-received
	for offset := 0; offset < len(data); offset += len(payload) {
		if !bytes.Equal(data[offset:offset+len(payload)], payload) {
			t.Fatal("concurrent writes were interleaved")
		}
	}
	transport.CloseAll()
	if err := transport.SendPacket("test", 1, payload); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after close = %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection remained open after CloseAll")
	}
	transport.CloseAll()
	_ = client.Close()
}
