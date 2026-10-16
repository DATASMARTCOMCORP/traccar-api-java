// Command sir-server is the Go port of the SIR tracking server. It reads the same XML
// configuration file as the Java server and shares its database:
//
//	sir-server /opt/traccar/conf/traccar.xml
//
// As with Java, the config file is the last argument, defaulting to ./debug.xml.
//
// Startup: load the config, set up logging, open the database (refusing an unknown
// schema version), open the device protocol ports and serve the API, then run until
// SIGINT or SIGTERM and shut down gracefully.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/api"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/all"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sir-server:", err)
		os.Exit(1)
	}
}

// run starts the server and blocks until ctx is cancelled and shutdown has finished.
// Startup errors are returned before anything is served.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	path := configPath(args)
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	logger, closeLog, err := newLogger(cfg, stderr)
	if err != nil {
		return err
	}
	defer closeLog()
	slog.SetDefault(logger)
	slog.Info("starting server", "version", version, "config", path)

	opts, err := store.OptionsFromConfig(cfg)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, opts)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()
	slog.Info("database ready", "timezone", st.Location().String())

	// Like Java at startup (DeviceUtil.resetStatus): every device is offline until it
	// connects again. Go always runs device ports (the plan moves ports to Go before the
	// API), so a Go restart means the same as a Java restart.
	if err := st.ResetDeviceStatuses(ctx); err != nil {
		return err
	}

	proc, err := ingest.NewProcessor(cfg, st, slog.Default())
	if err != nil {
		return err
	}
	listeners, err := startProtocols(cfg, proc, st.Location())
	if err != nil {
		return err
	}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	procCtx, stopProc := context.WithCancel(context.Background())
	defer stopProc()
	go proc.Run(procCtx)

	apiOpts, err := api.OptionsFromConfig(cfg)
	if err != nil {
		return err
	}
	apiOpts.SendCommand = proc.SendCommand
	port, err := cfg.Int(config.WebPort, config.DefaultWebPort)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.String(config.WebAddress, ""), strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	web := &http.Server{
		Handler:           api.New(apiOpts, st, auth.NewTokens(st)),
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- web.Serve(ln) }()
	slog.Info("web server started", "address", ln.Addr().String())

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return fmt.Errorf("web: %w", err)
	}
	slog.Info("stopping server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, l := range listeners {
		l.Close()
	}
	if err := web.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("web shutdown: %w", err)
	}
	if err := proc.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("processing shutdown: %w", err)
	}
	return nil // the deferred st.Close waits for queries in flight
}

// startProtocols opens the ports of the ported protocols, as Java's ServerManager does
// for all of them: each protocol allowed by protocols.enable with a port above 0. Like
// Java, a server that can't listen is logged and skipped; a bad decoder option fails
// startup.
func startProtocols(cfg *config.Config, proc *ingest.Processor, zone *time.Location) ([]*ingest.Listener, error) {
	var listeners []*ingest.Listener
	for _, name := range all.Names() {
		if !cfg.ProtocolEnabled(name) {
			continue
		}
		port, err := cfg.Port(name)
		if err != nil {
			return nil, err
		}
		if port <= 0 {
			continue
		}
		newProtocol, _ := all.Lookup(name)
		proto, err := newProtocol(cfg, zone)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		l, err := proc.ServeProtocol(proto, port)
		if err != nil {
			slog.Warn("Port disabled due to conflict", "protocol", name, "port", port, "error", err)
		} else {
			slog.Info("protocol started", "protocol", name, "port", port)
		}
		listeners = append(listeners, l)
	}
	return listeners, nil
}

// shutdownTimeout bounds how long requests in flight get to finish after a stop signal.
const shutdownTimeout = 30 * time.Second

func configPath(args []string) string {
	if len(args) == 0 {
		return "./debug.xml"
	}
	return args[len(args)-1]
}
