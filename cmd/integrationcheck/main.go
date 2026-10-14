package main

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/integrations"
	"light-media/internal/logging"
	"os"
	"time"
)

func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	var in integrations.Input
	if e != nil || len(b) > 65536 || json.Unmarshal(b, &in) != nil {
		_ = trace.Report(os.Stdout, map[string]string{"status": "invalid_input"}, "invalid_input")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 2*time.Minute)
	defer cancel()
	report := integrations.Run(ctx, in)
	var secrets []string
	for _, d := range in.Downloaders {
		secrets = append(secrets, d.Username, d.Password)
	}
	for _, s := range in.Sites {
		secrets = append(secrets, s.Cookie, s.APIKey)
	}
	if trace.Report(os.Stdout, report, report.Status, secrets...) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
