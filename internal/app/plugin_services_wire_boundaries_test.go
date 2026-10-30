package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestOriginalServiceWireQueryDecoding(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-services/wire-query-decoding-source-oracle.json")
	var oracle struct {
		Cases []struct {
			RawHex  string `json:"raw_hex"`
			Value   string `json:"value"`
			Present bool   `json:"present"`
		} `json:"cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) < 30 {
		t.Fatal("actual source QueryParams oracle unavailable", err)
	}
	for _, row := range oracle.Cases {
		raw, err := hex.DecodeString(row.RawHex)
		if err != nil {
			t.Fatal(err)
		}
		got, present := originalServiceWireQuery(string(raw), "jobid")
		if got != row.Value || present != row.Present {
			t.Fatalf("query bytes %s: got (%q, %v), source (%q, %v)", row.RawHex, got, present, row.Value, row.Present)
		}
	}
}

// Called by TestPluginServicesProductionCLI, after stdin bootstrap and login.
// Every request crosses a real TCP listener, App.Handler and ServeMux perimeter;
// unlike a handler-only test, HEAD semantics and mux redirects are observable.
func assertOriginalServiceWireBoundaryCLI(t *testing.T, authenticated *http.Client, origin, token string) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/standard-services/wire-boundary-source-oracle.json")
	var oracle struct {
		Commit       string                   `json:"source_commit"`
		Observations []serviceWireObservation `json:"observations"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Observations) != 56 {
		t.Fatal("actual independent source boundary56 unavailable", err)
	}
	client := *authenticated
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	anonymous := client
	anonymous.Jar = nil
	observations := []serviceWireObservation{}
	capture := func(name, method, path string, headers map[string]string, auth bool) serviceWireObservation {
		t.Helper()
		r, err := http.NewRequest(method, origin+path, nil)
		if err != nil {
			t.Fatal("owned boundary request construction failed")
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		c := &client
		if !auth {
			c = &anonymous
		}
		response, err := c.Do(r)
		if err != nil {
			t.Fatal("owned boundary HTTP failed", name)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if err != nil {
			t.Fatal("owned boundary body failed", name)
		}
		if bytes.Contains(body, []byte(token)) {
			t.Fatal("runtime authority leaked in body")
		}
		row := serviceWireObservation{name, path, response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("Allow"), response.Header.Get("Location"), method, string(body)}
		safe := row
		safe.Path = strings.ReplaceAll(safe.Path, token, "<owned-token>")
		safe.Location = strings.ReplaceAll(safe.Location, token, "<owned-token>")
		observations = append(observations, safe)
		return row
	}
	checkRedirect := func(row serviceWireObservation, canonical, query string) {
		t.Helper()
		if row.Status != 307 || row.Body != "" || row.ContentType != "" || row.Location != origin+canonical+query {
			t.Fatal("owned source redirect differs", row.Case, row.Status)
		}
	}
	for _, want := range oracle.Observations {
		t.Run("wire_boundary/"+want.Case, func(t *testing.T) {
			path, addedToken := want.Path, ""
			if strings.Contains(path, "schedule2") || strings.Contains(path, "runscheduler2") {
				addedToken = "&token=" + url.QueryEscape(token)
				path += addedToken
			}
			got := capture(want.Case, want.Method, path, nil, true)
			if want.Location != "" {
				canonical, query, _ := strings.Cut(want.Path, "?")
				checkRedirect(got, strings.TrimRight(canonical, "/"), "?"+query+addedToken)
				got.Location = strings.TrimSuffix(got.Location, addedToken)
			}
			got.Path = want.Path
			assertServiceWireObservation(t, want, got)
		})
	}
	for _, version := range []string{"v1", "v2"} {
		for _, route := range []string{"dashboard/schedule", "dashboard/schedule2", "dashboard/schedule/unknown/progress", "dashboard/schedule2/unknown/progress", "system/runscheduler", "system/runscheduler2"} {
			base := "/api/" + version + "/" + route
			isToken := strings.Contains(route, "schedule2") || strings.Contains(route, "runscheduler2")
			query := "?jobid=unknown"
			if isToken {
				query += "&token=" + url.QueryEscape(token)
			}
			if row := capture(base+"_anonymous", "GET", base+"//", nil, false); row.Status != 401 {
				t.Fatal("anonymous service slash accepted", base)
			}
			row := capture(base+"_post_slash", "POST", base+"//"+query, nil, true)
			checkRedirect(row, base, query)
			if isToken {
				if row := capture(base+"_last_token", "GET", base+"//?token="+url.QueryEscape(token)+"&token=invalid", nil, true); row.Status != 401 {
					t.Fatal("last token authority weakened", base)
				}
			}
			if strings.HasPrefix(route, "system/") {
				for header, value := range map[string]string{"Origin": "https://foreign.invalid", "Sec-Fetch-Site": "cross-site"} {
					row := capture(base+"_foreign_slash_"+header, "GET", base+"//"+query, map[string]string{header: value}, true)
					checkRedirect(row, base, query)
					if row := capture(base+"_foreign_canonical_"+header, "GET", base+query, map[string]string{header: value}, true); row.Status != 403 {
						t.Fatal("manual origin guard weakened", base)
					}
				}
			}
		}
		base := "/api/" + version + "/dashboard/schedule"
		row := capture(base+"_forwarded_origin", "GET", base+"//", map[string]string{"X-Forwarded-Host": "foreign.invalid", "X-Forwarded-Proto": "https"}, true)
		checkRedirect(row, base, "")
	}
	for _, path := range []string{"/api/plugins/services", "/api/dashboard/schedule", "/api/dashboard/schedule2?token=" + url.QueryEscape(token)} {
		row := capture("native", "GET", path, nil, true)
		var value map[string]json.RawMessage
		var rows []json.RawMessage
		if row.Status != 200 || json.Unmarshal([]byte(row.Body), &value) != nil || len(value) != 2 || len(value["request_id"]) == 0 || json.Unmarshal(value["data"], &rows) != nil {
			t.Fatal("native/unversioned envelope changed")
		}
	}
	if row := capture("native_missing_run", "POST", "/api/plugins/services/run", nil, true); row.Status != 400 {
		t.Fatal("native validation changed")
	}
	if len(observations) != 108 {
		t.Fatal("incomplete real CLI boundary observations", len(observations))
	}
	if output := os.Getenv("OWNED_SERVICE_WIRE_BOUNDARY_OBSERVATIONS"); output != "" {
		raw, _ := json.MarshalIndent(observations, "", "  ")
		if err := os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
