// Command sir-server is the Go port of the SIR tracking server. It reads the same XML
// configuration file as the Java server and shares its database:
//
//	sir-server /opt/traccar/conf/traccar.xml
//
// As with Java, the config file is the last argument, defaulting to ./debug.xml.
//
// Startup: load the config, set up logging, open the database (refusing an unknown
// schema version), then run until SIGINT or SIGTERM and shut down gracefully. Listeners
// (API, device protocols) are added in later migration steps.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	// Java resets every device to offline at startup (DeviceUtil.resetStatus). Go doesn't
	// yet: while Java still serves devices against the same database, that would mark
	// Java's connected devices offline. It moves here when Go owns the device ports (Step 6).

	<-ctx.Done()
	// Listeners will be stopped here, with a deadline, as they are ported. The deferred
	// st.Close waits for queries in flight.
	slog.Info("stopping server")
	return nil
}

func configPath(args []string) string {
	if len(args) == 0 {
		return "./debug.xml"
	}
	return args[len(args)-1]
}
