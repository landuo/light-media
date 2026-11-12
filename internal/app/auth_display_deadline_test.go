package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeDisplayAuthenticationDeadline(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	for _, tc := range []struct {
		name, path                 string
		cookie, expired, cancelled bool
		status                     int
		code                       string
	}{
		{"valid session", "/api/subscriptions", true, false, false, http.StatusOK, ""},
		{"expired caller with valid session", "/api/subscriptions", true, true, false, http.StatusGatewayTimeout, "REQUEST_TIMEOUT"},
		{"ordinary unauthenticated", "/api/subscriptions", false, false, false, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"cancelled caller is not a deadline", "/api/subscriptions", true, false, true, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"non display retains authentication behavior", "/api/settings", true, true, false, http.StatusUnauthorized, "UNAUTHORIZED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			} else if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r := httptest.NewRequest(http.MethodGet, tc.path, nil).WithContext(ctx)
			if tc.cookie {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body)
			}
			if tc.code != "" {
				var body struct {
					Error APIError `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != tc.code {
					t.Fatalf("error=%+v want=%s decode=%v body=%s", body.Error, tc.code, err, w.Body)
				}
			}
		})
	}
	// A timed-out read must not destroy an otherwise valid session.
	if w := callTest(t, h, http.MethodGet, "/api/subscriptions", "", cookie, ""); w.Code != http.StatusOK {
		t.Fatalf("session unusable after deadline: %d %s", w.Code, w.Body)
	}
}
