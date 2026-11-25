package mergemessagesmtp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type timeoutConn struct {
	net.Conn
	ctx     context.Context
	timeout time.Duration
}

func (c *timeoutConn) deadline() time.Time {
	d := time.Now().Add(c.timeout)
	if v, ok := c.ctx.Deadline(); ok && v.Before(d) {
		d = v
	}
	return d
}
func (c *timeoutConn) Read(b []byte) (int, error) {
	c.Conn.SetReadDeadline(c.deadline())
	return c.Conn.Read(b)
}
func (c *timeoutConn) Write(b []byte) (int, error) {
	c.Conn.SetWriteDeadline(c.deadline())
	return c.Conn.Write(b)
}

type client struct {
	conn              net.Conn
	reader            *bufio.Reader
	features          map[string]string
	ehlo, helo, esmtp bool
	local             string
	closed            bool
	cancel            func() bool
}

func (a *Adapter) connect(ctx context.Context) (*client, error) {
	dial := a.options.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: a.options.Timeout}).DialContext
	}
	conn, e := dial(ctx, "tcp", net.JoinHostPort(a.binding.Host, strconv.Itoa(a.binding.Port)))
	if e != nil {
		return nil, failure("OSError", "connect")
	}
	timed := &timeoutConn{conn, ctx, a.options.Timeout}
	c := &client{conn: timed, features: map[string]string{}}
	c.cancel = context.AfterFunc(ctx, func() { conn.Close() })
	if a.binding.Encrypt == "SSL" {
		secure := tls.Client(c.conn, a.options.TLSConfig.Clone())
		if e = secure.HandshakeContext(ctx); e != nil {
			c.close()
			return nil, tlsFailure(e, "connect_tls")
		}
		c.conn = secure
	}
	c.reader = bufio.NewReader(c.conn)
	code, _, e := c.reply()
	if e != nil {
		c.close()
		return nil, e
	}
	if code != 220 {
		c.close()
		return nil, &Error{Class: "SMTPConnectError", Phase: "greeting", Code: code}
	}
	c.local = a.options.LocalHostname
	if c.local == "" {
		c.local = localHostname()
	}
	return c, nil
}
func localHostname() string {
	name, _ := os.Hostname()
	if strings.Contains(name, ".") {
		return name
	}
	ips, e := net.LookupHost(name)
	if e == nil && len(ips) > 0 {
		names, e := net.LookupAddr(ips[0])
		if e == nil {
			for _, n := range names {
				n = strings.TrimSuffix(n, ".")
				if strings.Contains(n, ".") {
					return n
				}
			}
		}
		for _, ip := range ips {
			if v := net.ParseIP(ip); v != nil && v.To4() != nil {
				return "[" + v.String() + "]"
			}
		}
	}
	return "[127.0.0.1]"
}
func (c *client) close() {
	if c.closed {
		return
	}
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}
func (c *client) send(raw []byte) error {
	if c.closed {
		return failure("SMTPServerDisconnected", "write")
	}
	for len(raw) > 0 {
		n, e := c.conn.Write(raw)
		if e != nil || n == 0 {
			c.close()
			return failure("SMTPServerDisconnected", "write")
		}
		raw = raw[n:]
	}
	return nil
}
func (c *client) put(cmd, args string) error {
	s := cmd
	if args != "" {
		s += " " + args
	}
	if strings.ContainsAny(s, "\r\n") {
		return failure("ValueError", "command")
	}
	if !ascii(s) {
		return failure("UnicodeEncodeError", "command")
	}
	return c.send([]byte(s + "\r\n"))
}
func (c *client) reply() (int, string, error) {
	var parts []string
	for {
		line := []byte{}
		for len(line) <= 8192 {
			b, e := c.reader.ReadByte()
			if e != nil {
				if errors.Is(e, io.EOF) && len(line) > 0 {
					break
				}
				c.close()
				return 0, "", failure("SMTPServerDisconnected", "reply")
			}
			line = append(line, b)
			if b == '\n' {
				break
			}
		}
		if len(line) > 8192 {
			c.close()
			return 0, "", &Error{Class: "SMTPResponseException", Phase: "reply", Code: 500}
		}
		part := []byte{}
		if len(line) > 4 {
			part = line[4:]
		}
		parts = append(parts, string(bytes.Trim(part, " \t\r\n")))
		prefix := line
		if len(prefix) > 3 {
			prefix = prefix[:3]
		}
		code, e := strconv.Atoi(strings.Trim(string(prefix), " \t\r\n\v\f"))
		if e != nil {
			code = -1
		}
		if e != nil || len(line) < 4 || line[3] != '-' {
			return code, strings.Join(parts, "\n"), nil
		}
	}
}
func (c *client) command(cmd, args string) (int, string, error) {
	if e := c.put(cmd, args); e != nil {
		return 0, "", e
	}
	return c.reply()
}
func (c *client) hello() error {
	if c.ehlo || c.helo {
		return nil
	}
	c.features = map[string]string{}
	code, msg, e := c.command("ehlo", c.local)
	if e != nil {
		return e
	}
	if code == -1 && msg == "" {
		c.close()
		return failure("SMTPServerDisconnected", "ehlo")
	}
	c.ehlo = true
	if code == 250 {
		c.esmtp = true
		lines := strings.Split(latin1(msg), "\n")
		for _, line := range lines[1:] {
			if len(line) >= 5 && strings.EqualFold(line[:5], "auth=") {
				c.features["auth"] += " " + line[5:]
				continue
			}
			i := 0
			for i < len(line) && ((line[i] >= 'a' && line[i] <= 'z') || (line[i] >= 'A' && line[i] <= 'Z') || (line[i] >= '0' && line[i] <= '9') || i > 0 && line[i] == '-') {
				i++
			}
			if i == 0 {
				continue
			}
			feature := strings.ToLower(line[:i])
			params := strings.TrimSpace(line[i:])
			if feature == "auth" {
				c.features[feature] += " " + params
			} else {
				c.features[feature] = params
			}
		}
	}
	if code >= 200 && code <= 299 {
		return nil
	}
	code, _, e = c.command("helo", c.local)
	if e != nil {
		return e
	}
	c.helo = true
	if code < 200 || code > 299 {
		return &Error{Class: "SMTPHeloError", Phase: "helo", Code: code}
	}
	return nil
}
func (c *client) startTLS(ctx context.Context, config *tls.Config) error {
	if e := c.hello(); e != nil {
		return e
	}
	if _, ok := c.features["starttls"]; !ok {
		return failure("SMTPNotSupportedError", "starttls")
	}
	code, _, e := c.command("STARTTLS", "")
	if e != nil {
		return e
	}
	if code != 220 {
		return &Error{Class: "SMTPResponseException", Phase: "starttls", Code: code}
	}
	secure := tls.Client(c.conn, config.Clone())
	if e = secure.HandshakeContext(ctx); e != nil {
		return tlsFailure(e, "starttls")
	}
	c.conn = secure
	c.reader = bufio.NewReader(secure)
	c.ehlo, c.helo, c.esmtp = false, false, false
	c.features = map[string]string{}
	return nil
}
func (c *client) login(user, password string) error {
	if e := c.hello(); e != nil {
		return e
	}
	methods, ok := c.features["auth"]
	if !ok {
		return failure("SMTPNotSupportedError", "auth")
	}
	advertised := strings.Fields(methods)
	found := []string{}
	for _, m := range []string{"CRAM-MD5", "PLAIN", "LOGIN"} {
		for _, v := range advertised {
			if m == v {
				found = append(found, m)
				break
			}
		}
	}
	if len(found) == 0 {
		return failure("SMTPException", "auth")
	}
	var last error
	for _, m := range found {
		e := c.auth(m, user, password)
		if e == nil {
			return nil
		}
		var smtp *Error
		if !errors.As(e, &smtp) || smtp.Class != "SMTPAuthenticationError" {
			return e
		}
		last = e
	}
	return last
}
func (c *client) auth(method, user, password string) error {
	count := 0
	authResponse := func(challenge []byte, initial bool) (string, error) {
		var s string
		switch method {
		case "PLAIN":
			s = "\x00" + user + "\x00" + password
		case "LOGIN":
			if initial || count < 2 {
				s = user
			} else {
				s = password
			}
		case "CRAM-MD5":
			if !ascii(password) {
				return "", failure("UnicodeEncodeError", "auth")
			}
			h := hmac.New(md5.New, []byte(password))
			h.Write(challenge)
			s = user + " " + fmt.Sprintf("%x", h.Sum(nil))
		}
		if !ascii(s) {
			return "", failure("UnicodeEncodeError", "auth")
		}
		return base64.StdEncoding.EncodeToString([]byte(s)), nil
	}
	args := method
	if method != "CRAM-MD5" {
		response, e := authResponse(nil, true)
		if e != nil {
			return e
		}
		args += " " + response
		count = 1
	}
	code, reply, e := c.command("AUTH", args)
	if e != nil {
		return e
	}
	for code == 334 {
		count++
		challenge, e := decodeChallenge(reply)
		if e != nil {
			return e
		}
		response, e := authResponse(challenge, false)
		if e != nil {
			return e
		}
		code, reply, e = c.command(response, "")
		if e != nil {
			return e
		}
		if count > 5 {
			return failure("SMTPException", "auth_challenges")
		}
	}
	if code == 235 || code == 503 {
		return nil
	}
	return &Error{Class: "SMTPAuthenticationError", Phase: "auth", Code: code}
}

