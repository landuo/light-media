package networkproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestRequestsDialBindingReleaseOwnsCanceledSocket(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		client, server := net.Pipe()
		stopped := false
		// Model the exact race: cancellation callback is queued, not running, and
		// stop successfully prevents it from running when RoundTrip releases.
		binding := &requestsDialBinding{ctx: ctx, conns: []net.Conn{client}, stops: []func() bool{func() bool { stopped = true; return true }}}
		if canceled {
			cancel()
		}
		binding.release()
		if !stopped {
			t.Fatal("callback not stopped")
		}
		server.SetReadDeadline(time.Now().Add(time.Second))
		if canceled {
			var data [1]byte
			if _, err := server.Read(data[:]); err != io.EOF {
				t.Fatalf("canceled socket not closed: %v", err)
			}
		} else {
			done := make(chan error, 1)
			go func() { _, err := client.Write([]byte("x")); done <- err }()
			var data [1]byte
			if _, err := io.ReadFull(server, data[:]); err != nil || data[0] != 'x' {
				t.Fatal("successful request connection closed during release")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		client.Close()
		server.Close()
	}
}
