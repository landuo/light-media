package sites

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchTimeoutAndCancellationRemainClassifiableWithoutLeakingRequest(t *testing.T) {
	for _, bodyStarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[bodyStarted], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bodyStarted {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client, err := New(Config{ID: "fixture", Kind: "ubits", BaseURL: server.URL, Cookie: "synthetic-private-cookie"})
			if err != nil {
				t.Fatal(err)
			}
			client.http.Timeout = 30 * time.Millisecond
			_, err = client.Search(context.Background(), "synthetic-private-query", 1)
			if !errors.Is(err, context.DeadlineExceeded) || err.Error() != context.DeadlineExceeded.Error() {
				t.Fatal("timeout was swallowed or included private URL/query", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = client.Search(ctx, "synthetic-private-query", 1)
			if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
				t.Fatal("cancellation was swallowed or included private URL/query", err)
			}
		})
	}
}
