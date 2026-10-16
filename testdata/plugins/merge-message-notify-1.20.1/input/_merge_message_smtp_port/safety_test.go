package mergemessagesmtp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	merge "light-media/work/mergemessagenotify"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func successCase(t *testing.T, mode string) wireCase {
	t.Helper()
	var found wireCase
	corpus(t, "testdata/smtp-python-3.12.13-wire.jsonl.gz", func(raw json.RawMessage) {
		var c wireCase
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		if c.ID == "success-"+mode {
			found = c
		}
	})
	if found.ID == "" {
		t.Fatal("missing source case")
	}
	return found
}
func TestDefaultRejectsUntrustedTLSCertificate(t *testing.T) {
	serverTLS, _ := testCertificate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(2 * time.Second))
		secure := tls.Server(raw, serverTLS)
		done <- secure.HandshakeContext(ctx)
	}()
	r := successCase(t, "SSL").Request
	port := listener.Addr().(*net.TCPAddr).Port
	r.Port = port
	a, e := New(Binding{Host: r.Host, Port: port, Encrypt: r.Encrypt, Username: r.Username, Password: r.Password, FromName: r.FromName, Recipients: r.To}, Options{LocalHostname: "client.synthetic.invalid"})
	if e != nil {
		t.Fatal(e)
	}
	err := a.Send(ctx, r)
	var smtp *Error
	if !errors.As(err, &smtp) || smtp.Class != "SSLCertVerificationError" {
		t.Fatal("untrusted server accepted", err)
	}
	if e := <-done; e == nil {
		t.Fatal("TLS server completed untrusted handshake")
	}
}
func TestContextCancellationClosesOwnedSocketWithoutRetry(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			close(accepted)
			close(closed)
			return
		}
		defer conn.Close()
		close(accepted)
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 1)
		conn.Read(b)
		close(closed)
	}()
	r := successCase(t, "NONE").Request
	port := listener.Addr().(*net.TCPAddr).Port
	r.Port = port
	dials := 0
	dialer := &net.Dialer{}
	a, e := New(Binding{Host: r.Host, Port: port, Encrypt: r.Encrypt, Username: r.Username, Password: r.Password, FromName: r.FromName, Recipients: r.To}, Options{LocalHostname: "client.synthetic.invalid", DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials++
		return dialer.DialContext(ctx, network, addr)
	}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- a.Send(ctx, r) }()
	<-accepted
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop SMTP")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned socket leaked")
	}
	if dials != 1 {
		t.Fatal("automatic retry", dials)
	}
}
func TestConcurrentBoundSessions(t *testing.T) {
	serverTLS, clientTLS := testCertificate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	c := successCase(t, "TLS")
	r := c.Request
	port := listener.Addr().(*net.TCPAddr).Port
	r.Port = port
	a, e := New(Binding{Host: r.Host, Port: port, Encrypt: r.Encrypt, Username: r.Username, Password: r.Password, FromName: r.FromName, Recipients: r.To}, Options{LocalHostname: "client.synthetic.invalid", TLSConfig: clientTLS, Timeout: 3 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	const workers = 12
	servers := make([]<-chan error, workers)
	for i := range servers {
		servers[i] = runReplay(ctx, listener, serverTLS, c)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Go(func() {
			if e := a.Send(ctx, r); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	for _, done := range servers {
		if e := <-done; e != nil {
			t.Error(e)
		}
	}
}
func TestMergeSDKFixtureProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--merge-smtp-worker" {
			if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, merge.Manifest(), merge.Handle); e != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func TestActualFrozenMergeAndSDKSendSMTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	serverTLS, _ := testCertificate(t)
	c := successCase(t, "NONE")
	done := runReplay(ctx, listener, serverTLS, c)
	r := c.Request
	port := listener.Addr().(*net.TCPAddr).Port
	r.Port = port
	a, e := New(Binding{Host: r.Host, Port: port, Encrypt: r.Encrypt, Username: r.Username, Password: r.Password, FromName: r.FromName, Recipients: r.To}, Options{LocalHostname: "client.synthetic.invalid"})
	if e != nil {
		t.Fatal(e)
	}
	p := merge.New(merge.Host{SMTP: a.Send, Timezone: "Asia/Shanghai", Now: func() time.Time { return time.Unix(1700000000, 125000000) }})
	config := map[string]any{"enable": true, "enable_channels": []any{"custom.email"}, "custom.email.smtp_host": r.Host, "custom.email.smtp_port": port, "custom.email.smtp_encrypt_type": r.Encrypt, "custom.email.username": r.Username, "custom.email.password": r.Password, "custom.email.from_name": r.FromName, "custom.email.to_addrs": strings.Join(r.To, ",")}
	host := func(ctx context.Context, id plugins.Identity, cap string, raw json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "MergeMessageNotify" || id.InstanceID != "synthetic-smtp" {
			return nil, ErrBinding
		}
		switch cap {
		case merge.ConfigureCapability:
			if string(raw) != "{}" {
				return nil, ErrBinding
			}
			return json.RawMessage(`null`), p.Init(ctx, config, false)
		case merge.EventCapability:
			if string(raw) != `{"event_id":"owned-smtp-notice"}` {
				return nil, ErrBinding
			}
			report := p.DispatchEvent(ctx, map[string]any{"title": r.Title, "text": r.Text, "image": "https://image.invalid/p.png", "type": merge.NotifyType("Plugin")})
			if report.Success != 1 || report.Failed != 0 {
				return nil, errors.New("source SMTP algorithm did not send")
			}
			return json.RawMessage(`null`), nil
		}
		return nil, ErrBinding
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	command := plugins.Command{Path: exe, Args: []string{"-test.run=^TestMergeSDKFixtureProcess$", "--", "--merge-smtp-worker"}}
	for _, action := range []string{"init", "event"} {
		raw, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"custom.email.smtp_host":"worker-cannot-select.invalid"}`), Input: json.RawMessage(`{"event_id":"owned-smtp-notice"}`)})
		result, e := (plugins.Runner{}).Run(ctx, command, merge.Manifest(), "synthetic-smtp", "invoke", raw, host)
		if e != nil || result.HostCalls != 1 || string(result.Result) != "null" {
			t.Fatal("actual SDK", action, e, result.HostCalls)
		}
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
