package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootstrapReadinessAuthenticatedAndIndependentOfStatistics(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	started := make(chan struct{})
	h := a.StartupHandler(started)
	if w := callTest(t, h, "GET", "/api/ready", "", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous readiness: %d %s", w.Code, w.Body)
	}
	if w := callTest(t, h, "GET", "/api/ready", "", cookie, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"starting":true`) {
		t.Fatalf("startup readiness: %d %s", w.Code, w.Body)
	}
	close(started)
	// status() requires this lock after its database counts. Readiness must
	// neither enter statistics nor wait for the control snapshot it includes.
	a.controlMu.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", "/api/ready", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case w := <-done:
		a.controlMu.Unlock()
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"starting":false`) || strings.Contains(w.Body.String(), `"counts"`) {
			t.Fatalf("ready response: %d %s", w.Code, w.Body)
		}
	case <-time.After(time.Second):
		a.controlMu.Unlock()
		<-done
		t.Fatal("readiness waited for full statistics")
	}
}

func TestBootstrapQueriesRespectCallerCancellation(t *testing.T) {
	a := newTestApp(t)
	setupTestAdmin(t, a)
	// Occupy every read connection, so a missing QueryRowContext/BeginTx
	// context would wait indefinitely rather than failing the caller budget.
	var held []*sql.Conn
	for i := 0; i < 4; i++ {
		conn, err := a.Store.reader().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	release := func() {
		for _, conn := range held {
			_ = conn.Close()
		}
		held = nil
	}
	defer release()
	for _, path := range []string{"/api/auth/status", "/api/settings"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := httptest.NewRecorder()
			// Exercise the actual body of each endpoint; authentication is
			// independently covered by TestNativeDisplayAuthenticationDeadline.
			a.api(w, httptest.NewRequest("GET", path, nil).WithContext(ctx))
			done <- w
		}()
		select {
		case w := <-done:
			cancel()
			if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "REQUEST_TIMEOUT") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
		case <-time.After(time.Second):
			cancel()
			release()
			<-done
			t.Fatalf("%s ignored caller cancellation", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.hasAdminContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("admin cancellation: %v", err)
	}
	if _, err := a.publicSettingsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("settings cancellation: %v", err)
	}
}
