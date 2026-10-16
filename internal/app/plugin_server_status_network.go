package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"

	"light-media/internal/plugins/serverstatus"
)

const pluginServerStatusBodyLimit = 1 << 20

var errPluginServerStatusBodyLimit = errors.New("服务状态响应超过1 MiB限制")
var errPluginServerStatusEncoding = errors.New("服务状态响应未声明可识别的字符集")

// This adapter is exclusively for the installed MPServerStatus public endpoint.
// Its HTTP certificate policy reproduces RequestUtils, never login/credential
// clients or the independent TLS probe. All seams below are Go-private fixtures;
// no worker, config object or request body can provide them.
type pluginServerStatusNetwork struct {
	testOrigin     string
	testTimeout    time.Duration
	testProxyURL   *url.URL
	testProxyRoots *x509.CertPool
	testTLSRoots   *x509.CertPool
	testResolve    func(context.Context, string) ([]net.IPAddr, error)
}

func (n pluginServerStatusNetwork) origin() (*url.URL, error) {
	target := serverstatus.ServerURL
	if n.testOrigin != "" {
		fixture, err := url.Parse(n.testOrigin)
		if err != nil || fixture.User != nil || (fixture.Scheme != "http" && fixture.Scheme != "https") || fixture.RawQuery != "" || fixture.Fragment != "" || net.ParseIP(fixture.Hostname()) == nil || !net.ParseIP(fixture.Hostname()).IsLoopback() {
			return nil, errors.New("invalid status fixture origin")
		}
		target = n.testOrigin
	}
	return url.Parse(target)
}
func pluginServerStatusSameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a != nil && b != nil && a.User == nil && b.User == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

type pluginServerStatusIdleConn struct {
	net.Conn
	idle time.Duration
}

func (c *pluginServerStatusIdleConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *pluginServerStatusIdleConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func (n pluginServerStatusNetwork) dial(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &pluginServerStatusIdleConn{Conn: conn, idle: timeout}, nil
}
func (n pluginServerStatusNetwork) dialTLS(ctx context.Context, network, address, serverName string, timeout time.Duration, config *tls.Config) (net.Conn, error) {
	conn, err := n.dial(ctx, network, address, timeout)
	if err != nil {
		return nil, err
	}
	config = config.Clone()
	config.ServerName = serverName
	tlsConn := tls.Client(conn, config)
	handshake, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err = tlsConn.HandshakeContext(handshake); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (n pluginServerStatusNetwork) Status(ctx context.Context, target string, timeout time.Duration) (*serverstatus.Response, error) {
	if target != serverstatus.ServerURL || (timeout != 20*time.Second && timeout != 0) {
		return nil, errors.New("服务状态请求超出固定范围")
	}
	origin, err := n.origin()
	if err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if n.testOrigin != "" && n.testTimeout > 0 {
		timeout = n.testTimeout
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.String(), nil)
	if err != nil {
		return nil, nil
	}
	proxy, err := http.ProxyFromEnvironment(request)
	if n.testOrigin != "" && n.testProxyURL != nil {
		proxy = n.testProxyURL
		err = nil
	}
	if err != nil {
		return nil, nil
	}
	// Dedicated transport, closed after this single requests.Session-equivalent
	// call. Redirects share it, later samples do not reuse its connections.
	// Source verify=False is limited to the authorized origin. HTTPS proxy TLS
	// is separately handshaken below with strict verification, even with auth.
	originTLS := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}} // Fixed public no-credential source behavior.
	transport := &http.Transport{
		Proxy:                  func(*http.Request) (*url.URL, error) { return proxy, nil },
		TLSClientConfig:        originTLS,
		TLSHandshakeTimeout:    timeout,
		DisableCompression:     true,
		ForceAttemptHTTP2:      false,
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:           1,
		MaxIdleConnsPerHost:    1,
		MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return n.dial(ctx, network, address, timeout)
		},
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if proxy != nil && proxy.Scheme == "https" {
			strict := &tls.Config{NextProtos: []string{"http/1.1"}}
			if n.testOrigin != "" {
				strict.RootCAs = n.testProxyRoots
			}
			return n.dialTLS(ctx, network, address, proxy.Hostname(), timeout, strict)
		}
		return n.dialTLS(ctx, network, address, origin.Hostname(), timeout, originTLS)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 30 {
			return errors.New("status redirect limit")
		}
		if !pluginServerStatusSameOrigin(origin, next.URL) {
			return errors.New("status redirect outside authorized origin")
		}
		next.Header.Del("Authorization")
		next.Header.Del("Cookie")
		next.Header.Del("Referer")
		if next.Response != nil && next.Response.StatusCode != http.StatusTemporaryRedirect && next.Response.StatusCode != http.StatusPermanentRedirect {
			next.Header.Del("Content-Type")
			next.Header.Del("Content-Length")
			next.Header.Del("Transfer-Encoding")
		}
		return nil
	}}
	request.Header.Set("User-Agent", "python-urllib3/2.7.0")
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	response, err := client.Do(request)
	if err != nil {
		return nil, nil
	} // RequestException -> None -> source 网络错误.
	defer response.Body.Close()
	body, err := pluginServerStatusReadBounded(response.Body)
	if err != nil {
		if errors.Is(err, errPluginServerStatusBodyLimit) {
			return nil, err
		}
		return nil, nil
	}
	body, err = pluginServerStatusDecompress(body, response.Header.Get("Content-Encoding"))
	if err != nil {
		if errors.Is(err, errPluginServerStatusBodyLimit) {
			return nil, err
		}
		return nil, nil
	}
	text, err := pluginServerStatusText(body, response.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for key, values := range response.Header {
		headers[key] = strings.Join(values, ", ")
	}
	return &serverstatus.Response{Status: response.StatusCode, Text: text, Content: body, Headers: headers, Protocol: response.ProtoMajor*10 + response.ProtoMinor}, nil
}

