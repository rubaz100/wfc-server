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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"wwfc/common"
	"wwfc/internal/bootstrap"
	"wwfc/internal/certs"
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
	certPath  string
	keyPath   string
	wiiCert   string
	wiiKey    string
	appMode   bool
}

// certSources builds the pre-made certificate import set from the init flags.
func (o options) certSources() certs.Sources {
	return certs.Sources{ModernCert: o.certPath, ModernKey: o.keyPath, WiiCert: o.wiiCert, WiiKey: o.wiiKey}
}

// Main is the mkwfc entry point. It returns a process exit code so callers can
// os.Exit on it.
func Main() int {
	if len(os.Args) > 1 && os.Args[1] == "__bind_helper" {
		return privbind.HelperMain(os.Args[2:])
	}
	if code, handled := maybeElevate(os.Args); handled {
		return code
	}
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mkwfc:", err)
		return 1
	}
	return 0
}

func runCLI(arguments []string, stdout, stderr io.Writer) error {
	// A bare launch (a double-click with no arguments) runs the server as an
	// app: it opens the Host Manager in the browser and, on Linux, elevates.
	appMode := len(arguments) == 0
	if appMode {
		arguments = []string{"run"}
	}
	command := arguments[0]
	if command != "init" && command != "run" && command != "doctor" && command != "repair-certs" {
		if command == "help" || command == "--help" || command == "-h" {
			printUsage(stdout)
			return nil
		}
		return fmt.Errorf("unknown command %q; use init, run, doctor, or repair-certs", command)
	}
	defaultDirectory, err := common.DefaultDataDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options options
	options.appMode = appMode
	flags.StringVar(&options.dataDir, "data-dir", defaultDirectory, "configuration and data directory")
	flags.StringVar(&options.lanIP, "lan-ip", "", "override the automatically detected private LAN IPv4 address")
	flags.StringVar(&options.dnsPort, "dns-port", "", "development DNS port override")
	flags.StringVar(&options.httpPort, "http-port", "", "development NAS HTTP port override")
	flags.StringVar(&options.httpsPort, "https-port", "", "development NAS HTTPS port override")
	flags.StringVar(&options.certPath, "cert", "", "import this modern TLS certificate (PEM) instead of generating one")
	flags.StringVar(&options.keyPath, "key", "", "import this modern TLS private key (PEM) instead of generating one")
	flags.StringVar(&options.wiiCert, "wii-cert", "", "import this Wii NAS certificate (DER) instead of generating one")
	flags.StringVar(&options.wiiKey, "wii-key", "", "import this Wii NAS private key (PKCS#8 PEM) instead of generating one")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if command != "init" && options.certSources().Any() {
		return fmt.Errorf("--cert/--key/--wii-cert/--wii-key are only valid with init; the stored certificates are used otherwise")
	}
	if options.lanIP == "" && (command == "run" || command == "init") {
		address, err := dnssrv.DetectLANAddress()
		if err != nil {
			return fmt.Errorf("detect LAN interface: %w", err)
		}
		options.lanIP = address.String()
	}
	if options.lanIP != "" {
		address, err := common.ValidatePrivateIPv4(options.lanIP)
		if err != nil {
			return err
		}
		options.lanIP = address
	}
	if command == "run" {
		// Validate before first-run initialization or persisting a new binding.
		if err := checkLANAddressLocal(options.lanIP); err != nil {
			return err
		}
	}
	var imported *certs.Sources
	if command == "init" {
		if sources := options.certSources(); sources.Any() {
			imported = &sources
		}
	}
	result, err := bootstrap.Ensure(options.dataDir, options.lanIP, imported)
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
		fmt.Fprintln(stdout, "Initial admin password: MKWii (only if no password is stored; change it in Settings)")
		fmt.Fprintln(stdout, "Gecko codes:", common.DataPath("gecko"))
		fmt.Fprintln(stdout, "Set your Wii DNS to", result.Config.LANAddress)
		return nil
	case "repair-certs":
		changed, err := certs.RegenerateWiiCertificate(result.Config.CertPathWii, result.Config.KeyPathWii, time.Now())
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintln(stdout, "Replaced the Wii certificate using the existing private key.")
			fmt.Fprintln(stdout, "Original certificate:", result.Config.CertPathWii+".before-openssl-compat")
			fmt.Fprintln(stdout, "Restart mkwfc before retrying the console.")
		} else {
			fmt.Fprintln(stdout, "The Wii certificate already uses the compatible encoding; no files were changed.")
		}
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
	address := options.lanIP
	if address == "" {
		address = config.LANAddress
	}
	updated, err := common.WithLANAddress(*config, address)
	if err != nil {
		return err
	}
	if options.dnsPort != "" {
		host, _, err := net.SplitHostPort(updated.DNSAddress)
		if err != nil {
			return fmt.Errorf("invalid DNS bind address: %w", err)
		}
		updated.DNSAddress = net.JoinHostPort(host, options.dnsPort)
	}
	if options.httpPort != "" {
		updated.NASPort = options.httpPort
	}
	if options.httpsPort != "" {
		updated.NASPortHTTPS = options.httpsPort
	}
	for _, port := range []string{updated.NASPort, updated.NASPortHTTPS} {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("invalid NAS port %q", port)
		}
	}
	*config = updated
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
	if err := checkLANAddressLocal(config.LANAddress); err != nil {
		return err
	}
	specs, err := bindSpecs(config)
	if err != nil {
		return err
	}
	// Save only the selected interface and bindings derived from the previous
	// LAN address. Development port overrides remain local to this invocation.
	previousLAN := result.Config.LANAddress
	if _, err := common.RebindLANAddress(config.LANAddress); err != nil {
		return err
	}
	if previousLAN != config.LANAddress {
		fmt.Fprintf(stdout, "LAN address changed: %s -> %s (saved)\n", previousLAN, config.LANAddress)
	}

	// Preflight: check that every required port is free before attempting to
	// bind (and before the macOS admin prompt appears).
	needsAuthorization, err := preflightPorts(ctx, specs)
	if err != nil {
		return err
	}

	// Warn the user if administrator credentials will be needed.
	if needsAuthorization && runtime.GOOS == "darwin" && os.Geteuid() != 0 {
		fmt.Fprintln(stdout, "Some ports require administrator privileges:", privilegedPortSummary(specs))
		fmt.Fprintln(stdout, "macOS will ask for your password to bind them.")
		fmt.Fprintln(stdout, "(Run mkwfc as root or with sudo to skip the prompt.)")
	}

	runtimeDir := filepath.Join(common.DataPath(), "run")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	sockets, err := privbind.Bind(ctx, specs, runtimeDir)
	if err != nil {
		return err
	}
	defer sockets.CloseAll()
	fmt.Fprintln(stdout, "Set your Wii DNS to", config.LANAddress)
	fmt.Fprintln(stdout, "Dolphin: use the built-in BBA and set its DNS to this address.")
	fmt.Fprintln(stdout, "Host Manager:", "http://"+config.AdminAddress+"/")
	fmt.Fprintln(stdout, "Gecko codes for this server's key:", common.DataPath("gecko"))
	if options.appMode {
		fmt.Fprintln(stdout, "Opening the Host Manager in your browser... (close this window to stop the server)")
		adminURL := "http://" + config.AdminAddress + "/"
		go func() {
			time.Sleep(1500 * time.Millisecond)
			openBrowser(adminURL)
		}()
	}
	return server.RunSingle(ctx, config, server.SingleOptions{Catalog: result.Catalog, Sockets: sockets})
}

