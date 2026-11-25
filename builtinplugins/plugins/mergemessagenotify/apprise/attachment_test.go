package apprise

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type attachmentWire struct {
	Method, URL string
	Headers     map[string]string
	Body        any
	Verify      bool
	Timeout     [2]float64
	Stream      bool
}

func TestSourceAttachments199(t *testing.T) {
	testSourceAttachments(t, "testdata/attachment-1.9.9.json")
}

func TestSourceAttachmentReview199(t *testing.T) {
	testSourceAttachments(t, "testdata/attachment-review-1.9.9.json")
}

func testSourceAttachments(t *testing.T, fixturePath string) {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Owned     string `json:"owned_file_base64"`
		HTTPImage string `json:"fixture_http_body_base64"`
		Boundary  string `json:"multipart_boundary"`
		Cases     []struct {
			Name, Title string
			Body        json.RawMessage
			URLs        []string
			Attachment  any
			Added       []bool
			Result      bool
			Requests    []attachmentWire
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	owned, err := base64.StdEncoding.DecodeString(fixture.Owned)
	if err != nil {
		t.Fatal(err)
	}
	image, err := base64.StdEncoding.DecodeString(fixture.HTTPImage)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const filename = "Local Artwork.PNG"
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, owned, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	localURL := (&url.URL{Scheme: "file", Path: path}).String()
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			var calls []attachmentWire
			var wireMu sync.Mutex
			var downloads atomic.Int32
			c := New(func(_ context.Context, request Request) (Response, error) {
				wire, err := prepareAttachmentWire(request, fixture.Boundary)
				if err != nil {
					t.Error(err)
					return Response{}, err
				}
				wireMu.Lock()
				calls = append(calls, wire)
				wireMu.Unlock()
				if request.Method != "GET" {
					return Response{Status: 200}, nil
				}
				if downloads.Add(1) == 1 && row.Name == "split-transient-get-retry" {
					return Response{Status: 503}, nil
				}
				target, err := url.Parse(request.URL)
				if err != nil {
					return Response{}, err
				}
				switch target.Path {
				case "/unavailable.jpg":
					return Response{Status: 404}, nil
				case "/size.jpg":
					return Response{Status: 200, Headers: map[string]string{"Content-Length": "1048576001"}}, nil
				case "/x/remote":
					return Response{Status: 200, Headers: map[string]string{"Content-Type": "application/x-ignored"}, Content: image}, nil
				default:
					return Response{Status: 200, Headers: map[string]string{
						"Content-Type": "image/png", "Content-Disposition": `inline; filename="owned image.png"`,
					}, Content: image}, nil
				}
			})
			c.ReadLocalAttachment = func(_ context.Context, name string) ([]byte, error) {
				if name != path {
					return nil, os.ErrPermission
				}
				return root.ReadFile(filename)
			}
			for i, service := range row.URLs {
				added, err := c.Add(service)
				if err != nil || added != row.Added[i] {
					t.Fatalf("Add() %v %v, want %v", added, err, row.Added[i])
				}
			}
			var body string
			if json.Unmarshal(row.Body, &body) != nil {
				var compact struct {
					Repeat, Suffix string
					Count          int
				}
				if err := json.Unmarshal(row.Body, &compact); err != nil {
					t.Fatal(err)
				}
				body = strings.Repeat(compact.Repeat, compact.Count) + compact.Suffix
			}
			got, err := c.Notify(t.Context(), row.Title, body, substituteAttachment(row.Attachment, localURL))
			if err != nil || got != row.Result {
				t.Fatalf("Notify() %v %v, want %v", got, err, row.Result)
			}
			if len(calls) != len(row.Requests) {
				t.Fatalf("requests = %d, want %d: %v", len(calls), len(row.Requests), calls)
			}
			if row.Name == "split-transient-get-retry" && (calls[0].Method != "GET" || calls[1].Method != "GET" || calls[2].Method != "POST") {
				t.Fatal("split attachment must retry before the surviving upload")
			}
			for _, expected := range row.Requests {
				if body, ok := expected.Body.(map[string]any); ok {
					delete(body, "raw_base64")
				}
				match := -1
				for i := range calls {
					if reflect.DeepEqual(calls[i], expected) {
						match = i
						break
					}
				}
				if match < 0 {
					t.Fatalf("missing original request %#v; remaining %#v", expected, calls)
				}
				calls = append(calls[:match], calls[match+1:]...)
			}
		})
	}
}

func substituteAttachment(value any, local string) any {
	switch value := value.(type) {
	case string:
		return strings.ReplaceAll(value, "{owned_file}", local)
	case []any:
		for i, entry := range value {
			value[i] = substituteAttachment(entry, local)
		}
	}
	return value
}

