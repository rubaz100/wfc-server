package admin

import (
	"context"
	"embed"
	"io/fs"
	"net"
	"net/http"
	"os"
	"time"
	"wwfc/api"
	"wwfc/common"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

//go:embed web
var webFS embed.FS

var server *http.Server

// webFileSystem returns the Host Manager's web assets. By default these are the
// copy embedded in the binary. For GUI development, set MKWFC_WEB_DIR to the
// admin/web source directory and the panel is served from disk instead, so
// edits show on reload without rebuilding. Falls back to the embed if the path
// is unset or missing.
func webFileSystem() (fs.FS, error) {
	if dir := os.Getenv("MKWFC_WEB_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			logging.Notice("ADMIN", "Serving web panel from disk:", aurora.BrightCyan(dir))
			return os.DirFS(dir), nil
		}
		logging.Warn("ADMIN", "MKWFC_WEB_DIR not a directory; using embedded panel:", aurora.Cyan(dir))
	}
	return fs.Sub(webFS, "web")
}

// StartServer starts the Host Manager: the embedded web panel plus the admin
// API, on config.AdminAddress (127.0.0.1 or LAN, depending on config).
func StartServer(reload bool) {
	if err := StartServerWithListener(reload, nil); err != nil {
		logging.Error("ADMIN", "HTTP server error:", err)
	}
}

func StartServerWithListener(reload bool, listener net.Listener) error {
	config := common.GetConfig()
	address := config.AdminAddress
	if listener != nil {
		address = listener.Addr().String()
	}
	mux := http.NewServeMux()
	api.RegisterExtendedHandlers(mux)
	webRoot, err := webFileSystem()
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(webRoot)))
	server = &http.Server{
		Addr: address, Handler: mux, IdleTimeout: 20 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second,
	}
	if listener == nil {
		listener, err = net.Listen("tcp", address)
		if err != nil {
			return err
		}
	}
	urls := panelURLs(address)
	go func() {
		logging.Notice("ADMIN", "Host Manager panel ready:")
		for _, u := range urls {
			logging.Notice("ADMIN", "    ", aurora.BrightCyan(u))
		}
		logging.Notice("ADMIN", "Sign in with the apiSecret from config.xml")
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logging.Error("ADMIN", "HTTP server error:", err)
		}
	}()
	return nil
}

// panelURLs returns the http URL(s) the admin panel is reachable at for the
// configured bind address. For a wildcard bind it lists loopback plus every
// private (RFC1918) IPv4 on the machine, so the host knows what to type on a
// phone or second laptop on the same LAN.
func panelURLs(address string) []string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return []string{"http://" + address + "/"}
	}

	// A specific bind address is the only place it's reachable.
	if host != "" && host != "0.0.0.0" && host != "::" && host != "[::]" {
		return []string{"http://" + net.JoinHostPort(host, port) + "/"}
	}

	urls := []string{"http://" + net.JoinHostPort("127.0.0.1", port) + "/"}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return urls
	}

	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}

		ip4 := ipNet.IP.To4()
		if ip4 == nil || !ip4.IsPrivate() {
			continue
		}

		urls = append(urls, "http://"+net.JoinHostPort(ip4.String(), port)+"/")
	}

	return urls
}

func Shutdown() {
	if server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logging.Error("ADMIN", "Error on HTTP shutdown:", err)
	}
}
