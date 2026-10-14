package main

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/integrations"
	"os"
	"time"
)

func main() {
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	var in integrations.Input
	if e != nil || len(b) > 65536 || json.Unmarshal(b, &in) != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "invalid_input"})
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := integrations.Run(ctx, in)
	_ = json.NewEncoder(os.Stdout).Encode(report)
	if report.Status != "ok" {
		os.Exit(1)
	}
}
