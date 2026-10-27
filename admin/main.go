package admin

import (
	"context"
	"embed"
	"io/fs"
	"net"
	"net/http"
	"time"
	"wwfc/api"
	"wwfc/common"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

//go:embed web
var webFS embed.FS

var server *http.Server

// StartServer starts the Host Manager: the embedded web panel plus the admin
// API, on config.AdminAddress (127.0.0.1 or LAN, depending on config).
func StartServer(reload bool) {
	config := common.GetConfig()
	address := config.AdminAddress

	mux := http.NewServeMux()
	api.RegisterExtendedHandlers(mux)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		logging.Error("ADMIN", "Failed to load embedded web UI:", err)
		return
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	server = &http.Server{
		Addr:         address,
		Handler:      mux,
		IdleTimeout:  20 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	urls := panelURLs(address)

	go func() {
		logging.Notice("ADMIN", "Host Manager panel ready:")
		for _, u := range urls {
			logging.Notice("ADMIN", "    ", aurora.BrightCyan(u))
		}
		logging.Notice("ADMIN", "Sign in with the apiSecret from config.xml")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("ADMIN", "HTTP server error:", err)
		}
	}()
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