// CPython 3.12.13 binascii_a2b_base64_impl(strict_mode=False), including
// ignored bytes, discontinuous/excess padding and trailing bytes after pads.
func decodeChallenge(s string) ([]byte, error) {
	out := make([]byte, 0, (len(s)+3)/4*3)
	position, left, pads := 0, byte(0), 0
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for _, c := range []byte(s) {
		if c == '=' {
			if position >= 2 {
				pads++
				if position+pads >= 4 {
					return out, nil
				}
			}
			continue
		}
		value := strings.IndexByte(alphabet, c)
		if value < 0 {
			continue
		}
		pads = 0
		v := byte(value)
		switch position {
		case 0:
			position = 1
			left = v
		case 1:
			position = 2
			out = append(out, (left<<2)|(v>>4))
			left = v & 15
		case 2:
			position = 3
			out = append(out, (left<<4)|(v>>2))
			left = v & 3
		case 3:
			position = 0
			out = append(out, (left<<6)|v)
			left = 0
		}
	}
	if position != 0 {
		return nil, failure("Error", "auth_base64")
	}
	return out, nil
}
func (c *client) reset() error {
	_, _, e := c.command("rset", "")
	var smtp *Error
	if errors.As(e, &smtp) && smtp.Class == "SMTPServerDisconnected" {
		return nil
	}
	return e
}
func (c *client) sendmail(from string, to []string, raw []byte) error {
	if e := c.hello(); e != nil {
		return e
	}
	wire := fixEOLs(raw)
	if !ascii(string(wire)) {
		return failure("UnicodeEncodeError", "data_encode")
	}
	sender, e := quoteAddr(from)
	if e != nil {
		return e
	}
	args := "FROM:" + sender
	if c.esmtp {
		if _, ok := c.features["size"]; ok {
			args += " size=" + strconv.Itoa(len(wire))
		}
	}
	code, _, e := c.command("mail", args)
	if e != nil {
		return e
	}
	if code != 250 {
		if code == 421 {
			c.close()
		} else if e = c.reset(); e != nil {
			return e
		}
		return &Error{Class: "SMTPSenderRefused", Phase: "mail", Code: code}
	}
	refused := 0
	for _, recipient := range to {
		addr, e := quoteAddr(recipient)
		if e != nil {
			return e
		}
		code, _, e = c.command("rcpt", "TO:"+addr)
		if e != nil {
			return e
		}
		if code != 250 && code != 251 {
			refused++
		}
		if code == 421 {
			c.close()
			return failure("SMTPRecipientsRefused", "rcpt")
		}
	}
	if refused == len(to) {
		if e = c.reset(); e != nil {
			return e
		}
		return failure("SMTPRecipientsRefused", "rcpt")
	}
	code, _, e = c.command("data", "")
	if e != nil {
		return e
	}
	if code != 354 {
		return &Error{Class: "SMTPDataError", Phase: "data_begin", Code: code}
	}
	if e = c.send(dotStuff(wire)); e != nil {
		return e
	}
	code, _, e = c.reply()
	if e != nil {
		return e
	}
	if code != 250 {
		if code == 421 {
			c.close()
		} else if e = c.reset(); e != nil {
			return e
		}
		return &Error{Class: "SMTPDataError", Phase: "data_end", Code: code}
	}
	return nil
}
func fixEOLs(raw []byte) []byte {
	out := make([]byte, 0, len(raw)+128)
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\r':
			out = append(out, '\r', '\n')
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		case '\n':
			out = append(out, '\r', '\n')
		default:
			out = append(out, raw[i])
		}
	}
	return out
}
func dotStuff(raw []byte) []byte {
	out := make([]byte, 0, len(raw)+8)
	start := true
	for _, b := range raw {
		if start && b == '.' {
			out = append(out, '.')
		}
		out = append(out, b)
		start = b == '\n'
	}
	if !bytes.HasSuffix(out, []byte("\r\n")) {
		out = append(out, '\r', '\n')
	}
	return append(out, '.', '\r', '\n')
}

func tlsFailure(err error, phase string) error {
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return failure("SSLCertVerificationError", phase)
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return failure("TimeoutError", phase)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return failure("SSLEOFError", phase)
	}
	return failure("SSLError", phase)
}
func latin1(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		b.WriteRune(rune(c))
	}
	return b.String()
}
