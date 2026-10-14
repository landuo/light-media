package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"light-media/internal/integrations"
	"light-media/internal/metainfo"
)

func main() {
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	var input integrations.Input
	if err != nil || len(body) > 65536 || json.Unmarshal(body, &input) != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "invalid_input"})
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := metainfo.Run(ctx, input)
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
