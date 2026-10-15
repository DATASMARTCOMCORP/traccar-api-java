// Command compat is the Java-vs-Go differential test harness (Step 1 of MIGRATION.md).
//
// It starts a throwaway Postgres cluster and creates the schema once by running the Java
// server's Liquibase changelog into a template database. Each side gets its own copy of
// that template. It then starts two servers (A and B, each "java" or "go") and runs the
// scenario library against both in lockstep. Each step's HTTP status, relevant headers
// and JSON body are compared, and after every scenario all database rows are compared.
// See README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// errDifferences means the run completed and found differences (exit 1); any other
// error is a setup failure (exit 2).
var errDifferences = errors.New("servers differ")

func main() {
	err := run()
	switch {
	case errors.Is(err, errDifferences):
		fmt.Fprintln(os.Stderr, "compat:", err)
		os.Exit(1)
	case err != nil:
		fmt.Fprintln(os.Stderr, "compat:", err)
		os.Exit(2)
	}
}

func run() error {
	var (
		pgBin   = flag.String("pg-bin", os.Getenv("PGBIN"), "directory with initdb, pg_ctl and postgres")
		repo    = flag.String("repo", "..", "repository root (Liquibase changelog, default jar location)")
		jar     = flag.String("jar", "", "Java server jar (default <repo>/target/tracker-server.jar)")
		goBin   = flag.String("go-bin", "", "Go server binary, for -a go or -b go")
		sideA   = flag.String("a", "java", "side A: java or go")
		sideB   = flag.String("b", "java", "side B: java or go")
		only    = flag.String("run", "", "only run scenarios whose name matches this regexp (they build on each other)")
		suiteF  = flag.String("suite", "full", "scenario suite: full (whole API, empty database) or auth (Step 3)")
		keep    = flag.Bool("keep", false, "keep the work directory, servers' logs and databases; print DSNs")
		work    = flag.String("work", "", "work directory (default: a new temp directory)")
		tz      = flag.String("tz", "America/Sao_Paulo", "time zone for both servers and Postgres; non-UTC on purpose")
		bTZ     = flag.String("b-tz", "", "canary: run side B in another time zone; the run must then fail")
		verbose = flag.Bool("v", false, "log every step")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if *pgBin == "" {
		return errors.New("-pg-bin (or PGBIN) is required")
	}
	repoAbs, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	if *jar == "" {
		*jar = filepath.Join(repoAbs, "target", "tracker-server.jar")
	}
	if _, err := os.Stat(*jar); err != nil {
		return fmt.Errorf("java jar: %w (build it with ./gradlew assemble)", err)
	}
	for _, side := range []string{*sideA, *sideB} {
		if side == "go" && *goBin == "" {
			return errors.New("-go-bin is required when a side is go")
		}
		if side != "java" && side != "go" {
			return fmt.Errorf("unknown side %q", side)
		}
	}
	filter, err := regexp.Compile(*only)
	if err != nil {
		return err
	}
	suite, ok := suites()[*suiteF]
	if !ok {
		return fmt.Errorf("unknown suite %q", *suiteF)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workDir := *work
	if workDir == "" {
		if workDir, err = os.MkdirTemp("", "sir-compat-"); err != nil {
			return err
		}
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	if !*keep {
		defer os.RemoveAll(workDir)
	}
	slog.Info("work directory", "dir", workDir)

	pg, err := startCluster(ctx, *pgBin, filepath.Join(workDir, "pgdata"), *tz)
	if err != nil {
		return err
	}
	if !*keep {
		defer pg.stop()
	}

	base := launch{javaJar: *jar, goBin: *goBin, repo: repoAbs, tz: *tz, cluster: pg, workRoot: workDir}

	// The schema always comes from Java's Liquibase run, never from Go: Go doesn't run
	// migrations. Both sides then start from byte-identical copies.
	if err := pg.createDatabase(ctx, "schema_template", ""); err != nil {
		return err
	}
	tmpl, err := newTarget(withKind(base, "java"), "schema", "schema_template")
	if err != nil {
		return err
	}
	if err := tmpl.start(ctx, withKind(base, "java")); err != nil {
		return err
	}
	// The suite's seed runs on the template through Java, so both sides start with the
	// same Java-created rows (users, password hashes, the token key pair).
	seed := newSide(tmpl, nil)
	for i, s := range suite.seed {
		resp, err := seed.do(ctx, s)
		if err != nil || resp.status >= 300 {
			tmpl.stop()
			status := 0
			if resp != nil {
				status = resp.status
			}
			return fmt.Errorf("seed step %d %s: status %d, %v", i+1, describe(s), status, err)
		}
	}
	tmpl.stop()

	var targets []*target
	defer func() {
		for _, t := range targets {
			t.stop()
		}
	}()
	runs := make([]*side, 2)
	for i, side := range []struct{ label, kind string }{{"A", *sideA}, {"B", *sideB}} {
		db := "side_" + side.label
		if err := pg.createDatabase(ctx, db, "schema_template"); err != nil {
			return err
		}
		l := withKind(base, side.kind)
		if side.label == "B" && *bTZ != "" {
			l.tz = *bTZ
		}
		t, err := newTarget(l, side.label, db)
		if err != nil {
			return err
		}
		targets = append(targets, t)
		if err := t.start(ctx, l); err != nil {
			return err
		}
		conn, err := pgx.Connect(ctx, pg.dsn(db))
		if err != nil {
			return err
		}
		defer conn.Close(context.Background())
		runs[i] = newSide(t, conn)
		maps.Copy(runs[i].vars, seed.vars)
	}
	runs[0].other, runs[1].other = runs[1], runs[0]

	failed := runScenarios(ctx, suite.scenarios, filter, runs[0], runs[1])

	if *keep {
		fmt.Printf("\nkept work directory %s\n  A (%s): %s\n  B (%s): %s\n  logs: %s, %s\n",
			workDir, *sideA, pg.dsn("side_A"), *sideB, pg.dsn("side_B"), targets[0].logPath(), targets[1].logPath())
		fmt.Printf("  stop Postgres with: %s -D %s stop\n", filepath.Join(*pgBin, "pg_ctl"), pg.dir)
	}
	if failed > 0 {
		return fmt.Errorf("%d scenario(s): %w", failed, errDifferences)
	}
	return nil
}

func withKind(l launch, kind string) launch {
	l.kind = kind
	return l
}

// runScenarios runs every step on A then B, compares, and after each scenario waits for
// both databases to settle and compares them. It returns the number of failed scenarios.
func runScenarios(ctx context.Context, scenarios []Scenario, filter *regexp.Regexp, a, b *side) int {
	failed, ran := 0, 0
	for _, sc := range scenarios {
		if !filter.MatchString(sc.Name) {
			continue
		}
		ran++
		var problems []string
		for i, s := range sc.Steps {
			label := fmt.Sprintf("step %d %s", i+1, describe(s))
			slog.Debug("step", "scenario", sc.Name, "step", label)
			ra, errA := a.do(ctx, s)
			rb, errB := b.do(ctx, s)
			if errA != nil || errB != nil {
				problems = append(problems, fmt.Sprintf("%s: error: A=%v B=%v", label, errA, errB))
				if ra == nil || rb == nil {
					continue
				}
			}
			slog.Debug("result", "scenario", sc.Name, "step", label, "statusA", ra.status, "statusB", rb.status)
			for _, d := range compareResponses(s, ra, rb, a.aliases, b.aliases) {
				problems = append(problems, label+": "+d)
			}
			if s.Device != nil {
				// Fixes are buffered, then processed asynchronously after the ACK. Wait out
				// the buffer, then until both databases settle, so the next step sees the
				// same state on both sides.
				time.Sleep(bufferingThreshold + time.Second)
				for _, r := range []*side{a, b} {
					if _, err := waitQuiet(ctx, r.db); err != nil {
						problems = append(problems, fmt.Sprintf("%s: %s: %v", label, r.t.label, err))
					}
				}
			}
		}
		for _, r := range []*side{a, b} {
			if !r.t.alive() {
				problems = append(problems, fmt.Sprintf("target %s exited: %v", r.t.label, r.t.err))
			}
		}
		snapA, errA := waitQuiet(ctx, a.db)
		snapB, errB := waitQuiet(ctx, b.db)
		if errA != nil || errB != nil {
			problems = append(problems, fmt.Sprintf("database snapshot: A=%v B=%v", errA, errB))
		}
		if snapA != nil && snapB != nil {
			snapA.alias(a.aliases)
			snapB.alias(b.aliases)
			for _, d := range diffSnapshots(snapA, snapB) {
				problems = append(problems, "db "+d)
			}
		}

		if len(problems) == 0 {
			fmt.Printf("PASS %-28s %d steps\n", sc.Name, len(sc.Steps))
			continue
		}
		failed++
		fmt.Printf("FAIL %-28s %d steps, %d differences\n", sc.Name, len(sc.Steps), len(problems))
		for _, p := range problems {
			fmt.Println("     ", p)
		}
	}
	fmt.Printf("\n%d scenario(s) run, %d failed\n", ran, failed)
	return failed
}

func describe(s Step) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Device != nil {
		return s.Device.Protocol + "/" + s.Device.Transport
	}
	return s.Method + " " + s.Path
}
