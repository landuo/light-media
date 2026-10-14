package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"light-media/internal/sqlitebaseline"
)

func main() {
	o := sqlitebaseline.DefaultOptions()
	flag.StringVar(&o.Driver, "driver", o.Driver, "modernc or mattn (must be included in build)")
	flag.StringVar(&o.ParentDir, "temp-parent", "", "parent for a newly created synthetic directory")
	flag.BoolVar(&o.Keep, "keep", false, "retain the newly created synthetic directory")
	flag.IntVar(&o.Subscriptions, "subscriptions", o.Subscriptions, "synthetic subscriptions, 1..1000")
	flag.IntVar(&o.Metadata, "metadata", o.Metadata, "synthetic metadata, subscriptions..20000")
	flag.IntVar(&o.Files, "files", o.Files, "synthetic file records, 1..200000")
	flag.IntVar(&o.ClaimAttempts, "claim-attempts", o.ClaimAttempts, "reservation attempts, 1..10000")
	flag.IntVar(&o.UniqueKeys, "unique-keys", o.UniqueKeys, "distinct acquisition keys, 1..claim-attempts")
	flag.IntVar(&o.Readers, "readers", o.Readers, "read workers, 1..8 (connection pool capped at 4)")
	flag.IntVar(&o.ReadsPerReader, "reads-per-reader", o.ReadsPerReader, "paged reads per worker, 1..1000")
	timeout := flag.Duration("timeout", 90*time.Second, "whole workload deadline, max 5m")
	crashChild := flag.Bool("internal-crash-child", false, "internal synthetic crash recovery helper")
	crashDB := flag.String("db", "", "internal helper only; cannot be used as benchmark target")
	flag.Parse()
	if *timeout <= 0 || *timeout > 5*time.Minute || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "invalid timeout or unexpected arguments")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if *crashChild {
		if err := sqlitebaseline.CrashChild(ctx, o.Driver, *crashDB); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *crashDB != "" {
		fmt.Fprintln(os.Stderr, "--db is internal-only; benchmark creates a fresh database")
		os.Exit(2)
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	o.CrashCommand = func(ctx context.Context, driver, path string) *exec.Cmd {
		return exec.CommandContext(ctx, self, "--internal-crash-child", "--driver", driver, "--db", path)
	}
	r, err := sqlitebaseline.Run(ctx, o)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encodingErr := encoder.Encode(r); encodingErr != nil {
		fmt.Fprintln(os.Stderr, encodingErr)
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}
