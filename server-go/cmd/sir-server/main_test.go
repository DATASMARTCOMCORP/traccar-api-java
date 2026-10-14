package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigPath(t *testing.T) {
	if got := configPath(nil); got != "./debug.xml" {
		t.Errorf("no args: %s", got)
	}
	if got := configPath([]string{"--service", "/opt/traccar/conf/traccar.xml"}); got != "/opt/traccar/conf/traccar.xml" {
		t.Errorf("last arg wins, as in Java: %s", got)
	}
}

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]slog.Level{
		"info": slog.LevelInfo, "INFO": slog.LevelInfo, "warning": slog.LevelWarn, "severe": slog.LevelError,
		"config": slog.LevelInfo, "fine": slog.LevelDebug, "all": slog.LevelDebug, "off": levelOff,
	} {
		if got, ok := parseLevel(name); !ok || got != want {
			t.Errorf("parseLevel(%q) = %v, %v", name, got, ok)
		}
	}
	if _, ok := parseLevel("verbose"); ok {
		t.Error("unknown level must be rejected")
	}
}

func writeConfig(t *testing.T, entries map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>\n<properties>\n")
	for k, v := range entries {
		b.WriteString("<entry key='" + k + "'>" + v + "</entry>\n")
	}
	b.WriteString("</properties>\n")
	path := filepath.Join(t.TempDir(), "config.xml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunStartupErrors(t *testing.T) {
	ctx := context.Background()
	if err := run(ctx, []string{filepath.Join(t.TempDir(), "missing.xml")}, &bytes.Buffer{}); err == nil {
		t.Error("missing config file must fail")
	}
	logFile := filepath.Join(t.TempDir(), "logs", "server.log")
	h2 := writeConfig(t, map[string]string{"database.driver": "org.h2.Driver", "database.url": "jdbc:h2:./data/database", "logger.file": logFile})
	if err := run(ctx, []string{h2}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "only org.postgresql.Driver") {
		t.Errorf("H2 config: %v", err)
	}
	unreachable := writeConfig(t, map[string]string{
		"database.url":  "jdbc:postgresql://127.0.0.1:1/none?connectTimeout=1",
		"database.user": "x", "logger.file": logFile,
	})
	if err := run(ctx, []string{unreachable}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "database") {
		t.Errorf("unreachable database: %v", err)
	}
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

// TestRunAgainstJavaDatabase starts the server on a database created by Java (see
// internal/store/roundtrip_test.go for SIR_TEST_JAVA_DB), then stops it as a signal would.
func TestRunAgainstJavaDatabase(t *testing.T) {
	dsn := os.Getenv("SIR_TEST_JAVA_DB")
	if dsn == "" {
		t.Skip("SIR_TEST_JAVA_DB not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	logFile := filepath.Join(t.TempDir(), "server.log")
	path := writeConfig(t, map[string]string{
		"database.driver":   "org.postgresql.Driver",
		"database.url":      "jdbc:postgresql://" + u.Host + u.Path,
		"database.user":     u.User.Username(),
		"database.password": password,
		"logger.file":       logFile,
		"database.timezone": "America/Sao_Paulo",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{path}, &bytes.Buffer{}) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(logFile)
		if strings.Contains(string(data), "database ready") {
			if !strings.Contains(string(data), "timezone=America/Sao_Paulo") {
				t.Errorf("database.timezone not applied:\n%s", data)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("server exited during startup: %v\n%s", err, data)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready:\n%s", data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
	data, _ := os.ReadFile(logFile)
	if !strings.Contains(string(data), "stopping server") {
		t.Errorf("no shutdown log:\n%s", data)
	}
}
