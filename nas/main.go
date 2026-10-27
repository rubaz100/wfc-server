package nas

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"wwfc/api"
	"wwfc/common"
	"wwfc/gamestats"
	"wwfc/logging"
	"wwfc/race"
	"wwfc/sake"

	"github.com/logrusorgru/aurora/v3"
)

var (
	serverName           string
	server, tlsServer    *http.Server
	payloadServerAddress string
	hostMuxes            []hostMux
	nasErrors            = make(chan error, 2)
)

type hostMux struct {
	host *regexp.Regexp
	mux  *http.ServeMux
}

func StartServer(reload bool) {
	if err := StartServerWithListeners(nil, nil, nil); err != nil {
		logging.Error("NAS", "Server startup failed:", err)
	}
}

func StartServerWithListeners(payload PayloadService, httpSocket, httpsSocket net.Listener) error {
	if server != nil || tlsServer != nil {
		return errors.New("NAS server is already running")
	}
	config := common.GetConfig()
	serverName = config.ServerName
	payloadServerAddress = config.PayloadServerAddress
	embeddedPayload = payload
	if err := CacheProfanityFile(); err != nil {
		logging.Info("NAS", err)
	}
	if config.EnableHTTPS {
		if err := setupTLS(config); err != nil {
			embeddedPayload = nil
			return err
		}
		if httpsSocket == nil {
			listener, err := net.Listen("tcp", net.JoinHostPort(*config.NASAddressHTTPS, config.NASPortHTTPS))
			if err != nil {
				embeddedPayload = nil
				return err
			}
			httpsSocket = listener
		}
	} else if httpsSocket != nil {
		embeddedPayload = nil
		return errors.New("an HTTPS listener was supplied while HTTPS is disabled")
	}
	if httpSocket == nil {
		listener, err := net.Listen("tcp", net.JoinHostPort(*config.NASAddress, config.NASPort))
		if err != nil {
			if httpsSocket != nil {
				_ = httpsSocket.Close()
			}
			embeddedPayload = nil
			return err
		}
		httpSocket = listener
	}

	authMux := http.NewServeMux()
	dlsMux := http.NewServeMux()
	sakeMux := http.NewServeMux()
	gamestatsMux := http.NewServeMux()
	raceMux := http.NewServeMux()
	rootMux := http.NewServeMux()
	authMux.HandleFunc("/ac", handleAuthAccountEndpoint)
	authMux.HandleFunc("/pr", handleAuthProfanityEndpoint)
	dlsMux.HandleFunc("/download", handleDownloadEndpoint)
	switch {
	case payload != nil:
		authMux.Handle("/payload", payload)
		authMux.Handle("/payload/", payload)
	case payloadServerAddress != "":
		authMux.HandleFunc("/payload", forwardPayloadRequest)
		authMux.HandleFunc("/payload/", forwardPayloadRequest)
	default:
		authMux.HandleFunc("/payload", handlePayloadRequest)
	}
	for i := 0; i <= 9; i++ {
		authMux.HandleFunc("/w"+strconv.Itoa(i), downloadStage1)
	}
	authMux.HandleFunc("/nastest.jsp", handleNASTest)
	api.RegisterHandlers(rootMux)
	sake.RegisterHandlers(sakeMux)
	race.RegisterHandlers(raceMux)
	gamestatsMux.HandleFunc("/", gamestats.HandleWebRequest)
	rootMux.HandleFunc("/", handleUnknown)
	authMux.HandleFunc("/", handleUnknown)
	sakeMux.HandleFunc("/", handleUnknown)
	raceMux.HandleFunc("/", handleUnknown)
	hostMuxes = []hostMux{
		{regexp.MustCompile(`^(nas|naswii)\.`), authMux},
		{regexp.MustCompile(`^dls1\.`), dlsMux},
		{regexp.MustCompile(`(\.|^)gamestats2?\.(gs\.|gamespy\.com$)`), gamestatsMux},
		{regexp.MustCompile(`(\.|^)sake\.(gs\.|gamespy\.com$)`), sakeMux},
		{regexp.MustCompile(`(\.|^)race\.(gs\.|gamespy\.com$)`), raceMux},
	}
	routes := hostMuxes
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRequest(rootMux, routes, w, r)
	})
	server = &http.Server{
		Addr: httpSocket.Addr().String(), Handler: handler,
		IdleTimeout: 20 * time.Second, ReadTimeout: 10 * time.Second,
	}
	nasErrors = make(chan error, 2)
	serve(server, &httpListener{Listener: httpSocket})
	if config.EnableHTTPS {
		tlsServer = &http.Server{
			Addr: httpsSocket.Addr().String(), Handler: handler,
			IdleTimeout: server.IdleTimeout, ReadTimeout: server.ReadTimeout,
		}
		serve(tlsServer, &tlsListener{Listener: httpsSocket})
	}
	return nil
}

