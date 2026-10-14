// Package sqlitebaseline measures a synthetic, isolated M0 database workload.
// It is not the application's production database layer.
package sqlitebaseline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Driver         string
	ParentDir      string
	Keep           bool
	Subscriptions  int
	Metadata       int
	Files          int
	ClaimAttempts  int
	UniqueKeys     int
	Readers        int
	ReadsPerReader int
	CrashCommand   func(context.Context, string, string) *exec.Cmd
}

func DefaultOptions() Options {
	return Options{Driver: "modernc", Subscriptions: 200, Metadata: 5000, Files: 50000, ClaimAttempts: 400, UniqueKeys: 100, Readers: 4, ReadsPerReader: 250}
}

type Memory struct {
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapInuseBytes uint64 `json:"heap_inuse_bytes"`
	GoSysBytes     uint64 `json:"go_sys_bytes"`
	Goroutines     int    `json:"goroutines"`
}

type Report struct {
	Status                string             `json:"status"`
	Evidence              string             `json:"evidence"`
	Driver                string             `json:"driver"`
	DriverModuleVersion   string             `json:"driver_module_version"`
	SQLiteVersion         string             `json:"sqlite_version"`
	SQLiteSourceID        string             `json:"sqlite_source_id"`
	WALResetFixPresent    bool               `json:"wal_reset_fix_present"`
	GoVersion             string             `json:"go_version"`
	Platform              string             `json:"platform"`
	WorkDir               string             `json:"synthetic_directory,omitempty"`
	Counts                map[string]int     `json:"counts"`
	Pragmas               map[string]any     `json:"pragmas"`
	Checks                map[string]string  `json:"checks"`
	TimingsMS             map[string]float64 `json:"timings_ms"`
	MemoryAfterSeed       Memory             `json:"memory_after_seed"`
	MemoryAfterWorkload   Memory             `json:"memory_after_workload"`
	PeakSampledGoSysBytes uint64             `json:"peak_sampled_go_sys_bytes"`
	DatabaseBytes         int64              `json:"database_bytes"`
	WALBytesBeforeClose   int64              `json:"wal_bytes_before_close"`
	Error                 string             `json:"error,omitempty"`
}

func memory() Memory {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return Memory{m.HeapAlloc, m.HeapInuse, m.Sys, runtime.NumGoroutine()}
}

func moduleVersion(driver string) string {
	path := "modernc.org/sqlite"
	if driver == "mattn" {
		path = "github.com/mattn/go-sqlite3"
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == path {
				return d.Version
			}
		}
	}
	return "unavailable-in-test-binary"
}

func (o Options) validate() error {
	if o.Subscriptions < 1 || o.Subscriptions > 1000 || o.Metadata < o.Subscriptions || o.Metadata > 20000 || o.Files < 1 || o.Files > 200000 || o.ClaimAttempts < 1 || o.ClaimAttempts > 10000 || o.UniqueKeys < 1 || o.UniqueKeys > o.ClaimAttempts || o.Readers < 1 || o.Readers > 8 || o.ReadsPerReader < 1 || o.ReadsPerReader > 1000 {
		return errors.New("workload outside bounded limits")
	}
	return nil
}

func driverName(driver string) (string, error) {
	n := ""
	switch driver {
	case "modernc":
		n = "sqlite"
	case "mattn":
		n = "sqlite3"
	default:
		return "", errors.New("driver must be modernc or mattn")
	}
	for _, available := range sql.Drivers() {
		if n == available {
			return n, nil
		}
	}
	return "", fmt.Errorf("driver %s is not compiled into this binary", driver)
}

