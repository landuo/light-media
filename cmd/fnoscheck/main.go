package main

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/fnos"
	"os"
	"time"
)

func main() {
	var input struct {
		Servers []fnos.Config `json:"servers"`
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil || len(b) > 65536 || json.Unmarshal(b, &input) != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "INVALID_INPUT"})
		os.Exit(2)
	}
	if len(input.Servers) > 4 {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "SERVER_LIMIT"})
		os.Exit(2)
	}
	results := make([]fnos.Result, 0, len(input.Servers))
	failed := len(input.Servers) == 0
	for _, s := range input.Servers {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		r := fnos.Probe(ctx, s)
		cancel()
		results = append(results, r)
		failed = failed || r.Status != "passed"
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "live_read_only", "results": results})
	if failed {
		os.Exit(1)
	}
}
