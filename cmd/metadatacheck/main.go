package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"light-media/internal/logging"
	"light-media/internal/metadata"
)

func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	if len(os.Args) != 1 {
		fail(trace)
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil {
		fail(trace)
	}
	credentials, err := metadata.ParseCredentials(body)
	if err != nil {
		fail(trace)
	}
	ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 60*time.Second)
	defer cancel()
	report := metadata.Run(ctx, credentials)
	if trace.Report(os.Stdout, report, report.Status, credentials.APIKey, credentials.AccessToken) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
func fail(trace *logging.Run) {
	_ = trace.Report(os.Stdout, map[string]string{"status": "invalid_input"}, "invalid_input")
	os.Exit(2)
}
