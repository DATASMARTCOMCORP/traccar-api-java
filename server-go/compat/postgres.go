package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// cluster is a throwaway Postgres instance listening only on 127.0.0.1 over TCP.
// Unix sockets are disabled so it can never be confused with a system server.
type cluster struct {
	bin  string // directory with initdb, pg_ctl, postgres
	dir  string // data directory
	port int
	tz   string
}

const clusterPassword = "compat"

func startCluster(ctx context.Context, bin, dir, tz string) (*cluster, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	c := &cluster{bin: bin, dir: dir, port: port, tz: tz}

	pwFile := filepath.Join(filepath.Dir(dir), "pgpass")
	if err := os.WriteFile(pwFile, []byte(clusterPassword), 0o600); err != nil {
		return nil, err
	}
	initdb := exec.CommandContext(ctx, filepath.Join(bin, "initdb"),
		"-D", dir, "-U", "postgres", "--pwfile="+pwFile, "-E", "UTF8", "--locale=C", "-A", "scram-sha-256")
	initdb.Env = append(os.Environ(), "TZ="+tz)
	if out, err := initdb.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initdb: %w\n%s", err, out)
	}

	opts := fmt.Sprintf("-p %d -h 127.0.0.1 -c unix_socket_directories='' -c fsync=off -c timezone=%s", port, tz)
	start := exec.CommandContext(ctx, filepath.Join(bin, "pg_ctl"),
		"-D", dir, "-l", filepath.Join(filepath.Dir(dir), "postgres.log"), "-o", opts, "-w", "start")
	if out, err := start.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pg_ctl start: %w\n%s", err, out)
	}
	slog.Info("postgres started", "port", port, "dir", dir)
	return c, nil
}

func (c *cluster) stop() {
	cmd := exec.Command(filepath.Join(c.bin, "pg_ctl"), "-D", c.dir, "-m", "fast", "-w", "stop")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("pg_ctl stop", "err", err, "output", string(out))
	}
}

// dsn returns a pgx connection string for a database in this cluster.
func (c *cluster) dsn(db string) string {
	return fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/%s?sslmode=disable", clusterPassword, c.port, db)
}

// jdbcURL returns the JDBC URL the Java server uses for a database in this cluster.
func (c *cluster) jdbcURL(db string) string {
	return "jdbc:postgresql://127.0.0.1:" + strconv.Itoa(c.port) + "/" + db
}

func (c *cluster) exec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, c.dsn("postgres"))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func (c *cluster) createDatabase(ctx context.Context, name, template string) error {
	sql := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if template != "" {
		sql += " TEMPLATE " + pgx.Identifier{template}.Sanitize()
	}
	// A template can't be copied while someone is connected to it; Java may take a moment
	// to close its pool after shutdown.
	var err error
	for range 20 {
		if err = c.exec(ctx, sql); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("create database %s: %w", name, err)
}