func pluginServerStatusReadBounded(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, pluginServerStatusBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > pluginServerStatusBodyLimit {
		return nil, errPluginServerStatusBodyLimit
	}
	return body, nil
}
func pluginServerStatusDecompress(body []byte, contentEncoding string) ([]byte, error) {
	names := strings.Split(strings.ToLower(contentEncoding), ",")
	for i := len(names) - 1; i >= 0; i-- {
		var reader io.Reader
		var closeReader func() error
		switch strings.TrimSpace(names[i]) {
		case "gzip", "x-gzip":
			decoder, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			reader = decoder
			closeReader = decoder.Close
		case "deflate":
			decoder, err := zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				reader = flate.NewReader(bytes.NewReader(body))
				closeReader = reader.(io.Closer).Close
			} else {
				reader = decoder
				closeReader = decoder.Close
			}
		case "br":
			reader = brotli.NewReader(bytes.NewReader(body))
		case "zstd":
			decoder, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(8<<20), zstd.WithDecoderMaxWindow(8<<20))
			if err != nil {
				return nil, err
			}
			reader = decoder
			closeReader = func() error { decoder.Close(); return nil }
		default:
			continue // urllib3 leaves unknown encodings untouched.
		}
		decoded, err := pluginServerStatusReadBounded(reader)
		if closeReader != nil {
			closeReader()
		}
		if err != nil {
			return nil, err
		}
		body = decoded
	}
	return body, nil
}

// Python's errors=replace consumes a valid but incomplete UTF8 prefix as one
// replacement, and separate invalid leading bytes separately. ToValidUTF8's
// collapse of consecutive invalid bytes does not match Response.text.
func pluginServerStatusUTF8(body []byte) string {
	var b strings.Builder
	for i := 0; i < len(body); {
		r, width := utf8.DecodeRune(body[i:])
		if r != utf8.RuneError || width > 1 {
			b.WriteRune(r)
			i += width
			continue
		}
		count := 1
		lead := body[i]
		length := 0
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			length = 2
		case lead >= 0xe0 && lead <= 0xef:
			length = 3
		case lead >= 0xf0 && lead <= 0xf4:
			length = 4
		}
		for count < length && i+count < len(body) {
			next := body[i+count]
			lo, hi := byte(0x80), byte(0xbf)
			if count == 1 {
				switch lead {
				case 0xe0:
					lo = 0xa0
				case 0xed:
					hi = 0x9f
				case 0xf0:
					lo = 0x90
				case 0xf4:
					hi = 0x8f
				}
			}
			if next < lo || next > hi {
				break
			}
			count++
		}
		b.WriteRune(utf8.RuneError)
		i += count
	}
	return b.String()
}
func pluginServerStatusDecode(body []byte, label string) string {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(label))
	var codec encoding.Encoding
	switch normalized {
	case "utf8", "utf":
		return pluginServerStatusUTF8(body)
	case "utf8sig":
		return pluginServerStatusUTF8(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))
	case "latin1", "latin", "l1", "iso88591", "cp819", "ibm819":
		codec = charmap.ISO8859_1
	case "ascii", "usascii", "646", "ansix341968", "iso646us", "cp367":
		var b strings.Builder
		for _, c := range body {
			if c < 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteRune(utf8.RuneError)
			}
		}
		return b.String()
	case "utf16":
		codec = textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM)
	case "utf16le":
		codec = textunicode.UTF16(textunicode.LittleEndian, textunicode.IgnoreBOM)
	case "utf16be":
		codec = textunicode.UTF16(textunicode.BigEndian, textunicode.IgnoreBOM)
	case "utf32":
		codec = utf32.UTF32(utf32.LittleEndian, utf32.UseBOM)
	case "utf32le":
		codec = utf32.UTF32(utf32.LittleEndian, utf32.IgnoreBOM)
	case "utf32be":
		codec = utf32.UTF32(utf32.BigEndian, utf32.IgnoreBOM)
	default:
		codec, _ = htmlindex.Get(label)
	}
	if codec == nil {
		return pluginServerStatusUTF8(body)
	} // Response.text unknown codec fallback.
	decoded, err := codec.NewDecoder().Bytes(body)
	if err != nil {
		return pluginServerStatusUTF8(body)
	}
	return string(decoded)
}
func pluginServerStatusText(body []byte, contentType string) (string, error) {
	if contentType != "" {
		_, params, err := mime.ParseMediaType(contentType)
		if err == nil {
			if charset, ok := params["charset"]; ok {
				return pluginServerStatusDecode(body, charset), nil
			}
		}
		// Match RequestUtils dependency's actual case-sensitive substring rules.
		if strings.Contains(contentType, "text") {
			return pluginServerStatusDecode(body, "ISO-8859-1"), nil
		}
		if strings.Contains(contentType, "application/json") {
			return pluginServerStatusDecode(body, "utf-8"), nil
		}
	}
	switch {
	case bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}):
		return pluginServerStatusDecode(body, "utf-8-sig"), nil
	case bytes.HasPrefix(body, []byte{0xff, 0xfe, 0, 0}) || bytes.HasPrefix(body, []byte{0, 0, 0xfe, 0xff}):
		return pluginServerStatusDecode(body, "utf-32"), nil
	case bytes.HasPrefix(body, []byte{0xff, 0xfe}) || bytes.HasPrefix(body, []byte{0xfe, 0xff}):
		return pluginServerStatusDecode(body, "utf-16"), nil
	case utf8.Valid(body):
		return string(body), nil
	default:
		return "", errPluginServerStatusEncoding // Explicit gap: not a fake chardet clone.
	}
}

