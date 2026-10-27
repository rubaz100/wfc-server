// Package cli implements the mkwfc command-line interface (init, run, doctor).
//
// It lives in the fork (not in cmd/mkwfc) because it wires together the fork's
// internal packages (bootstrap, dnssrv, privbind, payload, certs), which Go's
// internal-import rule only permits from within this module. The product binary
// in the root mkwfc module, and the fork's own cmd/mkwfc, are both thin shims
// that call Main.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"wwfc/common"
	"wwfc/internal/bootstrap"
	"wwfc/internal/dnssrv"
	"wwfc/internal/privbind"
	"wwfc/logging"
	"wwfc/server"
)

type options struct {
	dataDir   string
	lanIP     string
	dnsPort   string
	httpPort  string
	httpsPort string
}

// Main is the mkwfc entry point. It returns a process exit code so callers can
// os.Exit on it.
func Main() int {
	if len(os.Args) > 1 && os.Args[1] == "__bind_helper" {
		return privbind.HelperMain(os.Args[2:])
	}
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mkwfc:", err)
		return 1
	}
	return 0
}

func runCLI(arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		printUsage(stdout)
		return nil
	}
	command := arguments[0]
	if command != "init" && command != "run" && command != "doctor" {
		if command == "help" || command == "--help" || command == "-h" {
			printUsage(stdout)
			return nil
		}
		return fmt.Errorf("unknown command %q; use init, run, or doctor", command)
	}
	defaultDirectory, err := common.DefaultDataDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options options
	flags.StringVar(&options.dataDir, "data-dir", defaultDirectory, "configuration and data directory")
	flags.StringVar(&options.lanIP, "lan-ip", "", "private IPv4 address for this LAN server")
	flags.StringVar(&options.dnsPort, "dns-port", "", "development DNS port override")
	flags.StringVar(&options.httpPort, "http-port", "", "development NAS HTTP port override")
	flags.StringVar(&options.httpsPort, "https-port", "", "development NAS HTTPS port override")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if options.lanIP == "" {
		address, err := dnssrv.DetectLANAddress()
		if err == nil {
			options.lanIP = address.String()
		} else if command == "init" {
			return err
		}
	}
	result, err := bootstrap.Ensure(options.dataDir, options.lanIP)
	if err != nil {
		return err
	}
	logging.SetLogDir(common.DataPath("logs"))
	if err := logging.SetOutput(result.Config.LogOutput); err != nil {
		return err
	}
	logging.SetLevel(*result.Config.LogLevel)
	switch command {
	case "init":
		fmt.Fprintln(stdout, "Data directory:", common.DataPath())
		fmt.Fprintln(stdout, "Host Manager:", "http://"+result.Config.AdminAddress+"/")
		fmt.Fprintln(stdout, "Gecko codes:", common.DataPath("gecko"))
		fmt.Fprintln(stdout, "The API secret is stored in config.xml; do not share it.")
		fmt.Fprintln(stdout, "Set your Wii DNS to", result.Config.LANAddress)
		return nil
	case "doctor":
		return runDoctor(commandContext(), result, options, stdout)
	case "run":
		return runServer(commandContext(), result, options, stdout)
	default:
		panic("unreachable")
	}
}

func applyOverrides(config *common.Config, options options) error {
	var err error
	if options.lanIP != "" {
		config.LANAddress, err = common.ValidatePrivateIPv4(options.lanIP)
		if err != nil {
			return err
		}
	}
	if _, err := common.ValidatePrivateIPv4(config.LANAddress); err != nil {
		return err
	}
	config.DefaultAddress = config.LANAddress
	*config.GameSpyAddress = config.LANAddress
	*config.NASAddress = config.LANAddress
	*config.NASAddressHTTPS = config.LANAddress
	if options.dnsPort != "" {
		config.DNSAddress = net.JoinHostPort(config.LANAddress, options.dnsPort)
	}
	if options.httpPort != "" {
		config.NASPort = options.httpPort
	}
	if options.httpsPort != "" {
		config.NASPortHTTPS = options.httpsPort
	}
	for _, port := range []string{config.NASPort, config.NASPortHTTPS} {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("invalid NAS port %q", port)
		}
	}
	return nil
}

func bindSpecs(config common.Config) ([]privbind.Spec, error) {
	dnsAddress, err := dnssrv.ValidateBindAddress(config.DNSAddress, config.AllowUnsafeBind)
	if err != nil {
		return nil, err
	}
	addresses := []privbind.Spec{
		{Name: "dns-udp", Network: "udp", Address: dnsAddress, Unsafe: config.AllowUnsafeBind},
		{Name: "dns-tcp", Network: "tcp", Address: dnsAddress, Unsafe: config.AllowUnsafeBind},
	}
	for _, item := range []struct {
		name, address string
	}{
		{"nas-http", net.JoinHostPort(*config.NASAddress, config.NASPort)},
		{"gpcm", net.JoinHostPort(*config.GameSpyAddress, "29900")},
		{"gpsp", net.JoinHostPort(*config.GameSpyAddress, "29901")},
		{"serverbrowser", net.JoinHostPort(*config.GameSpyAddress, "28910")},
		{"gamestats", net.JoinHostPort(*config.GameSpyAddress, "29920")},
		{"admin", config.AdminAddress},
	} {
		addresses = append(addresses, privbind.Spec{Name: item.name, Network: "tcp", Address: item.address, Unsafe: config.AllowUnsafeBind})
	}
	if config.EnableHTTPS {
		addresses = append(addresses, privbind.Spec{Name: "nas-https", Network: "tcp", Address: net.JoinHostPort(*config.NASAddressHTTPS, config.NASPortHTTPS), Unsafe: config.AllowUnsafeBind})
	}
	for _, item := range []struct {
		name, port string
	}{{"qr2", "27900"}, {"natneg", "27901"}} {
		addresses = append(addresses, privbind.Spec{Name: item.name, Network: "udp", Address: net.JoinHostPort(*config.GameSpyAddress, item.port), Unsafe: config.AllowUnsafeBind})
	}
	for _, spec := range addresses {
		if err := privbind.ValidateSpec(spec); err != nil {
			return nil, err
		}
	}
	return addresses, nil
}

func runServer(ctx context.Context, result bootstrap.Result, options options, stdout io.Writer) error {
	config := result.Config
	if err := applyOverrides(&config, options); err != nil {
		return err
	}
	specs, err := bindSpecs(config)
	if err != nil {
		return err
	}
	runtimeDir := filepath.Join(common.DataPath(), "run")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	sockets, err := privbind.Bind(ctx, specs, runtimeDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Set your Wii DNS to", config.LANAddress)
	fmt.Fprintln(stdout, "Dolphin: use the built-in BBA and set its DNS to this address.")
	fmt.Fprintln(stdout, "Host Manager:", "http://"+config.AdminAddress+"/")
	fmt.Fprintln(stdout, "Gecko codes for this server's key:", common.DataPath("gecko"))
	return server.RunSingle(ctx, config, server.SingleOptions{Catalog: result.Catalog, Sockets: sockets})
}

func commandContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: mkwfc <init|run|doctor> [--data-dir DIR] [--lan-ip IPv4]")
	fmt.Fprintln(writer, "Development overrides: --dns-port PORT --http-port PORT --https-port PORT")
}
