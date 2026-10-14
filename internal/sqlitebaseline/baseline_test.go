package sqlitebaseline

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func availableDrivers() []string {
	var names []string
	for _, d := range sql.Drivers() {
		if d == "sqlite" {
			names = append(names, "modernc")
		}
		if d == "sqlite3" {
			names = append(names, "mattn")
		}
	}
	return names
}

func TestWALResetVersionGate(t *testing.T) {
	for version, want := range map[string]bool{"3.51.2": false, "3.51.3": true, "3.50.6": false, "3.50.7": true, "3.44.6": true, "3.53.4": true, "unknown": false, "3.51.x": false} {
		if got := WalResetFixed(version); got != want {
			t.Errorf("%s = %v, want %v", version, got, want)
		}
	}
}

func TestBoundedFreshWorkload(t *testing.T) {
	o := DefaultOptions()
	o.ParentDir = t.TempDir()
	o.Files = 200001
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("out-of-bounds workload accepted")
	}
	entries, err := os.ReadDir(o.ParentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid workload created files")
	}
	o = DefaultOptions()
	o.Driver = "missing"
	o.ParentDir = t.TempDir()
	if _, err = Run(context.Background(), o); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestCrashHelperRefusesUnmarkedDatabase(t *testing.T) {
	if err := CrashChild(context.Background(), "modernc", filepath.Join(t.TempDir(), "baseline.db")); err == nil {
		t.Fatal("unmarked directory accepted")
	}
}

func TestBaselineRecoveryAndSnapshot(t *testing.T) {
	for _, driver := range availableDrivers() {
		t.Run(driver, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			o := DefaultOptions()
			o.Driver = driver
			o.ParentDir = t.TempDir()
			o.Subscriptions = 3
			o.Metadata = 10
			o.Files = 100
			o.ClaimAttempts = 64
			o.UniqueKeys = 8
			o.ReadsPerReader = 20
			o.CrashCommand = func(ctx context.Context, driver, path string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashHelperProcess$")
				cmd.Env = append(os.Environ(), "LIGHT_MEDIA_CRASH_TEST=1", "LIGHT_MEDIA_CRASH_DRIVER="+driver, "LIGHT_MEDIA_CRASH_DB="+path)
				return cmd
			}
			r, err := Run(ctx, o)
			if err != nil {
				t.Fatalf("%+v: %v", r, err)
			}
			if r.Status != "passed" || r.Counts["claims"] != 9 || r.Counts["jobs"] != 9 {
				t.Fatalf("invalid report: %+v", r)
			}
			for key, status := range r.Checks {
				if status != "passed" {
					t.Errorf("%s = %s", key, status)
				}
			}
			entries, err := os.ReadDir(o.ParentDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("synthetic data was not cleaned")
			}
		})
	}
}

func TestCrashHelperProcess(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_CRASH_TEST") != "1" {
		return
	}
	if err := CrashChild(context.Background(), os.Getenv("LIGHT_MEDIA_CRASH_DRIVER"), os.Getenv("LIGHT_MEDIA_CRASH_DB")); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash helper returned instead of exiting")
}

func TestEveryReadConnectionHasGuards(t *testing.T) {
	for _, driver := range availableDrivers() {
		t.Run(driver, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "test.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			writer, err := openDB(ctx, driver, path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err = writer.ExecContext(ctx, schema); err != nil {
				t.Fatal(err)
			}
			reader, err := openDB(ctx, driver, path, true)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var conns []*sql.Conn
			defer func() {
				for _, conn := range conns {
					conn.Close()
				}
			}()
			for i := 0; i < 4; i++ {
				conn, e := reader.Conn(ctx)
				if e != nil {
					t.Fatal(e)
				}
				conns = append(conns, conn)
				for p, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "busy_timeout": 5000, "query_only": 1, "cache_size": -2048} {
					var v int
					if e = conn.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); e != nil || v != want {
						t.Fatalf("connection %d %s: %d %v", i, p, v, e)
					}
				}
				if _, e = conn.ExecContext(ctx, "INSERT INTO metadata VALUES(1,'tv','title','overview')"); e == nil {
					t.Fatal("reader pool accepted writes")
				}
			}
		})
	}
}

func TestForcedFailureLeavesNoDomainOrJobRecord(t *testing.T) {
	for _, driver := range availableDrivers() {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "test.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := openDB(ctx, driver, path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.ExecContext(ctx, schema); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES(1,'tv','title','overview')"); err != nil {
				t.Fatal(err)
			}
			if _, err = claimAndEnqueue(ctx, db, 1, 1, true); err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("expected injected error: %v", err)
			}
			if err = verifyClaimCounts(ctx, db, 0); err != nil {
				t.Fatal(err)
			}
			created, err := claimAndEnqueue(ctx, db, 1, 1, false)
			if err != nil || !created {
				t.Fatalf("rolled-back key could not be reclaimed: %v", err)
			}
		})
	}
}