func openDB(ctx context.Context, driver, path string, readonly bool) (*sql.DB, error) {
	n, err := driverName(driver)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("mode", "rw")
	if driver == "modernc" {
		for _, p := range []string{"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(5000)", "cache_size(-2048)", "wal_autocheckpoint(1000)"} {
			q.Add("_pragma", p)
		}
		if readonly {
			q.Add("_pragma", "query_only(1)")
		}
	} else {
		q.Set("_foreign_keys", "on")
		q.Set("_synchronous", "FULL")
		q.Set("_busy_timeout", "5000")
		q.Set("_cache_size", "-2048")
		if readonly {
			q.Set("_query_only", "on")
		}
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open(n, u.String())
	if err != nil {
		return nil, err
	}
	limit := 1
	if readonly {
		limit = 4
	}
	db.SetMaxOpenConns(limit)
	db.SetMaxIdleConns(limit)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
PRAGMA journal_mode=WAL;
CREATE TABLE metadata(id INTEGER PRIMARY KEY, kind TEXT NOT NULL, title TEXT NOT NULL, overview TEXT NOT NULL);
CREATE TABLE subscriptions(id INTEGER PRIMARY KEY, media_id INTEGER NOT NULL REFERENCES metadata(id), state TEXT NOT NULL, next_check INTEGER NOT NULL);
CREATE INDEX subscriptions_due ON subscriptions(state,next_check);
CREATE TABLE files(id INTEGER PRIMARY KEY, media_id INTEGER NOT NULL REFERENCES metadata(id), path TEXT NOT NULL UNIQUE, size_bytes INTEGER NOT NULL, state TEXT NOT NULL);
CREATE INDEX files_media ON files(media_id,id);
CREATE TABLE claims(id INTEGER PRIMARY KEY, media_id INTEGER NOT NULL REFERENCES metadata(id), season INTEGER NOT NULL, episode INTEGER NOT NULL, purpose TEXT NOT NULL, state TEXT NOT NULL, UNIQUE(media_id,season,episode,purpose));
CREATE TABLE jobs(id INTEGER PRIMARY KEY, claim_id INTEGER NOT NULL UNIQUE REFERENCES claims(id), state TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, lease_until INTEGER);
CREATE INDEX jobs_due ON jobs(state,lease_until,id);
PRAGMA user_version=1;
`

func execBatch(ctx context.Context, db *sql.DB, count int, query string, args func(int) []any) error {
	const batchSize = 500
	for start := 0; start < count; start += batchSize {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			tx.Rollback()
			return err
		}
		end := min(start+batchSize, count)
		for i := start; i < end; i++ {
			if _, err = stmt.ExecContext(ctx, args(i)...); err != nil {
				stmt.Close()
				tx.Rollback()
				return err
			}
		}
		if err = stmt.Close(); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func seed(ctx context.Context, db *sql.DB, o Options) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	overview := strings.Repeat("Synthetic media metadata; ", 16)
	if err := execBatch(ctx, db, o.Metadata, "INSERT INTO metadata VALUES(?,?,?,?)", func(i int) []any { return []any{i + 1, "tv", fmt.Sprintf("Synthetic %05d", i+1), overview} }); err != nil {
		return err
	}
	if err := execBatch(ctx, db, o.Subscriptions, "INSERT INTO subscriptions VALUES(?,?,?,?)", func(i int) []any { return []any{i + 1, i + 1, "active", 1700000000 + i*60} }); err != nil {
		return err
	}
	return execBatch(ctx, db, o.Files, "INSERT INTO files VALUES(?,?,?,?,?)", func(i int) []any {
		return []any{i + 1, (i % o.Metadata) + 1, fmt.Sprintf("/synthetic/media/%05d/video-%06d.mkv", i%o.Metadata+1, i+1), int64(2_000_000_000), "present"}
	})
}

// claimAndEnqueue atomically reserves one acquisition and creates its durable job.
// A failed/duplicate reservation never leaves an orphan job.
func claimAndEnqueue(ctx context.Context, db *sql.DB, mediaID, episode int, forceFailure bool) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "INSERT INTO claims(media_id,season,episode,purpose,state) VALUES(?,1,?,'acquire','queued') ON CONFLICT(media_id,season,episode,purpose) DO NOTHING", mediaID, episode)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}
	if forceFailure {
		return false, errors.New("injected failure between claim and job")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO jobs(claim_id,state) VALUES(?,'queued')", id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func count(ctx context.Context, db *sql.DB, query string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}

func integrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return err
		}
		if s != "ok" {
			return errors.New("integrity check failed")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign key check failed")
	}
	return rows.Err()
}

func WalResetFixed(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	v := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		v[i] = n
	}
	return v[0] > 3 || (v[0] == 3 && (v[1] > 51 || v[1] == 51 && v[2] >= 3 || v[1] == 50 && v[2] >= 7 || v[1] == 44 && v[2] >= 6))
}

// Run always creates a new synthetic directory. It cannot point at an existing
// application database; ParentDir controls only where the temporary directory goes.
func Run(ctx context.Context, o Options) (r Report, returnErr error) {
	start := time.Now()
	r = Report{Status: "failed", Evidence: "synthetic_local_filesystem", Driver: o.Driver, DriverModuleVersion: moduleVersion(o.Driver), GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Counts: map[string]int{}, Pragmas: map[string]any{}, Checks: map[string]string{}, TimingsMS: map[string]float64{}}
	defer func() {
		r.TimingsMS["total"] = float64(time.Since(start).Microseconds()) / 1000
		if returnErr != nil {
			r.Error = returnErr.Error()
		}
	}()
	if err := o.validate(); err != nil {
		return r, err
	}
	if _, err := driverName(o.Driver); err != nil {
		return r, err
	}
	dir, err := os.MkdirTemp(o.ParentDir, "light-media-sqlite-")
	if err != nil {
		return r, err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		os.RemoveAll(dir)
		return r, err
	}
	dir = absDir
	if o.Keep {
		r.WorkDir = dir
	} else {
		defer os.RemoveAll(dir)
	}
	if err = os.WriteFile(filepath.Join(dir, ".sqlitebaseline"), []byte("synthetic-m0\n"), 0600); err != nil {
		return r, err
	}
	path := filepath.Join(dir, "baseline.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return r, err
	}
	if err = f.Close(); err != nil {
		return r, err
	}
	db, err := openDB(ctx, o.Driver, path, false)
	if err != nil {
		return r, err
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	if err = db.QueryRowContext(ctx, "SELECT sqlite_version(),sqlite_source_id()").Scan(&r.SQLiteVersion, &r.SQLiteSourceID); err != nil {
		return r, err
	}
	r.WALResetFixPresent = WalResetFixed(r.SQLiteVersion)
	if !r.WALResetFixPresent {
		return r, errors.New("SQLite lacks known WAL-reset fix")
	}
	stage := time.Now()
	if err = seed(ctx, db, o); err != nil {
		return r, err
	}
	r.TimingsMS["seed"] = float64(time.Since(stage).Microseconds()) / 1000
	for _, p := range []string{"journal_mode", "synchronous", "foreign_keys", "busy_timeout", "cache_size", "wal_autocheckpoint"} {
		var v any
		if err = db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err != nil {
			return r, err
		}
		r.Pragmas[p] = v
	}
	if r.Pragmas["journal_mode"] != "wal" || r.Pragmas["synchronous"] != int64(2) || r.Pragmas["foreign_keys"] != int64(1) {
		return r, errors.New("required pragmas not active")
	}
	r.MemoryAfterSeed = memory()
	r.PeakSampledGoSysBytes = r.MemoryAfterSeed.GoSysBytes
	reader, err := openDB(ctx, o.Driver, path, true)
	if err != nil {
		return r, err
	}
	defer reader.Close()
	stage = time.Now()
	latencies, err := workload(ctx, db, reader, o)
	if err != nil {
		return r, err
	}
	r.TimingsMS["mixed_workload"] = float64(time.Since(stage).Microseconds()) / 1000
	sort.Float64s(latencies)
	r.TimingsMS["paged_read_p50"] = latencies[len(latencies)/2]
	r.TimingsMS["paged_read_p95"] = latencies[(len(latencies)-1)*95/100]
	r.TimingsMS["paged_read_max"] = latencies[len(latencies)-1]
	r.MemoryAfterWorkload = memory()
	r.PeakSampledGoSysBytes = max(r.PeakSampledGoSysBytes, r.MemoryAfterWorkload.GoSysBytes)
	r.Counts["subscriptions"] = o.Subscriptions
	r.Counts["metadata"] = o.Metadata
	r.Counts["files"] = o.Files
	r.Counts["claim_attempts"] = o.ClaimAttempts
	r.Counts["paged_reads"] = len(latencies)
	if err = verifyClaimCounts(ctx, db, o.UniqueKeys); err != nil {
		return r, err
	}
	r.Checks["unique_reservation_with_contention"] = "passed"
	if _, err = claimAndEnqueue(ctx, db, 1, 800000, true); err == nil {
		return r, errors.New("failure injection did not fail")
	}
	if n, e := count(ctx, db, "SELECT count(*) FROM claims WHERE episode=800000"); e != nil || n != 0 {
		return r, errors.New("rollback left domain mutation")
	}
	r.Checks["claim_and_job_transaction_rollback"] = "passed"
	if _, err = db.ExecContext(ctx, "INSERT INTO subscriptions VALUES(100001,999999,'active',0)"); err == nil {
		return r, errors.New("foreign key violation was accepted")
	}
	r.Checks["foreign_key_enforcement"] = "passed"
	if _, err = db.ExecContext(ctx, "UPDATE jobs SET state='running',lease_until=0 WHERE id=1"); err != nil {
		return r, err
	}
	r.WALBytesBeforeClose = fileSize(path + "-wal")
	if err = reader.Close(); err != nil {
		return r, err
	}
	if err = db.Close(); err != nil {
		return r, err
	}
	db = nil
	if o.CrashCommand != nil {
		stage = time.Now()
		cmd := o.CrashCommand(ctx, o.Driver, path)
		output, e := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(e, &exitErr) || exitErr.ExitCode() != 86 || string(output) != "synthetic crash ready\n" {
			return r, errors.New("controlled crash helper did not reach expected exit")
		}
		r.TimingsMS["controlled_crash_process"] = float64(time.Since(stage).Microseconds()) / 1000
	} else {
		r.Checks["ungraceful_process_exit_recovery"] = "not_run"
	}
	db, err = openDB(ctx, o.Driver, path, false)
	if err != nil {
		return r, err
	}
	if o.CrashCommand != nil {
		if n, e := count(ctx, db, "SELECT count(*) FROM claims WHERE episode=900000"); e != nil || n != 1 {
			return r, errors.New("committed crash transaction was lost")
		}
		if n, e := count(ctx, db, "SELECT count(*) FROM claims WHERE episode=900001"); e != nil || n != 0 {
			return r, errors.New("uncommitted crash transaction survived")
		}
		r.Checks["ungraceful_process_exit_recovery"] = "passed"
	}
	res, err := db.ExecContext(ctx, "UPDATE jobs SET state='queued',lease_until=NULL WHERE state='running' AND lease_until<=0")
	if err != nil {
		return r, err
	}
	affected, err := res.RowsAffected()
	if err != nil || affected != 1 {
		return r, errors.New("expired leased job was not recovered")
	}
	r.Checks["reopen_and_expired_job_lease_recovery"] = "passed"
	want := o.UniqueKeys
	if o.CrashCommand != nil {
		want++
	}
	if err = verifyClaimCounts(ctx, db, want); err != nil {
		return r, err
	}
	r.Counts["claims"] = want
	r.Counts["jobs"] = want
	stage = time.Now()
	backupPath := filepath.Join(dir, "snapshot.db")
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", backupPath); err != nil {
		return r, err
	}
	if err = os.Chmod(backupPath, 0600); err != nil {
		return r, err
	}
	backup, err := openDB(ctx, o.Driver, backupPath, true)
	if err != nil {
		return r, err
	}
	defer backup.Close()
	if err = integrity(ctx, backup); err != nil {
		return r, err
	}
	if err = verifyClaimCounts(ctx, backup, want); err != nil {
		return r, err
	}
	for _, table := range []string{"subscriptions", "metadata", "files"} {
		n, e := count(ctx, backup, "SELECT count(*) FROM "+table)
		if e != nil || n != r.Counts[table] {
			return r, errors.New("backup row count mismatch")
		}
	}
	// The snapshot stays consistent while the source changes afterwards.
	if _, err = claimAndEnqueue(ctx, db, 1, 900002, false); err != nil {
		return r, err
	}
	if n, e := count(ctx, backup, "SELECT count(*) FROM claims WHERE episode=900002"); e != nil || n != 0 {
		return r, errors.New("snapshot changed with source")
	}
	r.TimingsMS["consistent_backup_verify"] = float64(time.Since(stage).Microseconds()) / 1000
	r.Checks["vacuum_into_snapshot_integrity_and_isolation"] = "passed"
	if err = integrity(ctx, db); err != nil {
		return r, err
	}
	r.Checks["source_integrity"] = "passed"
	r.DatabaseBytes = fileSize(path)
	r.Status = "passed"
	return r, nil
}

func fileSize(path string) int64 {
	if s, e := os.Stat(path); e == nil {
		return s.Size()
	}
	return 0
}

func verifyClaimCounts(ctx context.Context, db *sql.DB, expected int) error {
	for _, table := range []string{"claims", "jobs"} {
		n, e := count(ctx, db, "SELECT count(*) FROM "+table)
		if e != nil {
			return e
		}
		if n != expected {
			return fmt.Errorf("%s count %d, expected %d", table, n, expected)
		}
	}
	n, e := count(ctx, db, "SELECT count(*) FROM claims c LEFT JOIN jobs j ON j.claim_id=c.id WHERE j.id IS NULL")
	if e != nil {
		return e
	}
	if n != 0 {
		return errors.New("claims without durable jobs")
	}
	return nil
}

func workload(ctx context.Context, writer, reader *sql.DB, o Options) ([]float64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, o.Readers+8)
	durations := make(chan []float64, o.Readers)
	for w := 0; w < 8; w++ {
		wg.Go(func() {
			for i := w; i < o.ClaimAttempts; i += 8 {
				if _, e := claimAndEnqueue(ctx, writer, 1, i%o.UniqueKeys+1, false); e != nil {
					errs <- e
					cancel()
					return
				}
			}
		})
	}
	for readerID := 0; readerID < o.Readers; readerID++ {
		wg.Go(func() {
			values := make([]float64, 0, o.ReadsPerReader)
			for i := 0; i < o.ReadsPerReader; i++ {
				start := time.Now()
				rows, e := reader.QueryContext(ctx, "SELECT id,path,size_bytes FROM files WHERE media_id=? ORDER BY id LIMIT 50", (i+readerID)%o.Metadata+1)
				if e != nil {
					errs <- e
					cancel()
					return
				}
				for rows.Next() {
					var id, size int64
					var path string
					if e = rows.Scan(&id, &path, &size); e != nil {
						rows.Close()
						errs <- e
						cancel()
						return
					}
				}
				e = rows.Err()
				closeErr := rows.Close()
				if e != nil {
					errs <- e
					cancel()
					return
				}
				if closeErr != nil {
					errs <- closeErr
					cancel()
					return
				}
				values = append(values, float64(time.Since(start).Microseconds())/1000)
			}
			durations <- values
		})
	}
	wg.Wait()
	close(errs)
	close(durations)
	for e := range errs {
		if e != nil {
			return nil, e
		}
	}
	all := make([]float64, 0, o.Readers*o.ReadsPerReader)
	for values := range durations {
		all = append(all, values...)
	}
	return all, nil
}

// CrashChild deliberately exits without deferred Close/Rollback. Only a marked
// synthetic database created by Run is accepted. Exit 86 is the expected signal.
func CrashChild(ctx context.Context, driver, path string) error {
	marker, e := os.ReadFile(filepath.Join(filepath.Dir(path), ".sqlitebaseline"))
	if e != nil || string(marker) != "synthetic-m0\n" || filepath.Base(path) != "baseline.db" {
		return errors.New("crash helper requires a marked synthetic database")
	}
	db, e := openDB(ctx, driver, path, false)
	if e != nil {
		return e
	}
	defer db.Close()
	if _, e = claimAndEnqueue(ctx, db, 1, 900000, false); e != nil {
		return e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO claims(media_id,season,episode,purpose,state) VALUES(1,1,900001,'acquire','queued')"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO jobs(claim_id,state) SELECT id,'queued' FROM claims WHERE episode=900001"); e != nil {
		return e
	}
	fmt.Print("synthetic crash ready\n")
	os.Exit(86)
	return nil
}
