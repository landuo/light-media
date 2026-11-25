package iyuuautoseed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real transport -> Helper -> Core boundary. Exhausted network
// attempts must not become a successful empty batch or reach downloader writes.
func TestCoreTransportFailureIsNotEmptySeedResult(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "valid_empty_result"
		if failed {
			name = "network_exhausted"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				calls.Add(1)
				if failed {
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			}))
			defer server.Close()
			defer close(release)
			api, err := NewAPITransport(TrustedAPIBinding{Token: "owned-fixture-token"}, APITransportOptions{
				Guard: func(context.Context) error { return nil }, Environment: []string{},
				LoopbackOrigin: server.URL, Timeout: 40 * time.Millisecond, TransientRetry: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			helper, err := RestoreHelper(api, func() time.Time { return time.Unix(1700000000, 0) }, HelperState{SID: "owned-fixture-sid"})
			if err != nil {
				t.Fatal(err)
			}
			// A nil host deliberately makes any accidental downloader/cache mutation fail.
			core := &Core{api: helper}
			var logs []SourceLog
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			ctx = WithSourceLogSink(ctx, func(_ context.Context, event SourceLog) { logs = append(logs, event) })
			err = core.seedTorrents(ctx, []selectedTorrent{{Hash: replayHash(1), SavePath: "/owned/fixture"}}, Service{Name: "owned"}, map[string]bool{})
			if failed {
				if err == nil {
					t.Fatal("exhausted transport was misreported as a successful empty batch")
				}
				if calls.Load() != 2 {
					t.Fatalf("want exactly two network attempts, got %d", calls.Load())
				}
				if strings.Contains(err.Error(), "owned-fixture-token") || strings.Contains(err.Error(), server.URL) {
					t.Fatal("transport error exposed credentials or URL")
				}
				for _, log := range logs {
					if log.Line == 954 || log.Line == 1005 || log.Line == 858 {
						t.Fatalf("network failure emitted success/empty source log %+v", log)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatalf("successful empty result was retried %d times", calls.Load())
				}
				found := false
				for _, log := range logs {
					if log.Line == 1005 {
						found = true
					}
				}
				if !found {
					t.Fatal("valid empty result did not complete the batch")
				}
			}
			if core.counters != (Counters{}) || len(core.attempts) != 0 || len(core.queue) != 0 {
				t.Fatal("query-only fixture changed mutation state")
			}
		})
	}
}
