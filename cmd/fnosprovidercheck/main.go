package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"light-media/internal/fnos"
	"light-media/internal/fnosprovider"
	"light-media/internal/logging"
)

func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	var cfg fnos.Config
	if err != nil || len(body) > 65536 || json.Unmarshal(body, &cfg) != nil {
		_ = trace.Report(os.Stdout, map[string]string{"status": "invalid_input"}, "invalid_input")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 60*time.Second)
	defer cancel()
	report := fnosprovider.Probe(ctx, cfg)
	if trace.Report(os.Stdout, report, report.Status, cfg.Username, cfg.Password, cfg.AccessCode) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
