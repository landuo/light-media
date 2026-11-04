package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/app"
)

type generationCloseCheck struct {
	application  *app.App
	handlersDone <-chan struct{}
	once         sync.Once
	err          error
}

func (c *generationCloseCheck) Close() error {
	c.once.Do(func() {
		select {
		case <-c.handlersDone:
		default:
			c.err = errors.New("App.Close ran before the HTTP handler returned")
		}
		c.err = errors.Join(c.err, c.application.Close())
	})
	return c.err
}

// Real TCP with the production server constructor, not a recorder or a copied
// BaseContext setup. Cleanup observes the original Serve goroutine in all paths.
func serveGenerationHTTP(t *testing.T, ctx context.Context, h http.Handler) (*http.Server, string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newGenerationHTTPServer(ctx, listener.Addr().String(), h)
	done := make(chan struct{})
	var serveErr error
	go func() { defer close(done); serveErr = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("original Serve did not finish")
			return
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			t.Errorf("Serve: %v", serveErr)
		}
	})
	return server, "http://" + listener.Addr().String(), done
}
func generationClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 8 * time.Second}
}
func generationRequest(t *testing.T, client *http.Client, method, url, body, origin string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestGenerationHTTPAuthenticatedSSEShutdown(t *testing.T) {
	for _, reason := range []string{"parent-stop", "generation-restart"} {
		t.Run(reason, func(t *testing.T) {
			parent, stop := context.WithCancel(context.Background())
			defer stop()
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			a, err := app.New(app.Options{DataDir: t.TempDir(), SafeMode: true})
			if err != nil {
				t.Fatal(err)
			}
			streamDone := make(chan struct{})
			closer := &generationCloseCheck{application: a, handlersDone: streamDone}
			t.Cleanup(func() { _ = closer.Close() })
			handler := a.Handler()
			// Only the authenticated SSE is tracked. Rejected requests must finish
			// before opening this stream and must retain the existing auth/origin gates.
			server, url, serveDone := serveGenerationHTTP(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/system/message" && r.URL.Query().Get("tracked") == "1" {
					defer close(streamDone)
				}
				handler.ServeHTTP(w, r)
			}))
			client := generationClient(t)
			setup := generationRequest(t, client, "POST", url+"/api/auth/setup", `{"username":"admin","password":"synthetic-generation-test-password"}`, url, nil)
			if setup.StatusCode != http.StatusOK {
				t.Fatalf("setup: %d", setup.StatusCode)
			}
			var cookie *http.Cookie
			for _, c := range setup.Cookies() {
				if c.Name == "lm_session" {
					cookie = c
				}
			}
			setup.Body.Close()
			if cookie == nil {
				t.Fatal("setup did not establish a session")
			}
			for _, c := range []struct {
				cookie *http.Cookie
				origin string
				status int
			}{
				{nil, url, http.StatusUnauthorized}, {cookie, "http://foreign.invalid", http.StatusForbidden},
			} {
				response := generationRequest(t, client, "GET", url+"/api/system/message", "", c.origin, c.cookie)
				if response.StatusCode != c.status {
					t.Fatalf("SSE auth/origin: got %d want %d", response.StatusCode, c.status)
				}
				response.Body.Close()
			}
			stream := generationRequest(t, client, "GET", url+"/api/system/message?tracked=1", "", url, cookie)
			if stream.StatusCode != http.StatusOK || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatal("authenticated stream unavailable")
			}
			reader := bufio.NewReader(stream.Body)
			frame, err := reader.ReadString('\n')
			if err != nil || !strings.HasPrefix(frame, "data: ") {
				t.Fatalf("first SSE frame: %q %v", frame, err)
			}
			if blank, err := reader.ReadString('\n'); err != nil || blank != "\n" {
				t.Fatal("invalid SSE frame delimiter")
			}
			select {
			case <-streamDone:
				t.Fatal("stream was not live at shutdown")
			default:
			}
			if reason == "parent-stop" {
				stop()
			}
			// Restart takes the production shutdown cancel path while parent remains
			// live; parent stop takes the other lifetime branch. No client disconnect.
			if err := shutdownServerGeneration(cancel, server, closer); err != nil {
				t.Fatalf("generation shutdown: %v", err)
			}
			select {
			case <-streamDone:
			default:
				t.Fatal("SSE did not drain")
			}
			select {
			case <-serveDone:
			case <-time.After(time.Second):
				t.Fatal("original Serve not observed")
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				t.Fatalf("SSE did not end cleanly: %v", err)
			}
			if reason == "generation-restart" && parent.Err() != nil {
				t.Fatal("generation shutdown canceled its supervisor")
			}
		})
	}
}

func TestGenerationHTTPOrdinaryRequestDrainsBeforeStoreClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := app.New(app.Options{DataDir: t.TempDir(), SafeMode: true})
	if err != nil {
		t.Fatal(err)
	}
	entered, canceled, returned, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	closer := &generationCloseCheck{application: a, handlersDone: returned}
	t.Cleanup(func() { _ = closer.Close() })
	server, url, _ := serveGenerationHTTP(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		close(entered)
		<-r.Context().Done()
		close(canceled)
		<-release
		// A regular handler may finish an admitted operation after cancellation.
		// Store must remain usable until it returns, not close ahead of HTTP drain.
		var one int
		if err := a.Store.DB.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
			http.Error(w, "store closed before drain", 500)
			return
		}
		fmt.Fprint(w, "drained")
	}))
	t.Cleanup(unblock)
	client := generationClient(t)
	requestDone := make(chan error, 1)
	requestFinished := make(chan struct{})
	go func() {
		defer close(requestFinished)
		response, err := client.Get(url)
		if err != nil {
			requestDone <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != 200 || string(body) != "drained") {
			err = fmt.Errorf("ordinary response: %d %q", response.StatusCode, body)
		}
		requestDone <- err
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		_ = server.Close()
		select {
		case <-requestFinished:
		case <-time.After(5 * time.Second):
			t.Error("owned HTTP client did not finish")
		}
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Error("owned ordinary handler did not finish")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary request did not enter")
	}
	shutdownDone := make(chan error, 1)
	shutdownFinished := make(chan struct{})
	go func() {
		defer close(shutdownFinished)
		shutdownDone <- shutdownServerGeneration(cancel, server, closer)
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		_ = server.Close()
		select {
		case <-shutdownFinished:
		case <-time.After(5 * time.Second):
			t.Error("owned shutdown goroutine did not finish")
		}
	})
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("generation cancellation did not reach real HTTP request")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown completed with handler held: %v", err)
	default:
	}
	if err := a.Store.DB.Ping(); err != nil {
		t.Fatalf("store closed with handler held: %v", err)
	}
	unblock()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary response did not finish")
	}
}
