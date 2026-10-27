package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"wwfc/admin"
	"wwfc/api"
	"wwfc/common"
	"wwfc/gamestats"
	"wwfc/gpcm"
	"wwfc/gpsp"
	"wwfc/internal/dnssrv"
	"wwfc/internal/payload"
	"wwfc/internal/privbind"
	"wwfc/logging"
	"wwfc/nas"
	"wwfc/natneg"
	"wwfc/qr2"
	"wwfc/race"
	"wwfc/sake"
	"wwfc/serverbrowser"
)

type SingleOptions struct {
	Catalog *payload.Catalog
	Sockets *privbind.Sockets
}

type tcpService struct {
	name          string
	newConnection func(uint64, string)
	handlePacket  func(uint64, []byte, string)
	close         func(uint64)
}

func RunSingle(ctx context.Context, config common.Config, options SingleOptions) (returnError error) {
	if options.Catalog == nil {
		return errors.New("an embedded payload catalog is required")
	}
	if options.Sockets == nil {
		return errors.New("pre-bound sockets are required")
	}
	if err := os.MkdirAll(common.DataPath("state"), 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	transport := common.NewDirectTransport()
	common.UseDirectTransport(transport)
	defer common.UseDirectTransport(nil)

	started := []func(){}
	stopModules := func() {
		for index := len(started) - 1; index >= 0; index-- {
			started[index]()
		}
	}
	defer func() {
		options.Sockets.CloseAll()
		transport.CloseAll()
		stopModules()
		if recovered := recover(); recovered != nil {
			returnError = fmt.Errorf("single-process server: %v", recovered)
		}
	}()
	start := func(name string, action func(), shutdown func()) error {
		if err := callModule(action); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		started = append(started, shutdown)
		return nil
	}
	for _, module := range []struct {
		name     string
		start    func()
		shutdown func()
	}{
		{"GPCM", func() { gpcm.StartServer(false) }, gpcm.Shutdown},
		{"GPSP", func() { gpsp.StartServer(false) }, gpsp.Shutdown},
		{"server browser", func() { serverbrowser.StartServer(false) }, serverbrowser.Shutdown},
		{"Race", func() { race.StartServer(false) }, race.Shutdown},
		{"Sake", func() { sake.StartServer(false) }, sake.Shutdown},
		{"GameStats", func() { gamestats.StartServer(false) }, gamestats.Shutdown},
		{"API", func() { api.StartServer(false) }, api.Shutdown},
	} {
		if err := start(module.name, module.start, module.shutdown); err != nil {
			return err
		}
	}

	qr2Socket, err := options.Sockets.PacketConn("qr2")
	if err != nil {
		return err
	}
	qr2.SetExploitProvider(options.Catalog.SBCM)
	if err := qr2.StartServerWithPacketConn(false, qr2Socket); err != nil {
		return fmt.Errorf("start QR2: %w", err)
	}
	started = append(started, qr2.Shutdown)
	natnegSocket, err := options.Sockets.PacketConn("natneg")
	if err != nil {
		return err
	}
	if err := natneg.StartServerWithPacketConn(false, natnegSocket); err != nil {
		return fmt.Errorf("start NATNEG: %w", err)
	}
	started = append(started, natneg.Shutdown)
	httpSocket, err := options.Sockets.Listener("nas-http")
	if err != nil {
		return err
	}
	var httpsSocket net.Listener
	if config.EnableHTTPS {
		httpsSocket, err = options.Sockets.Listener("nas-https")
		if err != nil {
			return err
		}
	}
	if err := nas.StartServerWithListeners(options.Catalog, httpSocket, httpsSocket); err != nil {
		return fmt.Errorf("start NAS: %w", err)
	}
	started = append(started, nas.Shutdown)
	adminSocket, err := options.Sockets.Listener("admin")
	if err != nil {
		return err
	}
	if err := admin.StartServerWithListener(false, adminSocket); err != nil {
		return fmt.Errorf("start Host Manager: %w", err)
	}
	started = append(started, admin.Shutdown)

	runErrors := make(chan error, 8)
	dnsContext, cancelDNS := context.WithCancel(ctx)
	defer cancelDNS()
	dnsUDP, err := options.Sockets.PacketConn("dns-udp")
	if err != nil {
		return err
	}
	dnsTCP, err := options.Sockets.Listener("dns-tcp")
	if err != nil {
		return err
	}
	dnsServer, err := dnssrv.New(dnssrv.Config{
		Address: config.DNSAddress, LANAddress: config.LANAddress,
		LocalDomain: config.LocalDomain, Upstream: config.DNSUpstream,
		AllowUnsafe: config.AllowUnsafeBind,
	})
	if err != nil {
		return fmt.Errorf("configure DNS: %w", err)
	}
	dnsDone := make(chan error, 1)
	go func() { dnsDone <- dnsServer.Run(dnsContext, dnsUDP, dnsTCP) }()

	connectionLoops := sync.WaitGroup{}
	acceptLoops := sync.WaitGroup{}
	var connectionIndex atomic.Uint64
	services := []tcpService{
		{"serverbrowser", serverbrowser.NewConnection, serverbrowser.HandlePacket, serverbrowser.CloseConnection},
		{"gpcm", gpcm.NewConnection, func(index uint64, data []byte, _ string) { gpcm.HandlePacket(index, data) }, gpcm.CloseConnection},
		{"gpsp", gpsp.NewConnection, func(index uint64, data []byte, _ string) { gpsp.HandlePacket(index, data) }, gpsp.CloseConnection},
		{"gamestats", gamestats.NewConnection, func(index uint64, data []byte, _ string) { gamestats.HandlePacket(index, data) }, gamestats.CloseConnection},
	}
	for _, service := range services {
		listener, err := options.Sockets.Listener(service.name)
		if err != nil {
			return err
		}
		acceptLoops.Add(1)
		go func() {
			defer acceptLoops.Done()
			for {
				connection, err := listener.Accept()
				if err != nil {
					if !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
						select {
						case runErrors <- fmt.Errorf("%s accept: %w", service.name, err):
						default:
						}
					}
					return
				}
				index := connectionIndex.Add(1)
				if err := transport.Add(service.name, index, connection); err != nil {
					_ = connection.Close()
					continue
				}
				connectionLoops.Add(1)
				go func() {
					defer connectionLoops.Done()
					defer transport.CloseConnection(service.name, index)
					defer service.close(index)
					address := connection.RemoteAddr().String()
					service.newConnection(index, address)
					buffer := make([]byte, 4096)
					for {
						n, err := connection.Read(buffer)
						if n > 0 {
							service.handlePacket(index, buffer[:n], address)
						}
						if err != nil {
							return
						}
					}
				}()
			}
		}()
	}

	logging.Notice("MKWFC", "All services running in one process")
	select {
	case <-ctx.Done():
	case err := <-nas.Errors():
		returnError = err
	case err := <-dnsDone:
		if err != nil && ctx.Err() == nil {
			returnError = err
		}
	case err := <-runErrors:
		returnError = err
	}
	cancelDNS()
	options.Sockets.CloseAll()
	acceptLoops.Wait()
	transport.CloseAll()
	connectionLoops.Wait()
	select {
	case err := <-dnsDone:
		if returnError == nil && err != nil && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
			returnError = err
		}
	case <-time.After(5 * time.Second):
		if returnError == nil {
			returnError = errors.New("DNS shutdown timed out")
		}
	}
	return returnError
}

func callModule(action func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	action()
	return nil
}
