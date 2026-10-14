package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
)

// newLogger builds the logger from the same keys as Java: logger.console (stdout) or
// logger.file, and logger.level with java.util.logging level names. The returned
// function closes the log file.
//
// Not yet ported: logger.rotate (Java renames the file daily). Until then, rotate
// externally, e.g. logrotate with copytruncate.
func newLogger(c *config.Config, stderr io.Writer) (*slog.Logger, func(), error) {
	level, ok := parseLevel(c.String(config.LoggerLevel, config.DefaultLoggerLevel))
	if !ok {
		return nil, nil, fmt.Errorf("config %s: unknown level %q", config.LoggerLevel, c.String(config.LoggerLevel, ""))
	}
	if level == levelOff {
		return slog.New(slog.DiscardHandler), func() {}, nil
	}
	opts := &slog.HandlerOptions{Level: level}
	if c.Bool(config.LoggerConsole, false) {
		return slog.New(slog.NewTextHandler(os.Stdout, opts)), func() {}, nil
	}
	path := c.String(config.LoggerFile, config.DefaultLoggerFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("log file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("log file: %w", err)
	}
	fmt.Fprintf(stderr, "logging to %s\n", path)
	return slog.New(slog.NewTextHandler(f, opts)), func() { f.Close() }, nil
}

const levelOff = slog.Level(100)

// parseLevel maps java.util.logging level names (any case) to slog levels.
func parseLevel(name string) (slog.Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "OFF":
		return levelOff, true
	case "SEVERE":
		return slog.LevelError, true
	case "WARNING":
		return slog.LevelWarn, true
	case "INFO", "CONFIG":
		return slog.LevelInfo, true
	case "FINE", "FINER", "FINEST", "ALL":
		return slog.LevelDebug, true
	}
	return 0, false
}
