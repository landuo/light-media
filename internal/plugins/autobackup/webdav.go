package autobackup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// HTTPDAV is the consumed webdavclient3 3.14.7 protocol surface. That exact
// dependency ignores webdav_digest_auth, so this adapter preserves Basic auth.
type HTTPDAV struct {
	host, login, password string
	disableCheck          bool
	client                *http.Client
	readRoots             []string
}

func NewHTTPDAV(options Object, client *http.Client, readRoots []string) (*HTTPDAV, error) {
	host, ok := options["webdav_hostname"].(string)
	if !ok {
		return nil, source("AttributeError", "hostname.rstrip")
	}
	host = strings.TrimRight(host, "/")
	u, e := url.Parse(host)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("unbound WebDAV origin")
	}
	login, ok := options["webdav_login"].(string)
	if !ok {
		return nil, source("TypeError", "basic login")
	}
	password, ok := options["webdav_password"].(string)
	if !ok {
		return nil, source("TypeError", "basic password")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPDAV{host, login, password, truth(options["disable_check"]), &copyClient, append([]string(nil), readRoots...)}, nil
}
func quotePath(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, v := range []byte(s) {
		if v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || strings.ContainsRune("_.-~/", rune(v)) {
			b.WriteByte(v)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[v>>4])
			b.WriteByte(hex[v&15])
		}
	}
	return b.String()
}

var dots = regexp.MustCompile(`/\.+/`)
var slashes = regexp.MustCompile(`/+`)

func urn(s string, directory bool) string {
	s = dots.ReplaceAllString(quotePath(s), "/")
	s = slashes.ReplaceAllString(s, "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	if directory && !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}
func normalize(s string) string {
	s = slashes.ReplaceAllString(s, "/")
	if strings.HasSuffix(s, "/") {
		s = s[:len(s)-1]
	}
	return s
}
func urnPath(s string) string {
	v, e := url.PathUnescape(s)
	if e != nil {
		return s
	}
	return v
}
func urnFilename(s string) string {
	p := strings.Split(s, "/")
	name := p[len(p)-1]
	if name == "" && len(p) > 1 {
		name = p[len(p)-2] + "/"
	}
	return urnPath(name)
}
func urnParent(s string) string {
	i := strings.LastIndex(strings.TrimSuffix(s, "/"), "/")
	if i <= 0 {
		return "/"
	}
	return urnPath(s[:i+1])
}
func basicPart(v string) (string, error) {
	var b []byte
	for _, r := range v {
		if r > 255 {
			return "", source("UnicodeEncodeError", "basic latin1")
		}
		b = append(b, byte(r))
	}
	return string(b), nil
}
func (d *HTTPDAV) request(ctx context.Context, action, remote string, data io.ReadSeeker) (*http.Response, error) {
	methods := map[string]string{"list": "PROPFIND", "check": "HEAD", "upload": "PUT", "clean": "DELETE"}
	method := methods[action]
	target := d.host + remote
	login, e := basicPart(d.login)
	if e != nil {
		return nil, e
	}
	password, e := basicPart(d.password)
	if e != nil {
		return nil, e
	}
	for redirects := 0; redirects <= 30; redirects++ {
		var body io.Reader
		var length int64
		if data != nil {
			var seekErr error
			length, seekErr = data.Seek(0, io.SeekEnd)
			if seekErr != nil {
				return nil, seekErr
			}
			if _, seekErr = data.Seek(0, io.SeekStart); seekErr != nil {
				return nil, seekErr
			}
			body = io.NopCloser(data)
		}
		req, e := http.NewRequestWithContext(ctx, method, target, body)
		if e == nil && data != nil {
			req.ContentLength = length
		}
		if e != nil {
			return nil, e
		}
		if action != "upload" {
			req.Header.Set("Accept", "*/*")
		}
		if action == "list" {
			req.Header.Set("Depth", "1")
		}
		if action == "clean" {
			req.Header.Set("Connection", "Keep-Alive")
		}
		if login != "" && password != "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(login+":"+password)))
		}
		res, e := d.client.Do(req)
		if e != nil {
			return nil, errors.New("WebDAV connection failed")
		}
		if (res.StatusCode == 301 || res.StatusCode == 302 || res.StatusCode == 303 || res.StatusCode == 307 || res.StatusCode == 308) && res.Header.Get("Location") != "" {
			next, e := req.URL.Parse(res.Header.Get("Location"))
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if e != nil {
				return nil, e
			}
			origin, _ := url.Parse(d.host)
			if next.Scheme != origin.Scheme || next.Host != origin.Host {
				return nil, errors.New("WebDAV redirect outside bound origin")
			}
			if redirects == 30 {
				return nil, errors.New("WebDAV redirect limit")
			}
			if res.StatusCode == 303 && method != "HEAD" || res.StatusCode == 302 && method != "HEAD" || res.StatusCode == 301 && method == "POST" {
				method = "GET"
			}
			if res.StatusCode != 307 && res.StatusCode != 308 {
				data = nil
			}
			target = next.String()
			continue
		}
		if res.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			switch res.StatusCode {
			case 507:
				return nil, errors.New("Not enough space on the server")
			case 404:
				return nil, &davNotFound{remote}
			case 423:
				return nil, fmt.Errorf("Resource %s locked", remote)
			case 405:
				return nil, fmt.Errorf("Method '%s' not supported for %s", action, d.host)
			}
			return nil, fmt.Errorf("Request to %s failed with code %d and message: %s", target, res.StatusCode, pythonBytes(body))
		}
		return res, nil
	}
	return nil, errors.New("WebDAV redirect limit")
}

