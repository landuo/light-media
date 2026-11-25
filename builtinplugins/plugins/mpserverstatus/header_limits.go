package mpserverstatus

import (
	"bufio"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
)

var errSourceHTTPHeaderLimit = errors.New("source HTTP physical header limit exceeded")
var errSourceHTTPStatus = errors.New("source HTTP invalid status line")

type sourceResponseHead struct {
	status, protocol int
	headers          http.Header
}

// This fixed-probe adapter retains source physical line limits and Python's
// status/header lexical boundary. Body framing/redirects remain net/http.
// It strips only the 100 blocks http.client.begin itself skips. Source final
// 101/103/etc become an internal empty response; public metadata is restored.
type sourceHeaderConn struct {
	net.Conn
	mu                sync.Mutex
	armed, statusLine bool
	proxy             bool
	line              []byte
	lines             [][]byte
	pending           []byte
	headers           int
	meta              sourceResponseHead
}

func wrapSourceHeaders(c net.Conn) net.Conn { return &sourceHeaderConn{Conn: c} }
func (c *sourceHeaderConn) arm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = true
	c.statusLine = true
	c.line = nil
	c.lines = nil
	c.headers = 0
	c.meta = sourceResponseHead{}
}
func (c *sourceHeaderConn) head() sourceResponseHead {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.meta
	m.headers = m.headers.Clone()
	return m
}
func sourceHTTPInteger(s string) (*big.Int, bool) {
	var b strings.Builder
	digit := false
	count := 0
	for i, r := range s {
		if i == 0 && (r == '+' || r == '-') {
			b.WriteRune(r)
			continue
		}
		if r == '_' {
			if !digit {
				return nil, false
			}
			digit = false
			continue
		}
		if r < '0' || r > '9' {
			return nil, false
		}
		b.WriteRune(r)
		digit = true
		count++
	}
	if !digit || count > 4300 {
		return nil, false
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	return n, ok
}
func sourceStatus(line []byte) (int, int, error) {
	fields := strings.FieldsFunc(httpHeaderText(string(line)), pythonSpace)
	if len(fields) < 2 {
		return 0, 0, errSourceHTTPStatus
	}
	version := 0
	switch {
	case fields[0] == "HTTP/1.0" || fields[0] == "HTTP/0.9":
		version = 10
	case strings.HasPrefix(fields[0], "HTTP/1."):
		version = 11
	default:
		return 0, 0, errSourceHTTPStatus
	}
	n, ok := sourceHTTPInteger(fields[1])
	if !ok || !n.IsInt64() || n.Int64() < 100 || n.Int64() > 999 {
		return 0, 0, errSourceHTTPStatus
	}
	return int(n.Int64()), version, nil
}
func sourceHeaderFields(lines [][]byte) http.Header {
	h := http.Header{}
	last := ""
	stopped := false
	for _, raw := range lines {
		line := strings.TrimRight(string(raw), "\r\n")
		if line == "" {
			break
		}
		if stopped {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if last != "" {
				v := h[last]
				v[len(v)-1] += "\r\n" + line
				h[last] = v
			}
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 1 {
			stopped = true
			continue
		}
		name := line[:colon]
		valid := true
		for _, b := range []byte(name) {
			if b < 33 || b > 126 || b == ':' {
				valid = false
				break
			}
		}
		if !valid {
			stopped = true
			continue
		}
		last = http.CanonicalHeaderKey(name)
		h[last] = append(h[last], strings.TrimLeft(line[colon+1:], " \t"))
	}
	return h
}
func normalizedSourceHead(m sourceResponseHead) ([]byte, error) {
	version := "HTTP/1.1"
	if m.protocol == 10 {
		version = "HTTP/1.0"
	}
	status := m.status
	if status < 200 {
		status = 200
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s %d source\r\n", version, status)
	for key, values := range m.headers {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Connection") {
			continue
		}
		valid := true
		for _, r := range key {
			if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		for _, v := range values {
			v = strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", " "), "\n", " ")
			valid := true
			for _, b := range []byte(v) {
				if b < 32 && b != '\t' || b == 127 {
					valid = false
					break
				}
			}
			if valid {
				fmt.Fprintf(&out, "%s: %s\r\n", key, v)
			}
		}
	}
	if m.status < 200 {
		out.WriteString("Content-Length: 0\r\nConnection: close\r\n")
	} else {
		if strings.ToLower(m.headers.Get("Transfer-Encoding")) == "chunked" {
			out.WriteString("Transfer-Encoding: chunked\r\n")
		} else if values := m.headers.Values("Content-Length"); len(values) > 0 {
			var length *big.Int
			for _, v := range values {
				n, ok := sourceHTTPInteger(strings.TrimFunc(v, pythonSpace))
				if !ok || n.Sign() < 0 {
					length = nil
					break
				}
				if length != nil && length.Cmp(n) != 0 {
					return nil, errSourceHTTPStatus
				}
				length = n
			}
			if length != nil {
				fmt.Fprintf(&out, "Content-Length: %s\r\n", length.String())
			}
		}
		connection := strings.ToLower(m.headers.Get("Connection"))
		if strings.Contains(connection, "close") {
			out.WriteString("Connection: close\r\n")
		} else if strings.Contains(connection, "keep-alive") {
			out.WriteString("Connection: keep-alive\r\n")
		}
	}
	out.WriteString("\r\n")
	return []byte(out.String()), nil
}
func (c *sourceHeaderConn) accept(data []byte) error {
	for i, b := range data {
		if !c.armed {
			c.pending = append(c.pending, data[i:]...)
			return nil
		}
		c.line = append(c.line, b)
		if len(c.line) > 65536 {
			return errSourceHTTPHeaderLimit
		}
		if b != '\n' {
			continue
		}
		if c.statusLine {
			status, version, e := sourceStatus(c.line)
			if e != nil {
				return e
			}
			c.meta = sourceResponseHead{status: status, protocol: version}
			c.statusLine = false
		} else {
			c.headers++
			if c.headers > 100 {
				return errSourceHTTPHeaderLimit
			}
			if string(c.line) == "\r\n" || string(c.line) == "\n" {
				if c.meta.status == 100 && !c.proxy {
					c.statusLine = true
					c.headers = 0
					c.lines = nil
				} else {
					c.meta.headers = sourceHeaderFields(c.lines)
					normal := c.meta
					if c.proxy {
						normal = sourceResponseHead{status: 200, protocol: 11, headers: http.Header{"Content-Length": []string{"0"}}}
					}
					wire, e := normalizedSourceHead(normal)
					if e != nil {
						return e
					}
					c.pending = append(c.pending, wire...)
					c.armed = false
					if c.meta.status < 200 {
						c.line = nil
						return nil
					}
				}
			} else {
				c.lines = append(c.lines, append([]byte(nil), c.line...))
			}
		}
		c.line = nil
	}
	return nil
}
func (c *sourceHeaderConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.pending) > 0 {
			n := copy(p, c.pending)
			c.pending = c.pending[n:]
			c.mu.Unlock()
			return n, nil
		}
		c.mu.Unlock()
		size := len(p)
		if size < 4096 {
			size = 4096
		}
		buffer := make([]byte, size)
		n, e := c.Conn.Read(buffer)
		c.mu.Lock()
		scan := c.accept(buffer[:n])
		c.mu.Unlock()
		if scan != nil {
			return 0, scan
		}
		if e != nil {
			c.mu.Lock()
			has := len(c.pending) > 0
			c.mu.Unlock()
			if !has {
				return 0, e
			}
		}
	}
}

func sourceProxyResponse(c net.Conn, request *http.Request) (net.Conn, error) {
	meter := &sourceHeaderConn{Conn: c, proxy: true}
	meter.arm()
	if _, e := http.ReadResponse(bufio.NewReader(meter), request); e != nil {
		return nil, e
	}
	if meter.head().status != 200 {
		return nil, errors.New("requests_proxy_connect_rejected")
	}
	return meter, nil
}
