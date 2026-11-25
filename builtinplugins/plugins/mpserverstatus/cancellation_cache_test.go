package mpserverstatus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Caller cancellation is a typed SDK adaptation, not Python source behavior.
// A native operation's own timeout with an active caller remains cacheable.
type cancelCacheNetwork struct {
	stage    string
	cancel   context.CancelFunc
	ordinary bool
	dns, tls int
}

func (n *cancelCacheNetwork) Status(context.Context, string, time.Duration) (*Response, error) {
	return &Response{Status: 200, Protocol: 11, Text: stubStatus}, nil
}
func (n *cancelCacheNetwork) failure(stage string) error {
	if n.stage != stage {
		return nil
	}
	if n.ordinary {
		return context.DeadlineExceeded
	}
	n.cancel()
	return context.Canceled
}
func (n *cancelCacheNetwork) DNS(context.Context, string, int) ([]string, error) {
	n.dns++
	return []string{"192.0.2.1"}, n.failure("dns")
}
func (n *cancelCacheNetwork) TLS(context.Context, string, int, time.Duration) (Certificate, error) {
	n.tls++
	return Certificate{Version: "TLSv1.3", NotAfter: "Nov 30 00:00:00 2026 GMT"}, n.failure("tls")
}
func TestCallerCancellationDoesNotReplaceNetworkCaches(t *testing.T) {
	for _, stage := range []string{"dns", "tls"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := &cancelCacheNetwork{stage: stage, cancel: cancel}
			m := New(n, func() float64 { return 10000 })
			old := &cache{At: 0, Info: Object{"error": "old expired cache"}}
			if stage == "dns" {
				m.DNSCache = old
			} else {
				m.TLSCache = old
			}
			if _, err := m.LoadWith(ctx, true, n, nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if m.cached(stage == "tls") != old {
				t.Fatal("caller cancellation replaced prior cache")
			}
			n.stage = ""
			out, err := m.LoadWith(context.Background(), true, n, nil)
			if err != nil || out.Error != "" || out.DNS["error"] != "" || out.TLS["error"] != "" {
				t.Fatal(out, err)
			}
			if m.cached(stage == "tls") == old {
				t.Fatal("fresh healthy callback did not retry")
			}
			if stage == "dns" && n.dns != 2 || stage == "tls" && n.tls != 2 {
				t.Fatal(n.dns, n.tls)
			}
		})
	}
}
func TestNativeTimeoutWithActiveCallerRemainsCached(t *testing.T) {
	for _, stage := range []string{"dns", "tls"} {
		t.Run(stage, func(t *testing.T) {
			n := &cancelCacheNetwork{stage: stage, ordinary: true}
			m := New(n, func() float64 { return 10000 })
			if _, err := m.LoadWith(context.Background(), true, n, nil); err != nil {
				t.Fatal(err)
			}
			n.stage = ""
			out, err := m.LoadWith(context.Background(), true, n, nil)
			if err != nil {
				t.Fatal(err)
			}
			info := out.DNS
			if stage == "tls" {
				info = out.TLS
			}
			if info["error"] != context.DeadlineExceeded.Error() {
				t.Fatal(info)
			}
			if stage == "dns" && n.dns != 1 || stage == "tls" && n.tls != 1 {
				t.Fatal(n.dns, n.tls)
			}
		})
	}
}
