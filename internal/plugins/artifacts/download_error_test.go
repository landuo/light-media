package artifacts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownloadFailureRedactsQueryAndRemoteParserTextPreservingCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		io.WriteString(connection, "HTTP/1.1 owned-private-network-body invalid\r\n\r\n")
	}))
	defer server.Close()
	address := server.URL + "/index?token=owned-secret-query"
	_, err := download(context.Background(), server.Client(), address, 1024)
	if err == nil || strings.Contains(err.Error(), "owned-secret-query") || strings.Contains(err.Error(), "owned-private-network-body") {
		t.Fatal("public artifact failure echoed a request secret or a remote parser body")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := download(ctx, server.Client(), address, 1024); !errors.Is(err, context.Canceled) {
		t.Fatal("redaction changed caller cancellation semantics", err)
	}
}
