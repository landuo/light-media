package app

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type serviceWireObservation struct {
	Case        string `json:"case"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Allow       string `json:"allow"`
	Location    string `json:"location"`
	Method      string `json:"method"`
	Body        string `json:"body"`
}

// Unlike the native registry helper, manual source acknowledgement has exactly
// four top-level fields, null messages and an empty object (not a host envelope).
func assertOriginalServiceWireSuccess(t *testing.T, status int, raw []byte) {
	t.Helper()
	var value map[string]json.RawMessage
	if status != 200 || json.Unmarshal(raw, &value) != nil || len(value) != 4 ||
		string(value["success"]) != "true" || string(value["message"]) != "null" ||
		string(value["message_i18n"]) != "null" || string(value["data"]) != "{}" {
		t.Fatalf("original manual acknowledgement expected: %d %s", status, raw)
	}
}

func serviceWireComparable(t *testing.T, body string, source bool) any {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal("invalid JSON wire", err)
	}
	var visit func(any, string) any
	visit = func(value any, key string) any {
		switch typed := value.(type) {
		case map[string]any:
			for name, item := range typed {
				typed[name] = visit(item, name)
			}
			return typed
		case []any:
			for index, item := range typed {
				typed[index] = visit(item, "")
			}
			return typed
		case json.Number:
			number, ok := new(big.Rat).SetString(string(typed))
			if !ok {
				t.Fatal("invalid precise number", typed)
			}
			return struct{ Number string }{number.RatString()}
		case string:
			if key == "started_at" || key == "finished_at" {
				if _, err := time.Parse("2006-01-02 15:04:05", typed); err != nil {
					t.Fatal("invalid observed time", typed)
				}
				return "<observed-local-time>"
			}
			if source {
				if typed == "OwnedServices_manual" {
					return "OwnedInput_owned_progress"
				}
				if typed == "Owned services" {
					return "Owned input interaction"
				}
			}
		}
		return value
	}
	return visit(value, "")
}

func assertServiceWireObservation(t *testing.T, want, got serviceWireObservation) {
	t.Helper()
	if want.Status != got.Status || want.ContentType != got.ContentType || want.Allow != got.Allow || want.Method != got.Method {
		t.Fatalf("wire status/headers: source=%+v target=%+v", want, got)
	}
	if want.Location != "" {
		source, e1 := url.Parse(want.Location)
		target, e2 := url.Parse(got.Location)
		if e1 != nil || e2 != nil || source.RequestURI() != target.RequestURI() {
			t.Fatal("redirect path/query differ", want.Location, got.Location)
		}
	} else if got.Location != "" {
		t.Fatal("unexpected redirect")
	}
	if want.Method == http.MethodHead || want.ContentType != "application/json" {
		if want.Body != got.Body {
			t.Fatalf("non-JSON bytes differ: %q vs %q", want.Body, got.Body)
		}
		return
	}
	if !reflect.DeepEqual(serviceWireComparable(t, want.Body, true), serviceWireComparable(t, got.Body, false)) {
		t.Fatalf("complete JSON wire differs\nsource: %s\ntarget: %s", want.Body, got.Body)
	}
}

func TestOriginalServiceWireRoutingSafety(t *testing.T) {
	a, cookie := installOwnedInputFixture(t)
	for _, version := range []string{"v1", "v2"} {
		for _, route := range []string{"dashboard/schedule", "dashboard/schedule2", "dashboard/schedule/owned/progress", "dashboard/schedule2/owned/progress", "system/runscheduler", "system/runscheduler2"} {
			token := strings.Contains(route, "schedule2") || strings.Contains(route, "runscheduler2")
			path := "/api/" + version + "/" + route
			query := "?jobid=first&jobid=&owned=a%2Fb&owned=%E4%B8%AD"
			if token {
				query += "&token=" + url.QueryEscape(a.Settings().APIToken)
			}
			r := callTest(t, a.Handler(), "GET", path+"/"+query, "", cookie, "")
			u, err := url.Parse(r.Header().Get("Location"))
			if r.Code != 307 || err != nil || u.Host != "example.com" || u.Path != path || u.RawQuery != query[1:] || r.Body.Len() != 0 || r.Header().Get("Content-Type") != "" {
				t.Fatal("redirect changed raw query/body/origin", route, r.Code)
			}
			for _, method := range []string{"HEAD", "OPTIONS", "PUT", "DELETE"} {
				r = callTest(t, a.Handler(), method, path+query, "", cookie, "")
				if r.Code != 405 || r.Header().Get("Allow") != "GET" {
					t.Fatal("method widened source route", method, route, r.Code)
				}
			}
			if r = callTest(t, a.Handler(), "GET", path, "", nil, ""); r.Code != 401 {
				t.Fatal("unauthenticated read not denied", route, r.Code)
			}
		}
		for _, suffix := range []string{"runscheduler", "runscheduler2"} {
			path := "/api/" + version + "/system/" + suffix + "?jobid=unknown&jobid="
			if suffix == "runscheduler2" {
				path += "&token=" + url.QueryEscape(a.Settings().APIToken)
			}
			r := callTest(t, a.Handler(), "GET", path, "", cookie, "")
			if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"success":false`)) {
				t.Fatal("last jobid must win")
			}
			request := httptest.NewRequest("GET", path, nil)
			request.AddCookie(cookie)
			request.Header.Set("Origin", "https://foreign.invalid")
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatal("manual origin guard weakened")
			}
		}
	}
	for _, path := range []string{"/api/plugins/services", "/api/dashboard/schedule", "/api/dashboard/schedule2?token=" + url.QueryEscape(a.Settings().APIToken)} {
		r := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		var host map[string]json.RawMessage
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &host) != nil || len(host) != 2 || len(host["request_id"]) == 0 || string(host["data"]) != "[]" {
			t.Fatal("native/unversioned host contract changed")
		}
	}
}

