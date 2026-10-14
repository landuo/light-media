package main

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/fnos"
	"light-media/internal/logging"
	"os"
	"time"
)

func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	var input struct {
		Servers []fnos.Config `json:"servers"`
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil || len(b) > 65536 || json.Unmarshal(b, &input) != nil {
		_ = trace.Report(os.Stdout, map[string]string{"error": "INVALID_INPUT"}, "invalid_input")
		os.Exit(2)
	}
	if len(input.Servers) > 4 {
		_ = trace.Report(os.Stdout, map[string]string{"error": "SERVER_LIMIT"}, "invalid_input")
		os.Exit(2)
	}
	results := make([]fnos.Result, 0, len(input.Servers))
	failed := len(input.Servers) == 0
	var secrets []string
	for _, s := range input.Servers {
		secrets = append(secrets, s.Username, s.Password, s.AccessCode)
		ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 90*time.Second)
		r := fnos.Probe(ctx, s)
		cancel()
		results = append(results, r)
		failed = failed || r.Status != "passed"
	}
	status := "passed"
	if failed {
		status = "incomplete"
	}
	if trace.Report(os.Stdout, map[string]any{"mode": "live_read_only", "results": results}, status, secrets...) != nil {
		os.Exit(1)
	}
	if failed {
		os.Exit(1)
	}
}