func prepareAttachmentWire(request Request, fixedBoundary string) (attachmentWire, error) {
	target, err := url.Parse(request.URL)
	if err != nil {
		return attachmentWire{}, err
	}
	for _, pair := range request.ParamOrder {
		if target.RawQuery != "" {
			target.RawQuery += "&"
		}
		target.RawQuery += url.QueryEscape(pair[0]) + "=" + url.QueryEscape(pair[1])
	}
	headers := make(map[string]string, len(request.Headers))
	for key, value := range request.Headers {
		if key == "X-Apprise-ID" {
			value = "{uuid4}"
		}
		headers[key] = value
	}
	if len(request.Auth) == 2 {
		header, err := httpBasicAuth(request.Auth)
		if err != nil {
			return attachmentWire{}, err
		}
		headers["Authorization"] = header
	}
	wire := attachmentWire{Method: request.Method, URL: target.String(), Headers: headers,
		Verify: request.Verify, Timeout: request.Timeout, Stream: request.MaxResponseBytes > 0}
	if request.Data == nil {
		return wire, nil
	}
	wire.Body = string(request.Data)
	contentType, params, err := mime.ParseMediaType(headers["Content-Type"])
	if err != nil || contentType != "multipart/form-data" {
		return wire, nil
	}
	parts, err := readAttachmentParts(request.Data, params["boundary"])
	if err != nil {
		return attachmentWire{}, err
	}
	wire.Body = map[string]any{"multipart": parts}
	wire.Headers["Content-Type"] = "multipart/form-data; boundary=" + fixedBoundary
	return wire, nil
}

func readAttachmentParts(data []byte, boundary string) ([]any, error) {
	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	parts := []any{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		parts = append(parts, map[string]any{
			"field": part.FormName(), "filename": optionalWireValue(part.FileName()),
			"content_type": optionalWireValue(part.Header.Get("Content-Type")),
			"base64":       base64.StdEncoding.EncodeToString(content),
		})
	}
}

func optionalWireValue(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestAttachmentHTTPTransport(t *testing.T) {
	raw, err := os.ReadFile("testdata/attachment-1.9.9.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		HTTPImage string `json:"fixture_http_body_base64"`
		Cases     []struct {
			Name, Title, Body string
			Requests          []attachmentWire
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	original := fixture.Cases[0]
	if original.Name != "json-http" || len(original.Requests) != 2 {
		t.Fatal("missing original wire observation")
	}
	image, err := base64.StdEncoding.DecodeString(fixture.HTTPImage)
	if err != nil {
		t.Fatal(err)
	}
	var downloads, uploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/art.png":
			downloads.Add(1)
			if r.URL.RawQuery != "source=owned" || r.Header.Get("User-Agent") != "Apprise" {
				t.Error("attachment download arguments differ from source")
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Disposition", `inline; filename="owned image.png"`)
			_, _ = w.Write(image)
		case r.Method == http.MethodGet && r.URL.Path == "/oversize.png":
			downloads.Add(1)
			w.Header().Set("Content-Length", "1048576001")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/collect":
			uploads.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != original.Requests[1].Body || r.Header.Get("Content-Type") != "application/json" {
				t.Error("attachment upload differs from source")
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Error("unexpected owned attachment traffic")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	transport := HTTPTransport{Environment: func() []string { return nil }}
	client := New(transport.Send)
	added, err := client.Add("json" + strings.TrimPrefix(server.URL, "http") + "/collect")
	if err != nil || !added {
		t.Fatal("owned recipient was not accepted", err)
	}
	got, err := client.Notify(t.Context(), original.Title, original.Body, server.URL+"/art.png?source=owned")
	if err != nil || got != true {
		t.Fatalf("actual attachment send = %v, %v", got, err)
	}
	got, err = client.Notify(t.Context(), original.Title, original.Body, server.URL+"/oversize.png")
	if err != nil || got != false || downloads.Load() != 2 || uploads.Load() != 1 {
		t.Fatalf("oversize attachment result = %v, %v, calls = %d/%d", got, err, downloads.Load(), uploads.Load())
	}
}

func TestAttachmentHTTPRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/image")
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusFound)
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	transport := HTTPTransport{Environment: func() []string { return nil }}
	got, err := transport.Send(t.Context(), Request{Method: http.MethodGet, URL: server.URL + "/redirect", Verify: true, Timeout: [2]float64{4, 4}, MaxResponseBytes: 8})
	if err != nil || got.Status != http.StatusOK || string(got.Content) != "image" {
		t.Fatalf("final attachment response = %#v, %v", got, err)
	}
}
