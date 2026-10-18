package networkproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

type requestsSOCKSObservation struct {
	Version       byte
	AddressType   byte
	Address, User string
	Port          uint16
	RequestURI    string
	Err           error
}

func socksObservation(t *testing.T) (net.Listener, <-chan requestsSOCKSObservation) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan requestsSOCKSObservation, 1)
	go func() {
		observed := requestsSOCKSObservation{}
		defer func() { done <- observed }()
		conn, err := listener.Accept()
		if err != nil {
			observed.Err = err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(4 * time.Second))
		reader := bufio.NewReader(conn)
		version, err := reader.ReadByte()
		if err != nil {
			observed.Err = err
			return
		}
		observed.Version = version
		if version == 4 {
			var header [7]byte
			if _, err = io.ReadFull(reader, header[:]); err != nil {
				observed.Err = err
				return
			}
			observed.Port = binary.BigEndian.Uint16(header[1:3])
			observed.Address = net.IP(header[3:7]).String()
			user, err := reader.ReadBytes(0)
			if err != nil {
				observed.Err = err
				return
			}
			observed.User = string(user[:len(user)-1])
			if observed.Address == "0.0.0.1" {
				domain, err := reader.ReadBytes(0)
				if err != nil {
					observed.Err = err
					return
				}
				observed.Address = string(domain[:len(domain)-1])
				observed.AddressType = 3
			} else {
				observed.AddressType = 1
			}
			if _, err = conn.Write([]byte{0, 0x5a, 0, 0, 0, 0, 0, 0}); err != nil {
				observed.Err = err
				return
			}
		} else if version == 5 {
			count, err := reader.ReadByte()
			if err != nil {
				observed.Err = err
				return
			}
			if _, err = io.CopyN(io.Discard, reader, int64(count)); err != nil {
				observed.Err = err
				return
			}
			if _, err = conn.Write([]byte{5, 0}); err != nil {
				observed.Err = err
				return
			}
			var header [4]byte
			if _, err = io.ReadFull(reader, header[:]); err != nil {
				observed.Err = err
				return
			}
			observed.AddressType = header[3]
			addressLength := 0
			switch header[3] {
			case 1:
				addressLength = 4
			case 4:
				addressLength = 16
			case 3:
				n, err := reader.ReadByte()
				if err != nil {
					observed.Err = err
					return
				}
				addressLength = int(n)
			default:
				observed.Err = fmt.Errorf("unsupported owned address type")
				return
			}
			address := make([]byte, addressLength)
			if _, err = io.ReadFull(reader, address); err != nil {
				observed.Err = err
				return
			}
			if header[3] == 3 {
				observed.Address = string(address)
			} else {
				observed.Address = net.IP(address).String()
			}
			var port [2]byte
			if _, err = io.ReadFull(reader, port[:]); err != nil {
				observed.Err = err
				return
			}
			observed.Port = binary.BigEndian.Uint16(port[:])
			if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}); err != nil {
				observed.Err = err
				return
			}
		} else {
			observed.Err = fmt.Errorf("unsupported owned SOCKS version")
			return
		}
		request, err := http.ReadRequest(reader)
		if err != nil {
			observed.Err = err
			return
		}
		request.Body.Close()
		observed.RequestURI = request.RequestURI
		_, observed.Err = io.WriteString(conn, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()
	return listener, done
}

func TestRequestsTransportActualSOCKS4And5Modes(t *testing.T) {
	for _, c := range []struct {
		scheme, host, want string
		version, atyp      byte
	}{{"socks4", "127.0.0.1", "127.0.0.1", 4, 1}, {"socks4a", "owned.invalid", "owned.invalid", 4, 3}, {"socks4a", "0177.0.0.1", "127.0.0.1", 4, 1}, {"socks4", "0x7f000001", "127.0.0.1", 4, 1}, {"socks4a", "::1", "::1", 4, 3}, {"socks5", "127.0.0.1", "127.0.0.1", 5, 1}, {"socks5h", "owned.invalid", "owned.invalid", 5, 3}, {"socks5", "localhost", "", 5, 0}} {
		t.Run(c.scheme+"/"+c.host, func(t *testing.T) {
			listener, done := socksObservation(t)
			defer listener.Close()
			proxy := c.scheme + "://synthetic:pass@" + listener.Addr().String()
			if c.version == 5 {
				proxy = c.scheme + "://" + listener.Addr().String()
			}
			transport := NewRequestsTransport(nil, func() []string { return []string{"ALL_PROXY=" + proxy} })
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get("http://" + net.JoinHostPort(c.host, "8123") + "/test")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			observed := <-done
			if observed.Err != nil {
				t.Fatal(observed.Err)
			}
			if response.StatusCode != 204 || observed.Version != c.version || observed.Port != 8123 || observed.RequestURI != "/test" {
				t.Fatal(response.StatusCode, observed)
			}
			if c.want != "" && (observed.Address != c.want || observed.AddressType != c.atyp) {
				t.Fatal(observed)
			}
			if c.host == "localhost" {
				if observed.AddressType != 1 && observed.AddressType != 4 || net.ParseIP(observed.Address) == nil {
					t.Fatalf("socks5 resolved destination remotely: %+v", observed)
				}
			}
			if c.version == 4 && observed.User != "synthetic" {
				t.Fatal("source SOCKS4 user missing")
			}
		})
	}
}

func TestRequestsTransportPreservesCallerTLSRootsAndBase(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	if base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("owned test must validate certificate")
	}
	calls := 0
	base.Proxy = func(*http.Request) (*url.URL, error) {
		calls++
		return nil, errors.New("base routing must not be mutated or reused")
	}
	transport := NewRequestsTransport(base, func() []string { return nil })
	defer transport.CloseIdleConnections()
	r, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 204 || calls != 0 {
		t.Fatal(r.StatusCode, calls)
	}
	if _, err = base.Proxy(nil); err == nil {
		t.Fatal("base policy mutated")
	}
}

func TestRequestsSOCKS4CancellationClosesActualHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		var header [8]byte
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			return
		}
		close(entered)
		io.Copy(io.Discard, conn)
	}()
	transport := NewRequestsTransport(nil, func() []string { return []string{"ALL_PROXY=socks4://" + listener.Addr().String()} })
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+strconv.Itoa(8123)+"/", nil)
	done := make(chan error, 1)
	go func() { _, err := (&http.Client{Transport: transport}).Do(req); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native SOCKS4 handshake did not start")
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled actual handshake blocked")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("owned socket leaked")
	}
}
func TestRequestsTransportCancellationClosesSOCKS5AndConnect(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h", "http"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			entered, closed := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(closed)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				var first [1]byte
				if _, err = io.ReadFull(conn, first[:]); err != nil {
					return
				}
				close(entered)
				io.Copy(io.Discard, conn)
			}()
			transport := NewRequestsTransport(nil, func() []string { return []string{"ALL_PROXY=" + scheme + "://" + listener.Addr().String()} })
			defer transport.CloseIdleConnections()
			target := "http://127.0.0.1:8123/test"
			if scheme == "socks5h" {
				target = "http://owned.invalid/test"
			}
			if scheme == "http" {
				target = "https://owned.invalid/test"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
			done := make(chan error, 1)
			go func() {
				response, err := (&http.Client{Transport: transport}).Do(req)
				if response != nil {
					response.Body.Close()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("owned handshake did not start")
			}
			cancel()
			select {
			case err = <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("request canceled but blocked")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("owned handshake socket leaked")
			}
		})
	}
}