func (n pluginServerStatusNetwork) DNS(ctx context.Context, host string, port int) ([]string, error) {
	if host != "movie-pilot.org" || port != 443 {
		return nil, errors.New("服务状态DNS探测超出固定范围")
	}
	resolve := net.DefaultResolver.LookupIPAddr
	if n.testResolve != nil {
		resolve = n.testResolve
	}
	ips, err := resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, ip := range ips {
		out = append(out, ip.IP.String())
	}
	return out, nil // Machine.DNS preserves the source's stable first-occurrence dedup.
}
func (n pluginServerStatusNetwork) TLS(ctx context.Context, host string, port int, timeout time.Duration) (serverstatus.Certificate, error) {
	if host != "movie-pilot.org" || port != 443 || timeout != 5*time.Second {
		return serverstatus.Certificate{}, errors.New("服务状态TLS探测超出固定范围")
	}
	origin, err := n.origin()
	if err != nil {
		return serverstatus.Certificate{}, err
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	name := host
	strict := &tls.Config{}
	if n.testOrigin != "" {
		name = origin.Hostname()
		fixturePort := origin.Port()
		if fixturePort == "" {
			fixturePort = "443"
		}
		address = net.JoinHostPort(name, fixturePort)
		strict.RootCAs = n.testTLSRoots
		if n.testTimeout > 0 {
			timeout = n.testTimeout
		}
	}
	conn, err := n.dialTLS(ctx, "tcp", address, name, timeout, strict)
	if err != nil {
		return serverstatus.Certificate{}, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return serverstatus.Certificate{}, errors.New("TLS服务没有提供证书")
	}
	leaf := state.PeerCertificates[0]
	version := map[uint16]string{tls.VersionTLS10: "TLSv1", tls.VersionTLS11: "TLSv1.1", tls.VersionTLS12: "TLSv1.2", tls.VersionTLS13: "TLSv1.3"}[state.Version]
	return serverstatus.Certificate{Version: version, Issuer: pluginServerStatusName(leaf.RawIssuer), Subject: pluginServerStatusName(leaf.RawSubject), NotAfter: leaf.NotAfter.UTC().Format("Jan _2 15:04:05 2006 GMT")}, nil
}
func pluginServerStatusName(raw []byte) [][][2]string {
	var sequence pkix.RDNSequence
	if _, err := asn1.Unmarshal(raw, &sequence); err != nil {
		return nil
	}
	names := map[string]string{"2.5.4.3": "commonName", "2.5.4.6": "countryName", "2.5.4.7": "localityName", "2.5.4.8": "stateOrProvinceName", "2.5.4.10": "organizationName", "2.5.4.11": "organizationalUnitName", "2.5.4.5": "serialNumber", "1.2.840.113549.1.9.1": "emailAddress"}
	out := [][][2]string{}
	for _, set := range sequence {
		group := [][2]string{}
		for _, attribute := range set {
			key := names[attribute.Type.String()]
			if key == "" {
				key = attribute.Type.String()
			}
			group = append(group, [2]string{key, fmt.Sprint(attribute.Value)})
		}
		out = append(out, group)
	}
	return out
}
