package autobackup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if record := os.Getenv("AUTOBACKUP_DUMP_PROBE"); record != "" {
		found := 0
		for _, v := range os.Environ() {
			if strings.HasPrefix(v, "PGPASSWORD=") {
				found++
			}
		}
		b, _ := json.Marshal(Object{"args": os.Args[1:], "password_matches": os.Getenv("PGPASSWORD") == "owned-private-password", "password_entries": found})
		if os.WriteFile(record, b, 0600) != nil {
			os.Exit(9)
		}
		for i, v := range os.Args {
			if v == "-f" && i+1 < len(os.Args) {
				if os.WriteFile(os.Args[i+1], []byte("-- owned test dump\n"), 0600) != nil {
					os.Exit(8)
				}
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestNativePostgresFixedArgvPasswordOnlyInEnvironment(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	record, dest := filepath.Join(root, "record.json"), filepath.Join(root, "owned.sql")
	d, e := NewPostgres(PostgresOptions{Binary: exe, Host: "owned host", Port: "5432", Username: "owned-user", Database: "owned-db", Password: "owned-private-password", Env: []string{"AUTOBACKUP_DUMP_PROBE=" + record, "PGPASSWORD=unrelated-old-value"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Dump(context.Background(), dest); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(record)
	if e != nil {
		t.Fatal(e)
	}
	var got Object
	decode(t, b, &got)
	expected := Object{"args": []string{"-h", "owned host", "-p", "5432", "-U", "owned-user", "-d", "owned-db", "-f", dest}, "password_matches": true, "password_entries": 1}
	if canonical(got) != canonical(expected) || strings.Contains(string(b), "owned-private-password") {
		t.Fatal("credentials or argv changed", canonical(got))
	}
	if _, e = os.Stat(dest); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = d.Dump(ctx, dest); e == nil || strings.Contains(e.Error(), "owned-private-password") {
		t.Fatal("cancellation leaked or accepted", e)
	}
}
func TestNativePostgresOwnedClusterDumpAndRestore(t *testing.T) {
	bins := map[string]string{}
	for _, name := range []string{"initdb", "postgres", "pg_ctl", "psql", "pg_dump"} {
		p, e := exec.LookPath(name)
		if e != nil {
			t.Skip("owned PostgreSQL toolchain unavailable: " + name)
		}
		bins[name] = p
	}
	root, tempErr := os.MkdirTemp("/tmp", "ab-pg-")
	if tempErr != nil {
		t.Fatal(tempErr)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	cluster, socket := filepath.Join(root, "cluster"), filepath.Join(root, "socket")
	if e := os.Mkdir(socket, 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	run := func(binary string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, bins[binary], args...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("owned %s failed %v %s", binary, e, string(out))
		}
		return out
	}
	run("initdb", "-D", cluster, "--no-locale", "--encoding=UTF8", "--auth=trust", "-U", "owned_backup")
	port := strconv.Itoa(55432)
	run("pg_ctl", "-D", cluster, "-l", filepath.Join(root, "server.log"), "-w", "start", "-o", fmt.Sprintf("-h '' -k %s -p %s", socket, port))
	defer func() {
		cmd := exec.Command(bins["pg_ctl"], "-D", cluster, "-m", "immediate", "-w", "stop")
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Errorf("owned PostgreSQL cleanup %v %s", e, out)
		}
	}()
	sql := func(db, input string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, bins["psql"], "-h", socket, "-p", port, "-U", "owned_backup", "-d", db, "-v", "ON_ERROR_STOP=1", "-At")
		cmd.Stdin = strings.NewReader(input)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("owned SQL failed %v %s", e, string(out))
		}
		return string(out)
	}
	sql("postgres", "CREATE DATABASE owned_original; CREATE DATABASE owned_restored;")
	sql("owned_original", `CREATE SCHEMA cinema; CREATE TYPE cinema.mood AS ENUM ('new','watched'); CREATE TABLE cinema.films(id bigserial PRIMARY KEY,title text UNIQUE NOT NULL, mood cinema.mood DEFAULT 'new', raw bytea); INSERT INTO cinema.films(title,raw) VALUES ('owned 电影',decode('0001ff','hex')), (E'line\nquote''',NULL); CREATE VIEW cinema.titles AS SELECT id,title FROM cinema.films; CREATE FUNCTION cinema.count_films() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM cinema.films'; COMMENT ON TABLE cinema.films IS 'owned schema metadata'; CREATE INDEX titles_idx ON cinema.films(title);`)
	dumpPath := filepath.Join(root, "owned.sql")
	d, e := NewPostgres(PostgresOptions{Binary: bins["pg_dump"], Host: socket, Port: port, Username: "owned_backup", Database: "owned_original", Password: "owned-only-not-real"})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Dump(ctx, dumpPath); e != nil {
		t.Fatal(e)
	}
	dump, e := os.ReadFile(dumpPath)
	if e != nil || len(dump) == 0 {
		t.Fatal("missing native dump", e)
	}
	for _, fragment := range []string{"CREATE SCHEMA cinema", "CREATE TYPE cinema.mood AS ENUM", "COPY cinema.films", "CREATE VIEW cinema.titles", "CREATE FUNCTION cinema.count_films", "COMMENT ON TABLE cinema.films", "CREATE INDEX titles_idx"} {
		if !strings.Contains(string(dump), fragment) {
			t.Error("native dump omitted", fragment)
		}
	}
	sql("owned_restored", string(dump))
	original := sql("owned_original", `SELECT id,title,mood,encode(raw,'hex') FROM cinema.films ORDER BY id; SELECT cinema.count_films(); SELECT obj_description('cinema.films'::regclass);`)
	restored := sql("owned_restored", `SELECT id,title,mood,encode(raw,'hex') FROM cinema.films ORDER BY id; SELECT cinema.count_films(); SELECT obj_description('cinema.films'::regclass);`)
	if original != restored {
		t.Fatal("native PostgreSQL restore differs")
	}
	if value := sql("owned_restored", `INSERT INTO cinema.films(title) VALUES ('next owned') RETURNING id;`); !strings.HasPrefix(value, "3\n") {
		t.Fatal("sequence state lost", value)
	}
	t.Log("owned PostgreSQL schema/data/view/function/type/index/comment/sequence round-trip passed")
}

func TestNativePostgresMissingClientUsesOnlyHostPackagedResolution(t *testing.T) {
	root := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	record := filepath.Join(root, "record.json")
	calls := 0
	id := "owned-container"
	d, e := NewPostgres(PostgresOptions{Binary: filepath.Join(root, "missing-pg-dump"), Password: "owned-private-password", Env: []string{"AUTOBACKUP_DUMP_PROBE=" + record}, Runtime: RuntimeSnapshot{1000, true, &id}, Proxy: "http://proxy.invalid:8080", ResolveClient: func(_ context.Context, p ClientProvisionRequest) (string, error) {
		calls++
		if p.MajorVersion != 17 || p.Execution != "container" || p.ContainerID == nil || *p.ContainerID != "owned-container" || len(p.Environment) != 4 {
			t.Fatal("source client branch projection changed", p)
		}
		return exe, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Dump(context.Background(), filepath.Join(root, "owned.sql")); e != nil || calls != 1 {
		t.Fatal("packaged resolution failed", e, calls)
	}
	b, e := os.ReadFile(record)
	if e != nil || strings.Contains(string(b), "owned-private-password") || strings.Contains(string(b), "sh") {
		t.Fatal("shell or credential reached argv", e, string(b))
	}
}
