# server-go

The Go port of the SIR server. `MIGRATION.md` is the plan and the source of truth.

```
cmd/sir-server/    main: config, logging, database, graceful shutdown
internal/config/   the Java XML config format, env overrides, typed keys
internal/model/    entities and Java-compatible encodings (dates, attributes, JSON text)
internal/store/    Postgres: explicit SQL, wall-clock time zone, schema version check
compat/            Java-vs-Go differential harness (see compat/README.md)
inventory/         Step 0 production queries and client API usage
```

Development (Go version per `go.mod`):

```sh
go vet ./... && go test ./...                       # unit tests; DB tests skip
CGO_ENABLED=0 go build -o sir-server ./cmd/sir-server
./sir-server /path/to/traccar.xml                   # same config file as the Java server
```

The database tests need rows written by the Java server. Run the harness with `-keep`,
then point the tests at side A (see `internal/store/roundtrip_test.go`):

```sh
go run ./compat -pg-bin /path/to/pgsql/bin -keep -work /tmp/run
SIR_TEST_JAVA_DB='postgres://postgres:compat@127.0.0.1:PORT/side_A' go test -count=1 ./...
```

CI (`.github/workflows/server-go.yml`) runs both, plus the harness canary.

Go-only config key: `database.timezone`, the zone Java's JVM stores timestamps in.
It defaults to the host zone, which is also the JVM's default.
