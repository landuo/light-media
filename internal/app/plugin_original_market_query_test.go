package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginalMarketSourceForceQueryAliasesAndLastValue(t *testing.T) {
	for _, raw := range []string{"1", "on", "t", "true", "y", "yes", "ON", "TRUE", "Y"} {
		got, err := originalMarketForceQuery(httptest.NewRequest("GET", "/api/plugins/market?force="+raw, nil), false)
		if err != nil || !got {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"0", "off", "f", "false", "n", "no", "OFF", "FALSE", "N"} {
		got, err := originalMarketForceQuery(httptest.NewRequest("GET", "/api/plugins/market?force="+raw, nil), true)
		if err != nil || got {
			t.Fatal(raw, got, err)
		}
	}
	for query, want := range map[string]bool{"": true, "?force=true&force=false": false, "?force=false&force=true": true, "?force=bad&force=true": true} {
		got, err := originalMarketForceQuery(httptest.NewRequest("GET", "/api/plugins/market"+query, nil), true)
		if err != nil || got != want {
			t.Fatal(query, got, err)
		}
	}
	for query, input := range map[string]string{"?force=": "", "?force=bad": "bad", "?force=true&force=bad": "bad"} {
		request := httptest.NewRequest("GET", "/api/plugins/market"+query, nil)
		_, err := originalMarketForceQuery(request, false)
		if err == nil {
			t.Fatal("invalid source query accepted", query)
		}
		response := httptest.NewRecorder()
		(&App{}).writeError(response, request, err)
		want := `{"detail":[{"type":"bool_parsing","loc":["query","force"],"msg":"Input should be a valid boolean, unable to interpret input","input":"` + input + `"}]}`
		if response.Code != http.StatusUnprocessableEntity || response.Body.String() != want {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}

func TestOriginalMarketSourceScalarQueryLastValue(t *testing.T) {
	for _, key := range []string{"state", "repo_url"} {
		request := httptest.NewRequest("GET", "/api/plugins?"+key+"=first&"+key+"=last", nil)
		if value := originalMarketQueryString(request, key); value != "last" {
			t.Fatal(key, value)
		}
	}
}
