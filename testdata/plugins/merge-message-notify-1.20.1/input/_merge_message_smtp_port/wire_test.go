package mergemessagesmtp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	merge "light-media/work/mergemessagenotify"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func testCertificate(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1234567), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Unix(1672531200, 0), NotAfter: time.Unix(2051222400, 0), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
}

type wireFrame struct {
	Server string `json:"s"`
	Client string `json:"c"`
	TLS    bool   `json:"tls"`
	EOF    bool   `json:"eof"`
	Close  bool   `json:"close"`
}
type wireCase struct {
	ID      string
	Request merge.SMTPRequest
	Wire    []wireFrame
	Outcome struct {
		Return *bool  `json:"return"`
		Error  string `json:"error"`
	}
}

func runReplay(ctx context.Context, listener net.Listener, serverTLS *tls.Config, c wireCase) <-chan error {
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer func() { conn.Close() }()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		if c.Request.Encrypt == "SSL" {
			secure := tls.Server(conn, serverTLS)
			if e = secure.HandshakeContext(ctx); e != nil {
				done <- e
				return
			}
			conn = secure
		}
		for i, frame := range c.Wire {
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			if frame.Server != "" {
				raw, e := base64.StdEncoding.DecodeString(frame.Server)
				if e != nil {
					done <- e
					return
				}
				if _, e = conn.Write(raw); e != nil {
					done <- fmt.Errorf("frame%d server write: %w", i, e)
					return
				}
			} else if frame.Client != "" {
				want, e := base64.StdEncoding.DecodeString(frame.Client)
				if e != nil {
					done <- e
					return
				}
				got := make([]byte, len(want))
				if _, e = io.ReadFull(conn, got); e != nil {
					done <- fmt.Errorf("frame%d client read: %w got=%q want=%q", i, e, got, want)
					return
				}
				if string(got) != string(want) {
					done <- fmt.Errorf("frame%d client bytes differ got=%q want=%q", i, got, want)
					return
				}
			} else if frame.TLS {
				secure := tls.Server(conn, serverTLS)
				if e = secure.HandshakeContext(ctx); e != nil {
					done <- e
					return
				}
				conn = secure
			} else if frame.EOF {
				b := make([]byte, 1)
				n, e := conn.Read(b)
				if n != 0 || e == nil {
					done <- fmt.Errorf("frame%d expected EOF got=%q error=%v", i, b[:n], e)
					return
				}
			} else if frame.Close {
				conn.Close()
			}
		}
		done <- nil
	}()
	return done
}
func TestActualPythonSMTPWire(t *testing.T) {
	serverTLS, clientTLS := testCertificate(t)
	count := 0
	corpus(t, "testdata/smtp-python-3.12.13-wire.jsonl.gz", func(raw json.RawMessage) {
		var c wireCase
		if e := json.Unmarshal(raw, &c); e != nil {
			t.Fatal(e)
		}
		t.Run(c.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			done := runReplay(ctx, listener, serverTLS, c)
			port := listener.Addr().(*net.TCPAddr).Port
			r := c.Request
			r.Port = port
			binding := Binding{Host: r.Host, Port: port, Encrypt: r.Encrypt, Username: r.Username, Password: r.Password, FromName: r.FromName, Recipients: r.To}
			a, e := New(binding, Options{LocalHostname: "client.synthetic.invalid", TLSConfig: clientTLS, Timeout: 2 * time.Second})
			if e != nil {
				t.Fatal(e)
			}
			e = a.Send(ctx, r)
			if c.Outcome.Error != "" {
				var quit merge.SMTPQuitError
				var smtp *Error
				if !errors.As(e, &quit) || !errors.As(quit.Err, &smtp) || smtp.Class != c.Outcome.Error {
					t.Errorf("original error %s got %v", c.Outcome.Error, e)
				}
			} else if c.Outcome.Return != nil && *c.Outcome.Return {
				if e != nil {
					t.Errorf("original True got %v", e)
				}
			} else {
				if e == nil {
					t.Error("original False got success")
				}
				var quit merge.SMTPQuitError
				if errors.As(e, &quit) {
					t.Errorf("original False got escaping finally %v", e)
				}
			}
			select {
			case e := <-done:
				if e != nil {
					t.Error(e)
				}
			case <-ctx.Done():
				t.Fatal("owned replay did not finish")
			}
		})
		count++
	})
	if count != 44 {
		t.Fatal("wire corpus count", count)
	}
	t.Logf("%d real Python source SMTP application-wire cases", count)
}
func TestTrustedBindingAndStrictTLSConfiguration(t *testing.T) {
	base := Binding{Host: "127.0.0.1", Port: 2525, Encrypt: "NONE", Username: "sender@example.invalid", Password: "synthetic-secret", FromName: "MoviePilot", Recipients: []string{"one@example.invalid"}}
	dialed := 0
	a, e := New(base, Options{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialed++
		return nil, errors.New("synthetic dial")
	}})
	if e != nil {
		t.Fatal(e)
	}
	r := merge.SMTPRequest{Host: base.Host, Port: base.Port, Encrypt: base.Encrypt, Username: base.Username, Password: base.Password, From: base.Username, FromName: base.FromName, To: append([]string(nil), base.Recipients...), Title: "title", Text: "body", Date: "Wed, 15 Nov 2023 06:13:20 +0800"}
	for _, change := range []func(*merge.SMTPRequest){func(v *merge.SMTPRequest) { v.Host = "foreign.invalid" }, func(v *merge.SMTPRequest) { v.Port = 25 }, func(v *merge.SMTPRequest) { v.Encrypt = "SSL" }, func(v *merge.SMTPRequest) { v.Username = "other@example.invalid" }, func(v *merge.SMTPRequest) { v.Password = "other" }, func(v *merge.SMTPRequest) { v.From = "other@example.invalid" }, func(v *merge.SMTPRequest) { v.FromName = "other" }, func(v *merge.SMTPRequest) { v.To = []string{"other@example.invalid"} }, func(v *merge.SMTPRequest) { v.To = append(v.To, "other@example.invalid") }} {
		v := r
		v.To = append([]string(nil), r.To...)
		change(&v)
		if !errors.Is(a.Send(context.Background(), v), ErrBinding) {
			t.Fatal("ungranted effect accepted")
		}
	}
	if dialed != 0 {
		t.Fatal("ungranted request dialed")
	}
	if _, e := New(base, Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}}); !errors.Is(e, ErrBinding) {
		t.Fatal("insecure TLS accepted")
	}
	copyBinding := base
	copyBinding.Recipients[0] = "mutated@example.invalid"
	if a.binding.Recipients[0] != "one@example.invalid" {
		t.Fatal("mutable binding retained")
	}
	err := a.Send(context.Background(), r)
	if err == nil || strings.Contains(err.Error(), base.Password) || strings.Contains(err.Error(), base.Username) {
		t.Fatal("unsafe error disclosure", err)
	}
}
