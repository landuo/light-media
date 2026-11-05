// Actual owned HTTP trace demonstrating Go's POST replay classification.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
)

func main() {
	rows := []map[string]any{}
	for _, withKey := range []bool{false, true} {
		var again atomic.Int32
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
			if r.URL.Path == "/begin" {
				w.Header().Set("Location", "/again")
				w.Header().Set("Content-Length", "0")
				w.WriteHeader(307)
				return
			}
			if again.Add(1) == 1 {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil { panic(err) }
				_ = connection.Close() // Request body was received; no response supplied.
				return
			}
			_, _ = io.WriteString(w, "owned-second-write")
		}))
		transport := http.DefaultTransport.(*http.Transport).Clone()
		client := &http.Client{Transport: transport}
		request, err := http.NewRequest("POST", peer.URL+"/begin", bytes.NewReader([]byte("owned=write")))
		if err != nil { panic(err) }
		if withKey { request.Header.Set("Idempotency-Key", "owned-private-binding") }
		response, err := client.Do(request)
		if response != nil { _, _ = io.Copy(io.Discard, response.Body); _ = response.Body.Close() }
		rows = append(rows, map[string]any{"idempotency_key": withKey, "again_body_receipts": again.Load(), "response_present": response != nil, "request_error": err != nil})
		transport.CloseIdleConnections()
		peer.Close()
	}
	if rows[0]["again_body_receipts"] != int32(1) || rows[1]["again_body_receipts"] != int32(2) { panic("causal connection-reuse trace did not execute") }
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"classification": "Actual owned307 POST plus closed-response reused connection; standard Go transport alone, not a product pass claim", "go_version": runtime.Version(), "cases": rows})
}
