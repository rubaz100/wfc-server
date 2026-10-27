package privbind

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

func BindDirect(specs []Spec) (*Sockets, error) {
	sockets := newSockets()
	names := map[string]bool{}
	for _, spec := range specs {
		if err := ValidateSpec(spec); err != nil {
			sockets.CloseAll()
			return nil, err
		}
		if names[spec.Name] {
			sockets.CloseAll()
			return nil, fmt.Errorf("duplicate socket name %q", spec.Name)
		}
		names[spec.Name] = true
		var socket boundSocket
		var err error
		switch spec.Network {
		case "tcp":
			socket.listener, err = net.Listen("tcp", spec.Address)
		case "udp":
			socket.packet, err = net.ListenPacket("udp", spec.Address)
		}
		if err != nil {
			sockets.CloseAll()
			return nil, fmt.Errorf("bind %s %s: %w", spec.Network, spec.Address, err)
		}
		if err := sockets.add(spec.Name, socket); err != nil {
			sockets.CloseAll()
			return nil, err
		}
	}
	return sockets, nil
}

func Bind(ctx context.Context, specs []Spec, runtimeDir string) (*Sockets, error) {
	sockets, err := BindDirect(specs)
	if err == nil || !permissionError(err) {
		return sockets, err
	}
	privileged, helperErr := privilegedBind(ctx, specs, runtimeDir)
	if helperErr != nil {
		return nil, fmt.Errorf("direct bind: %v; privileged bind: %w", err, helperErr)
	}
	return privileged, nil
}

func permissionError(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}