type davNotFound struct{ path string }

func (e *davNotFound) Error() string { return "Remote resource: " + e.path + " not found" }
func pythonBytes(b []byte) string {
	quote := byte('\'')
	if bytes.ContainsRune(b, '\'') && !bytes.ContainsRune(b, '"') {
		quote = '"'
	}
	var s strings.Builder
	s.WriteByte('b')
	s.WriteByte(quote)
	for _, c := range b {
		switch c {
		case '\n':
			s.WriteString(`\n`)
		case '\r':
			s.WriteString(`\r`)
		case '\t':
			s.WriteString(`\t`)
		case '\\':
			s.WriteString(`\\`)
		default:
			if c == quote {
				s.WriteByte('\\')
				s.WriteByte(c)
			} else if c >= 32 && c < 127 {
				s.WriteByte(c)
			} else {
				s.WriteString(fmt.Sprintf(`\x%02x`, c))
			}
		}
	}
	s.WriteByte(quote)
	return s.String()
}
func (d *HTTPDAV) Check(ctx context.Context, remote string) (bool, error) {
	if d.disableCheck {
		return true, nil
	}
	r, e := d.request(ctx, "check", urn(remote, false), nil)
	if e != nil {
		var n *davNotFound
		if errors.As(e, &n) {
			return false, nil
		}
		return false, e
	}
	defer r.Body.Close()
	return r.StatusCode == 200, nil
}

// DAV response traversal matches .//{DAV:}response, first descendant DAV:href,
// and descendant DAV:collection. A malformed XML document is an empty listing.
type xmlNode struct {
	Name     xml.Name
	Text     string
	Children []*xmlNode
}

func parseXML(raw []byte) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var stack []*xmlNode
	var root *xmlNode
	for {
		token, e := dec.Token()
		if e == io.EOF {
			if len(stack) != 0 {
				return nil, errors.New("unclosed XML")
			}
			return root, nil
		}
		if e != nil {
			return nil, e
		}
		switch v := token.(type) {
		case xml.StartElement:
			n := &xmlNode{Name: v.Name}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			} else if root != nil {
				return nil, errors.New("multiple XML roots")
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(v)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
}
func descendants(n *xmlNode, local string) []*xmlNode {
	var found []*xmlNode
	for _, c := range n.Children {
		if c.Name.Space == "DAV:" && c.Name.Local == local {
			found = append(found, c)
		}
		found = append(found, descendants(c, local)...)
	}
	return found
}
func (d *HTTPDAV) List(ctx context.Context, remote string) ([]string, error) {
	directory := urn(remote, true)
	if urnPath(directory) != "/" {
		exists, e := d.Check(ctx, urnPath(directory))
		if e != nil {
			return nil, e
		}
		if !exists {
			return nil, &davNotFound{urnPath(directory)}
		}
	}
	r, e := d.request(ctx, "list", directory, nil)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		return nil, e
	}
	root, e := parseXML(raw)
	if e != nil || root == nil {
		return []string{}, nil
	}
	result := []string{}
	for _, response := range descendants(root, "response") {
		hrefs := descendants(response, "href")
		if len(hrefs) == 0 {
			continue
		}
		u, e := url.Parse(hrefs[0].Text)
		if e != nil {
			return nil, e
		}
		href := "/" + urnPath(u.EscapedPath())
		value := urn(href, len(descendants(response, "collection")) > 0)
		parsed := normalize("/" + urnPath(value))
		if normalize(urnPath(directory)) == parsed {
			continue
		}
		result = append(result, urnFilename(value))
	}
	return result, nil
}
func (d *HTTPDAV) Upload(ctx context.Context, remote, local string, callback func() error) error {
	if e := authorize(local, d.readRoots); e != nil {
		return e
	}
	st, e := os.Stat(local)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return fmt.Errorf("Local file: %s not found", local)
		}
		return e
	}
	u := urn(remote, false)
	if strings.HasSuffix(u, "/") {
		return fmt.Errorf("Option (remote_path=%s) have invalid name or value", remote)
	}
	if st.IsDir() {
		return fmt.Errorf("Option (local_path=%s) have invalid name or value", local)
	}
	exists, e := d.Check(ctx, urnParent(u))
	if e != nil {
		return e
	}
	if !exists {
		return fmt.Errorf("Remote parent for: %s not found", urnPath(u))
	}
	content, e := os.Open(filepath.Clean(local))
	if e != nil {
		return e
	}
	defer content.Close()
	r, e := d.request(ctx, "upload", u, content)
	if e != nil {
		return e
	}
	r.Body.Close()
	if callback != nil {
		return callback()
	}
	return nil
}
func (d *HTTPDAV) Delete(ctx context.Context, remote string) error {
	r, e := d.request(ctx, "clean", urn(remote, false), nil)
	if e != nil {
		return e
	}
	r.Body.Close()
	return nil
}
