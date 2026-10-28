package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOriginalServiceAPITokenActualSDK(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	ownedProgressConfig(t, a, cookie, "tuple-fail", "", 3600000)
	token := url.QueryEscape(a.Settings().APIToken)
	for _, prefix := range []string{"/api/", "/api/v1/", "/api/v2/"} {
		path := prefix + "dashboard/schedule2"
		for _, query := range []string{"", "?token=invalid", "?apikey=" + token, "?token=" + token + "&token=invalid"} {
			r := callTest(t, a.Handler(), http.MethodGet, path+query, "", cookie, "")
			if r.Code != 401 {
				t.Fatal("token endpoint accepted wrong authority", path+query, r.Code)
			}
		}
		r := callTest(t, a.Handler(), http.MethodGet, path+"?token=invalid&token="+token, "", nil, "")
		var result struct {
			Data []PluginServiceInfo `json:"data"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || len(result.Data) != 1 || result.Data[0].ID != "OwnedInput_owned_progress" {
			t.Fatal("API token could not query actual registered service", r.Code, r.Body.String())
		}
	}
	base := "/api/v1/system/runscheduler2?token=" + token
	if r := callTest(t, a.Handler(), http.MethodGet, base, "", nil, ""); r.Code != 422 {
		t.Fatal("required jobid lost validation", r.Code, r.Body.String())
	}
	if r := callTest(t, a.Handler(), http.MethodGet, base+"&jobid=", "", nil, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"success":false`) {
		t.Fatal("empty original jobid result", r.Code, r.Body.String())
	}
	if r := callTest(t, a.Handler(), http.MethodGet, base+"&jobid=missing", "", nil, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"success":true`) {
		t.Fatal("missing job should remain source no-op", r.Code, r.Body.String())
	}
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest(http.MethodGet, base+"&jobid=OwnedInput_owned_progress", nil)
		if header == "Origin" {
			r.Header.Set(header, "https://foreign.invalid")
		} else {
			r.Header.Set(header, "cross-site")
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("token route bypassed existing cross-site write guard", header, w.Code)
		}
	}
	if r := callTest(t, a.Handler(), http.MethodPost, base+"&jobid=OwnedInput_owned_progress", "", nil, ""); r.Code != 405 || r.Header().Get("Allow") != http.MethodGet {
		t.Fatal("token route accepted wrong method", r.Code, r.Body.String())
	}
	// No session cookie: the valid token invokes the actual worker and waits for
	// its false tuple. The original API still returns success for the request.
	r := callTest(t, a.Handler(), http.MethodGet, base+"&jobid=missing&jobid=OwnedInput_owned_progress", "", nil, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"success":true`) {
		t.Fatal("token manual service request failed", r.Code, r.Body.String())
	}
	r = callTest(t, a.Handler(), http.MethodGet, "/api/v2/dashboard/schedule2/OwnedInput_owned_progress/progress?token="+token, "", nil, "")
	var progress struct {
		Data struct {
			Success bool `json:"success"`
			Data    struct {
				Status  string  `json:"status"`
				Value   float64 `json:"value"`
				Success bool    `json:"success"`
				Error   string  `json:"error"`
			} `json:"data"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &progress) != nil || !progress.Data.Success || progress.Data.Data.Status != "failed" || progress.Data.Data.Value != 37 || progress.Data.Data.Success || progress.Data.Data.Error != "owned service failure" {
		t.Fatal("token API returned before real worker result", r.Code, r.Body.String())
	}
	r = callTest(t, a.Handler(), http.MethodGet, "/api/dashboard/schedule2/missing/progress?token="+token, "", nil, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"success":false`) {
		t.Fatal("unknown progress response", r.Code, r.Body.String())
	}
	if r := callTest(t, a.Handler(), http.MethodGet, "/api/v1/dashboard/schedule?token="+token, "", nil, ""); r.Code != 401 {
		t.Fatal("token widened administrator-only route", r.Code)
	}
}
