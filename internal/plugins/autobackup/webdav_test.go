package autobackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func davErrorType(e error) any {
	if e == nil {
		return nil
	}
	var s *SourceError
	if errors.As(e, &s) {
		return s.Type
	}
	var n *davNotFound
	if errors.As(e, &n) {
		return "RemoteResourceNotFound"
	}
	m := e.Error()
	switch {
	case m == "Not enough space on the server":
		return "NotEnoughSpace"
	case strings.HasPrefix(m, "Resource "):
		return "ResourceLocked"
	case strings.HasPrefix(m, "Method '"):
		return "MethodNotSupported"
	case strings.HasPrefix(m, "Request to "):
		return "ResponseErrorCode"
	case strings.HasPrefix(m, "Remote parent for: "):
		return "RemoteParentNotFound"
	}
	return "RuntimeError"
}

const defaultXML = `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response><d:response><d:href>/bk_20260101000000.zip</d:href></d:response><d:response><d:href>/folder/</d:href><d:collection/></d:response><d:response><d:href>https://dav.invalid/root/a%20b.zip</d:href></d:response></d:multistatus>`

func TestInstalledWebDAVDependencyOracle(t *testing.T) {
	raw, e := os.ReadFile(originalFixture("reference/webdav-oracle.json"))
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Input     Object `json:"input"`
			Options   Object `json:"options"`
			Operation string `json:"operation"`
			Trace     []any  `json:"trace"`
			Result    any    `json:"result"`
			Error     any    `json:"error"`
			Message   any    `json:"message"`
			Callback  []any  `json:"callback"`
		} `json:"cases"`
	}
	decode(t, raw, &fixture)
	if len(fixture.Cases) != 55 {
		t.Fatal(len(fixture.Cases))
	}
	for i, c := range fixture.Cases {
		t.Run(c.Operation+"/"+text(i), func(t *testing.T) {
			trace := []any{}
			callback := []any{}
			client := &http.Client{Transport: rtFunc(func(req *http.Request) (*http.Response, error) {
				var body any
				if req.Body != nil {
					b, _ := io.ReadAll(req.Body)
					if len(b) > 0 {
						body = string(b)
					}
				}
				headers := Object{}
				for k, v := range req.Header {
					if k != "Authorization" {
						headers[k] = v[0]
					}
				}
				trace = append(trace, Object{"method": req.Method, "url": req.URL.String(), "auth": []any{"owned", "synthetic"}, "headers": headers, "body": body, "timeout": 30, "verify": true})
				status := map[string]int{"PROPFIND": 207, "HEAD": 200, "PUT": 201, "DELETE": 204}[req.Method]
				if statuses, ok := c.Input["status"].(map[string]any); ok {
					if value, ok := statuses[req.Method].(json.Number); ok {
						n, _ := value.Int64()
						status = int(n)
					}
				}
				response := "owned response"
				if req.Method == "PROPFIND" {
					response = defaultXML
					if v, ok := c.Input["xml"].(string); ok {
						response = v
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
			})}
			dir := t.TempDir()
			file := filepath.Join(dir, "owned.zip")
			if e = os.WriteFile(file, []byte("owned zip bytes"), 0600); e != nil {
				t.Fatal(e)
			}
			dav, e := NewHTTPDAV(c.Options, client, []string{dir})
			if e != nil {
				t.Fatal(e)
			}
			var result any
			switch c.Operation {
			case "list":
				result, e = dav.List(context.Background(), "/")
			case "check":
				result, e = dav.Check(context.Background(), "/owned.zip")
			case "check_url":
				result, e = dav.Check(context.Background(), "https://dav.invalid/root/owned.zip")
			case "upload":
				e = dav.Upload(context.Background(), "owned.zip", file, func() error { callback = append(callback, "called"); return nil })
			case "delete":
				e = dav.Delete(context.Background(), "/owned.zip")
			}
			if e != nil {
				result = nil
			}
			if canonical(davErrorType(e)) != canonical(c.Error) {
				t.Fatalf("error type got%v/%v want%v", e, davErrorType(e), c.Error)
			}
			if e != nil && canonical(e.Error()) != canonical(c.Message) {
				t.Errorf("error message got%s want%s", canonical(e.Error()), canonical(c.Message))
			}
			if canonical(result) != canonical(c.Result) || canonical(trace) != canonical(c.Trace) || canonical(callback) != canonical(c.Callback) {
				t.Errorf("result %s/%s\ntrace %s\nwant %s\ncallback %s/%s", canonical(result), canonical(c.Result), canonical(trace), canonical(c.Trace), canonical(callback), canonical(c.Callback))
			}
		})
	}
}
func TestNativeWebDAVTLSUploadOriginalCallbackAndDigestFlag(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bk_20261018090203.zip")
	content := strings.Repeat("owned", 1<<16)
	if e := os.WriteFile(file, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var methods, paths []string
	var authOK bool
	var received string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.EscapedPath())
		user, password, ok := r.BasicAuth()
		authOK = ok && user == "owned" && password == "synthetic"
		switch r.Method {
		case "PROPFIND":
			w.WriteHeader(207)
			_, _ = io.WriteString(w, defaultXML)
		case "HEAD":
			if strings.Contains(r.URL.EscapedPath(), "https%3A") {
				w.WriteHeader(404)
			} else {
				w.WriteHeader(200)
			}
		case "PUT":
			b, _ := io.ReadAll(r.Body)
			digest := sha256.Sum256(b)
			received = hex.EncodeToString(digest[:])
			w.WriteHeader(201)
		case "DELETE":
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	dav, e := NewHTTPDAV(Object{"webdav_hostname": srv.URL + "/root", "webdav_login": "owned", "webdav_password": "synthetic", "webdav_digest_auth": true}, srv.Client(), []string{dir})
	if e != nil {
		t.Fatal(e)
	}
	p := New(Dependencies{DataPath: func() string { return dir }, BackupFile: func(context.Context, string) (*string, error) { return &file, nil }, NewDAV: func(context.Context, Object) (DAV, error) { return dav, nil }})
	if e = p.Init(context.Background(), Object{"webdav_enabled": true, "webdav_hostname": srv.URL + "/root", "webdav_login": "owned", "webdav_password": "synthetic"}); e != nil {
		t.Fatal(e)
	}
	r, e := p.Backup(context.Background())
	mu.Lock()
	defer mu.Unlock()
	digest := sha256.Sum256([]byte(content))
	if e != nil || !r.Success || !strings.Contains(r.Message, "WebDAV备份上传完成但验证失败") || received != hex.EncodeToString(digest[:]) || !authOK {
		t.Fatal(r, e, authOK, received)
	}
	if canonical(methods) != canonical([]string{"PROPFIND", "HEAD", "PUT", "HEAD"}) || !strings.Contains(paths[3], "https%3A/") {
		t.Fatal(methods, paths)
	}
}
