package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"light-media/internal/metadata"
)

func main() {
	if len(os.Args) != 1 {
		fail()
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil {
		fail()
	}
	credentials, err := metadata.ParseCredentials(body)
	if err != nil {
		fail()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report := metadata.Run(ctx, credentials)
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(1)
	}
	if report.Status != "ok" {
		os.Exit(1)
	}
}
func fail() {
	json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "invalid_input"})
	os.Exit(2)
}
