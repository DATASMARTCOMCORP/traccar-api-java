// Command sir-server is the Go port of the SIR tracking server. It reads the same XML
// configuration file as the Java server and shares its database:
//
//	sir-server /opt/traccar/conf/traccar.xml
//
// As with Java, the config file is the last argument, defaulting to ./debug.xml.
//
// Startup: load the config, set up logging, open the database (refusing an unknown
// schema version), serve the API, then run until SIGINT or SIGTERM and shut down gracefully.
// Device protocol listeners are added in later migration steps.
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

	apiOpts, err := api.OptionsFromConfig(cfg)
	if err != nil {
		return err
	}
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
	if err := web.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("web shutdown: %w", err)
	}
	return nil // the deferred st.Close waits for queries in flight
}

// shutdownTimeout bounds how long requests in flight get to finish after a stop signal.
const shutdownTimeout = 30 * time.Second

func configPath(args []string) string {
	if len(args) == 0 {
		return "./debug.xml"
	}
	return args[len(args)-1]
}
