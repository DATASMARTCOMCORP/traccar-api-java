package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// protocols lists the device protocols under test and the extra config each one needs.
// The values mirror production (sir-dev.xml).
var protocols = []struct {
	name  string
	extra map[string]string
}{
	{"osmand", nil},
	{"suntech", nil},
	{"intellitrac", nil},
	{"atrack", map[string]string{"atrack.custom": "true"}},
}

// bufferingThreshold is server.buffering.threshold: fixes are held in memory this long
// before processing, where a database snapshot can't see them.
const bufferingThreshold = 5 * time.Second

// target is one server under test: the Java reference jar or the Go binary. Both read
// the same XML config file, which also exercises config-format compatibility.
type target struct {
	label string // "A" or "B", used in reports
	kind  string // "java" or "go"
	dir   string
	db    string

	webPort int
	ports   map[string]int // device protocol name → port

	cmd    *exec.Cmd
	exited chan struct{}
	err    error // set when exited is closed
}

type launch struct {
	kind     string
	javaJar  string
	goBin    string
	repo     string // repository root, for the Liquibase changelog
	tz       string
	cluster  *cluster
	workRoot string
}

func newTarget(l launch, label, db string) (*target, error) {
	t := &target{label: label, kind: l.kind, db: db, dir: filepath.Join(l.workRoot, label), ports: map[string]int{}}
	if err := os.MkdirAll(filepath.Join(t.dir, "web"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(t.dir, "web", "index.html"), []byte("<html></html>\n"), 0o644); err != nil {
		return nil, err
	}
	var err error
	if t.webPort, err = freePort(); err != nil {
		return nil, err
	}
	for _, p := range protocols {
		if t.ports[p.name], err = freePort(); err != nil {
			return nil, err
		}
	}
	if err := t.writeConfig(l); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *target) configPath() string { return filepath.Join(t.dir, "config.xml") }
func (t *target) logPath() string    { return filepath.Join(t.dir, "tracker-server.log") }
func (t *target) baseURL() string    { return "http://127.0.0.1:" + strconv.Itoa(t.webPort) }

func (t *target) writeConfig(l launch) error {
	entries := map[string]string{
		"database.driver":    "org.postgresql.Driver",
		"database.url":       l.cluster.jdbcURL(t.db),
		"database.user":      "postgres",
		"database.password":  clusterPassword,
		"database.changelog": filepath.Join(l.repo, "schema", "changelog-master.xml"),
		"web.address":        "127.0.0.1",
		"web.port":           strconv.Itoa(t.webPort),
		"web.path":           filepath.Join(t.dir, "web"),
		"media.path":         filepath.Join(t.dir, "media"),
		"logger.console":     "false",
		"logger.file":        t.logPath(),
		"logger.level":       "info",
		// Keep runs hermetic. Java defaults to an online geocoder (locationiq) and posts
		// daily statistics to traccar.org. Production uses the offline pluscodes geocoder.
		"geocoder.type":     "pluscodes",
		"server.statistics": "",
		// Position processing as configured in production (sir-dev.xml).
		"server.instantAcknowledgement": "true",
		"server.buffering.threshold":    strconv.Itoa(int(bufferingThreshold.Milliseconds())),
		"server.timeout":                "120",
	}
	var names []string
	for _, p := range protocols {
		names = append(names, p.name)
		entries[p.name+".port"] = strconv.Itoa(t.ports[p.name])
		entries[p.name+".address"] = "127.0.0.1"
		for k, v := range p.extra {
			entries[k] = v
		}
	}
	entries["protocols.enable"] = strings.Join(names, ",")

	var b strings.Builder
	b.WriteString("<?xml version='1.0' encoding='UTF-8'?>\n")
	b.WriteString("<!DOCTYPE properties SYSTEM 'http://java.sun.com/dtd/properties.dtd'>\n<properties>\n")
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString("    <entry key='")
		xml.EscapeText(&b, []byte(k))
		b.WriteString("'>")
		xml.EscapeText(&b, []byte(entries[k]))
		b.WriteString("</entry>\n")
	}
	b.WriteString("</properties>\n")
	return os.WriteFile(t.configPath(), []byte(b.String()), 0o644)
}

func (t *target) start(ctx context.Context, l launch) error {
	ports := []int{t.webPort}
	for _, p := range t.ports {
		ports = append(ports, p)
	}
	for _, p := range ports {
		if !portFree(p) {
			return fmt.Errorf("target %s: port %d is already in use", t.label, p)
		}
	}

	switch t.kind {
	case "java":
		t.cmd = exec.Command("java", "-Duser.timezone="+l.tz, "-jar", l.javaJar, t.configPath())
		// Java resolves relative paths (templates, default web) against the working directory.
		t.cmd.Dir = l.repo
	case "go":
		t.cmd = exec.Command(l.goBin, t.configPath())
		t.cmd.Dir = t.dir
	default:
		return fmt.Errorf("unknown target kind %q", t.kind)
	}
	t.cmd.Env = append(os.Environ(), "TZ="+l.tz)
	out, err := os.Create(filepath.Join(t.dir, "stdout.log"))
	if err != nil {
		return err
	}
	t.cmd.Stdout, t.cmd.Stderr = out, out
	if err := t.cmd.Start(); err != nil {
		out.Close()
		return fmt.Errorf("target %s: %w", t.label, err)
	}
	t.exited = make(chan struct{})
	go func() {
		t.err = t.cmd.Wait()
		out.Close()
		close(t.exited)
	}()
	slog.Info("target starting", "target", t.label, "kind", t.kind, "pid", t.cmd.Process.Pid, "web", t.webPort, "ports", t.ports)
	return t.waitReady(ctx)
}

// waitReady polls GET /api/server until it answers 200. A target that exits while
// starting (for example because a port was taken) fails immediately.
func (t *target) waitReady(ctx context.Context) error {
	deadline := time.After(3 * time.Minute)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-t.exited:
			return fmt.Errorf("target %s exited while starting: %v (see %s and %s)",
				t.label, t.err, t.logPath(), filepath.Join(t.dir, "stdout.log"))
		case <-deadline:
			return fmt.Errorf("target %s not ready after 3 minutes (see %s)", t.label, t.logPath())
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		resp, err := client.Get(t.baseURL() + "/api/server")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				slog.Info("target ready", "target", t.label)
				return nil
			}
		}
	}
}

func (t *target) stop() {
	if t.cmd == nil || t.cmd.Process == nil {
		return
	}
	select {
	case <-t.exited:
		return
	default:
	}
	_ = t.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-t.exited:
	case <-time.After(30 * time.Second):
		slog.Warn("target did not stop on SIGTERM, killing", "target", t.label)
		_ = t.cmd.Process.Kill()
		<-t.exited
	}
	var exitErr *exec.ExitError
	if t.err != nil && !errors.As(t.err, &exitErr) {
		slog.Warn("target exit", "target", t.label, "err", t.err)
	}
}

func (t *target) alive() bool {
	select {
	case <-t.exited:
		return false
	default:
		return true
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// portFree reports whether nothing listens on the port, on any address, for TCP and UDP.
// Java binds protocol ports on all interfaces unless told otherwise, so check wide.
func portFree(port int) bool {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	u, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	u.Close()
	return true
}
