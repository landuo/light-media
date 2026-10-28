package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicPluginServicesAuthAndCrossSiteGETGuard(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	for _, path := range []string{"/api/plugins/services", "/api/v1/dashboard/schedule", "/api/v1/system/runscheduler?jobid=anything"} {
		if w := callTest(t, h, "GET", path, "", nil, ""); w.Code != 401 {
			t.Fatal("unauthenticated service API", path, w.Code)
		}
	}
	if w := callTest(t, h, "POST", "/api/plugins/services/run", `{}`, cookie, "http://foreign.invalid"); w.Code != 403 {
		t.Fatal("foreign POST accepted", w.Code)
	}
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest("GET", "/api/v1/system/runscheduler?jobid=anything", nil)
		r.AddCookie(cookie)
		if header == "Origin" {
			r.Header.Set(header, "http://foreign.invalid")
		} else {
			r.Header.Set(header, "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-site GET executor accepted", header, w.Code, w.Body.String())
		}
	}
	if w := callTest(t, h, "GET", "/api/v1/system/runscheduler?jobid=missing", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal("original missing-service no-op response changed", w.Code, w.Body.String())
	}
	if w := callTest(t, h, "POST", "/api/plugins/services/run", `{"instance_id":"missing","service_id":"missing"}`, cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_SERVICE_NOT_FOUND") {
		t.Fatal("native service endpoint lost explicit missing-service response", w.Code, w.Body.String())
	}
}

func TestPluginServiceTimeDifferenceOriginalUnits(t *testing.T) {
	at := time.Now()
	for _, sample := range []struct {
		delta time.Duration
		want  string
	}{
		{-time.Second, ""}, {500 * time.Millisecond, ""}, {time.Second, "1秒"}, {59 * time.Second, "59秒"}, {61 * time.Second, "1分钟"}, {3601 * time.Second, "1小时"}, {(86400 + 7200 + 180 + 59) * time.Second, "1天2小时3分钟"},
	} {
		if got := pluginServiceTimeDifference(at.Add(sample.delta), at); got != sample.want {
			t.Fatalf("%v: %q != %q", sample.delta, got, sample.want)
		}
	}
}
