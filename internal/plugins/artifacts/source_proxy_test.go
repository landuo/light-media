package artifacts

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSourceProxyUsesTrustedTransportForModulesAndChecksums(t *testing.T) {
	const module = "/example.org/some/!module/@v/v1.2.3"
	const checksumTile = "/tile/8/0/001"
	transport := repositoryReplayTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.URL.Scheme != "https" {
			t.Errorf("request copied caller auth or used an unexpected method or scheme")
		}
		if req.URL.Host == "storage.googleapis.com" && req.URL.Path == "/archive" {
			return sourceProxyReply(http.StatusOK, "module archive bytes"), nil
		}
		if req.URL.Host == goChecksumHost && req.URL.Path == checksumTile {
			return sourceProxyReply(http.StatusOK, "signed checksum tile response"), nil
		}
		if req.URL.Host != goModuleProxyHost || req.URL.RawQuery != "" {
			t.Errorf("unexpected module proxy upstream: %s", req.URL)
			return sourceProxyReply(http.StatusBadRequest, ""), nil
		}
		switch req.URL.Path {
		case module + ".mod":
			return sourceProxyReply(http.StatusOK, "module example.org/some/Module"), nil
		case module + ".zip":
			response := sourceProxyReply(http.StatusFound, "")
			response.Header.Set("Location", "https://storage.googleapis.com/archive")
			return response, nil
		case module + ".info":
			response := sourceProxyReply(http.StatusFound, "")
			response.Header.Set("Location", "http://localhost:8080/untrusted")
			return response, nil
		default:
			t.Errorf("unexpected upstream path %q", req.URL.Path)
			return sourceProxyReply(http.StatusBadRequest, ""), nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address, closeProxy, err := startSourceProxy(ctx, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{module + ".mod", http.StatusOK, "module example.org/some/Module"},
		{module + ".zip", http.StatusOK, "module archive bytes"},
		{module + ".info", http.StatusBadGateway, ""},
		{goChecksumRoute + strings.TrimPrefix(checksumTile, "/"), http.StatusOK, "signed checksum tile response"},
		{goChecksumRoute + "supported", http.StatusOK, ""},
		{"/sumdb/foreign.invalid/supported", http.StatusNotFound, ""},
		{module + ".mod?escaped=1", http.StatusNotFound, ""},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "not-for-forwarding")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != tc.status || (tc.body != "" && string(data) != tc.body) {
			t.Fatalf("relay response for %s: status %d, body %q, error %v", tc.path, response.StatusCode, data, err)
		}
	}
}

func sourceProxyReply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}
