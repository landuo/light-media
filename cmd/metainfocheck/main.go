package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"light-media/internal/integrations"
	"light-media/internal/logging"
	"light-media/internal/metainfo"
)

func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	var input integrations.Input
	if err != nil || len(body) > 65536 || json.Unmarshal(body, &input) != nil {
		_ = trace.Report(os.Stdout, map[string]string{"status": "invalid_input"}, "invalid_input")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 2*time.Minute)
	defer cancel()
	report := metainfo.Run(ctx, input)
	var secrets []string
	for _, s := range input.Sites {
		secrets = append(secrets, s.Cookie, s.APIKey)
	}
	if trace.Report(os.Stdout, report, report.Status, secrets...) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