func TestOriginalServiceWireSourceQueries20(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/wire-query-source-oracle.json")
	var oracle struct {
		Commit       string                   `json:"source_commit"`
		Observations []serviceWireObservation `json:"observations"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Observations) != 20 {
		t.Fatal("fixed independent source query20 unavailable", err)
	}
	a, cookie := installOwnedInputFixture(t)
	for _, want := range oracle.Observations {
		t.Run(want.Case, func(t *testing.T) {
			path := want.Path
			tokenSuffix := ""
			if strings.Contains(path, "schedule2") || strings.Contains(path, "runscheduler2") {
				// The real token is appended last, exercising the same last-value
				// authority rule while retaining the original synthetic token bytes.
				tokenSuffix = "&token=" + url.QueryEscape(a.Settings().APIToken)
				path += tokenSuffix
			}
			r := callTest(t, a.Handler(), want.Method, path, "", cookie, "")
			location := r.Header().Get("Location")
			if want.Location != "" {
				u, err := url.Parse(location)
				input, _ := url.Parse(path)
				if err != nil || u.Host != "example.com" || u.Scheme != "http" || u.RawQuery != input.RawQuery {
					t.Fatal("redirect altered request origin or exact raw query")
				}
				if tokenSuffix != "" {
					u.RawQuery = strings.TrimSuffix(u.RawQuery, tokenSuffix)
				}
				location = u.String()
			}
			got := serviceWireObservation{want.Case, want.Path, r.Code, r.Header().Get("Content-Type"), r.Header().Get("Allow"), location, want.Method, r.Body.String()}
			assertServiceWireObservation(t, want, got)
		})
	}
}
func TestOriginalServiceWireSource66(t *testing.T) {

	oracleRaw, err := os.ReadFile("../../testdata/standard-services/wire-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit       string                   `json:"source_commit"`
		Observations []serviceWireObservation `json:"observations"`
	}
	if json.Unmarshal(oracleRaw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Observations) != 66 {
		t.Fatal("fixed66 actual source HTTP oracle unavailable")
	}
	expected := map[string]serviceWireObservation{}
	for _, row := range oracle.Observations {
		if _, exists := expected[row.Case]; exists {
			t.Fatal("duplicate source case", row.Case)
		}
		expected[row.Case] = row
	}
	a, cookie := installOwnedInputFixture(t)
	out := []serviceWireObservation{}
	captureMethod := func(name, path, method string) {
		t.Helper()
		actual := path
		isToken := strings.Contains(path, "schedule2") || strings.Contains(path, "runscheduler2")
		if isToken {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			actual += sep + "token=" + url.QueryEscape(a.Settings().APIToken)
		}
		r := callTest(t, a.Handler(), method, actual, "", cookie, "")
		location := r.Header().Get("Location")
		if location != "" {
			u, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != "example.com" || u.Scheme != "http" {
				t.Fatal("redirect changed request origin")
			}
			if isToken {
				q := u.Query()
				if q.Get("token") != a.Settings().APIToken {
					t.Fatal("redirect changed query token")
				}
				q.Del("token")
				u.RawQuery = q.Encode()
				location = u.String()
			}
		}
		got := serviceWireObservation{name, path, r.Code, r.Header().Get("Content-Type"), r.Header().Get("Allow"), location, method, r.Body.String()}
		out = append(out, got)
		want, ok := expected[name]
		if !ok {
			t.Fatal("unexpected target case", name)
		}
		delete(expected, name)
		t.Run(name, func(t *testing.T) { assertServiceWireObservation(t, want, got) })
	}
	capture := func(name, path string) { captureMethod(name, path, "GET") }
	for _, v := range []string{"v1", "v2"} {
		base := "/api/" + v
		capture("empty_list_"+v, base+"/dashboard/schedule")
		capture("missing_jobid_"+v, base+"/system/runscheduler")
		capture("missing_jobid_token_"+v, base+"/system/runscheduler2")
		capture("empty_jobid_"+v, base+"/system/runscheduler?jobid=")
		capture("unknown_jobid_"+v, base+"/system/runscheduler?jobid=unknown")
		capture("unknown_progress_"+v, base+"/dashboard/schedule/unknown/progress")
		capture("empty_list_token_"+v, base+"/dashboard/schedule2")
		capture("unknown_progress_token_"+v, base+"/dashboard/schedule2/unknown/progress")
	}

	for _, sample := range []struct {
		mode   string
		paused bool
	}{{"waiting", false}, {"single_paused", true}} {
		opts := map[string]any{}
		if sample.paused {
			opts["next_run_time"] = nil
		}
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "services": []any{map[string]any{"id": "owned_progress", "name": "Owned manual", "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "kwargs": opts}}})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(cfg), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		for _, v := range []string{"v1", "v2"} {
			for _, suffix := range []string{"schedule", "schedule2"} {
				capture(sample.mode+"_list_"+v+"_"+suffix, "/api/"+v+"/dashboard/"+suffix)
			}
		}
	}
	for _, v := range []string{"v1", "v2"} {
		for _, route := range []string{"dashboard/schedule", "dashboard/schedule2", "dashboard/schedule/unknown/progress", "dashboard/schedule2/unknown/progress", "system/runscheduler", "system/runscheduler2"} {
			captureMethod("post_not_allowed_"+v+"_"+route, "/api/"+v+"/"+route, "POST")
			capture("trailing_slash_"+v+"_"+route, "/api/"+v+"/"+route+"/")
		}
	}
	cfg := `{"enabled":true,"services":[{"id":"paused","name":"Paused","handler":"run_service","trigger":{"kind":"interval","timezone":"UTC","interval_ms":3600000},"kwargs":{"next_run_time":null}},{"id":"waiting","name":"Waiting","handler":"run_service","trigger":{"kind":"interval","timezone":"UTC","interval_ms":3600000}}]}`
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", cfg, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	for _, v := range []string{"v1", "v2"} {
		for _, suffix := range []string{"schedule", "schedule2"} {
			capture("paused_mixed_"+v+"_"+suffix, "/api/"+v+"/dashboard/"+suffix)
		}
	}
	config, _ := json.Marshal(map[string]any{"enabled": true, "services": []any{map[string]any{"id": "owned_progress", "name": "Owned manual", "handler": "run_service", "trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "input": map[string]any{"mode": "error-empty", "progress": 37, "gate": "", "text": "owned progress update"}}}})
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	for _, v := range []string{"v1", "v2"} {
		capture("callback_empty_error_request_"+v, "/api/"+v+"/system/runscheduler?jobid=OwnedInput_owned_progress")
		capture("callback_empty_error_progress_"+v, "/api/"+v+"/dashboard/schedule/OwnedInput_owned_progress/progress")
		capture("callback_empty_error_progress_token_"+v, "/api/"+v+"/dashboard/schedule2/OwnedInput_owned_progress/progress")
		capture("callback_empty_error_list_"+v, "/api/"+v+"/dashboard/schedule")
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	p := state.progress["OwnedInput_owned_progress"]
	if p == nil {
		t.Fatal("missing actual progress")
	}
	p.data["success"] = json.RawMessage(`[]`)
	p.updated = time.Now()
	state.mu.Unlock()
	for _, v := range []string{"v1", "v2"} {
		capture("bad_progress_model_"+v, "/api/"+v+"/dashboard/schedule/OwnedInput_owned_progress/progress")
		capture("bad_progress_model_token_"+v, "/api/"+v+"/dashboard/schedule2/OwnedInput_owned_progress/progress")
		capture("bad_progress_model_list_"+v, "/api/"+v+"/dashboard/schedule")
	}
	if len(expected) != 0 {
		t.Fatal("source cases were not executed", len(expected))
	}
	if output := os.Getenv("OWNED_SERVICE_WIRE_OBSERVATIONS"); output != "" {
		raw, _ := json.MarshalIndent(out, "", "  ")
		if err := os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