func Errors() <-chan error { return nasErrors }

func Shutdown() {
	httpServer, httpsServer := server, tlsServer
	server, tlsServer = nil, nil
	embeddedPayload = nil
	ctx, release := context.WithTimeout(context.Background(), 10*time.Second)
	defer release()
	for _, current := range []*http.Server{httpServer, httpsServer} {
		if current == nil {
			continue
		}
		if err := current.Shutdown(ctx); err != nil {
			logging.Error("NAS", "Error on HTTP shutdown:", err)
		}
	}
}

func handleRequest(rootMux *http.ServeMux, routes []hostMux, w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = strings.Trim(parsed, "[]")
	}
	if host == "conntest.nintendowifi.net" && r.Method == http.MethodGet {
		handleConnectionTest(w, r)
		return
	}
	for _, route := range routes {
		if route.host.MatchString(host) {
			route.mux.ServeHTTP(w, r)
			return
		}
	}
	rootMux.ServeHTTP(w, r)
}

func serve(current *http.Server, listener net.Listener) {
	go func() {
		err := current.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			nasErrors <- fmt.Errorf("NAS listener %s: %w", listener.Addr(), err)
		}
	}()
}

func getModuleName(r *http.Request) string {
	return "NAS:" + r.RemoteAddr
}

func replyHTTPError(w http.ResponseWriter, errorCode int, errorString string) {
	response := "<html>\n" +
		"<head><title>" + errorString + "</title></head>\n" +
		"<body>\n" +
		"<center><h1>" + errorString + "</h1></center>\n" +
		"<hr><center>" + serverName + "</center>\n" +
		"</body>\n" +
		"</html>\n"

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Length", strconv.Itoa(len(response)))
	w.Header().Set("Connection", "close")
	w.Header().Set("Server", "Nintendo")
	w.WriteHeader(errorCode)
	_, _ = w.Write([]byte(response))
}

func handleUnknown(w http.ResponseWriter, r *http.Request) {
	logging.Info(getModuleName(r), "Unknown request:", aurora.Yellow(r.Method), aurora.Cyan(r.Host+r.URL.Path))
	replyHTTPError(w, http.StatusNotFound, "404 Not Found")
}

func handleNASTest(w http.ResponseWriter, r *http.Request) {
	response := "" +
		"<html>\n" +
		"<body>\n" +
		"</br>AuthServer is up</br> \n" +
		"\n" +
		"</body>\n" +
		"</html>\n"

	w.Header().Set("Content-Type", "text/html;charset=ISO-8859-1")
	w.Header().Set("Content-Length", strconv.Itoa(len(response)))
	w.Header().Set("Connection", "close")
	w.Header().Set("NODE", "authserver-service.authserver.svc.cluster.local")
	w.Header().Set("Server", "Nintendo")

	w.WriteHeader(200)
	_, _ = w.Write([]byte(response))
}
