// records-migrate-check reports how a light-media database would be split from
// the legacy records table into the per-feature tables. It never writes: the
// database is opened read-only and the report goes to stdout.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"light-media/internal/app"

	_ "modernc.org/sqlite"
)

func main() {
	var dir string
	var apply bool
	flag.StringVar(&dir, "data-dir", "", "light-media data directory")
	flag.BoolVar(&apply, "apply", false, "run the split migration (the service must be stopped) and report the result")
	flag.Parse()
	if dir == "" || !filepath.IsAbs(dir) {
		fmt.Fprintln(os.Stderr, "usage: records-migrate-check --data-dir /absolute/path")
		os.Exit(2)
	}
	dbPath := filepath.Join(dir, "light-media.db")
	if st, err := os.Stat(dbPath); err != nil || !st.Mode().IsRegular() {
		fmt.Fprintln(os.Stderr, "database not found")
		os.Exit(1)
	}
	if apply {
		store, err := app.OpenStore(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		report, err := app.CheckRecordsMigration(context.Background(), store.DB)
		store.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		fmt.Fprint(os.Stderr, report.String())
		return
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	report, err := app.CheckRecordsMigration(context.Background(), db)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	fmt.Fprint(os.Stderr, report.String())
	if len(report.TargetMissing) > 0 {
		os.Exit(1)
	}
}