// checkLANAddressLocal verifies the server's LAN address is actually assigned to
// an interface on this machine, so the user gets a clear message instead of a
// raw "bind: can't assign requested address" when they point --lan-ip (or a
// stale config.xml) at an address the host no longer has.
func checkLANAddressLocal(lanAddress string) error {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Errorf("enumerate local interfaces before binding: %w", err)
	}
	var local []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		if ip4.String() == lanAddress {
			return nil
		}
		if common.IsLANIPv4(ip4) {
			local = append(local, ip4.String())
		}
	}
	hint := "none found"
	if len(local) > 0 {
		hint = strings.Join(local, ", ")
	}
	return fmt.Errorf("LAN address %s is not assigned to any interface on this machine; "+
		"re-run with --lan-ip set to this host's current private IPv4 (available: %s)", lanAddress, hint)
}

func commandContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: mkwfc <init|run|doctor|repair-certs> [--data-dir DIR] [--lan-ip IPv4]")
	fmt.Fprintln(writer, "run detects the current LAN interface and saves derived bindings; --lan-ip selects an explicit interface.")
	fmt.Fprintln(writer, "Development overrides: --dns-port PORT --http-port PORT --https-port PORT")
	fmt.Fprintln(writer, "init may import pre-made certificates: --cert PEM --key PEM --wii-cert DER --wii-key PKCS8-PEM")
	fmt.Fprintln(writer, "repair-certs backs up and replaces only an incompatible Wii certificate, preserving its key.")
}
