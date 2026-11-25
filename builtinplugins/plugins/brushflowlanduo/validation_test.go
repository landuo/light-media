package brushflowlanduo

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestBrushHTTPValidationMatchesFastAPI(t *testing.T) {
	data, err := os.ReadFile("testdata/fastapi-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Method      string
			Path        string
			Query       map[string]string
			Body        json.RawMessage
			Detail      []any
			Status      int
			ContentType string `json:"content_type"`
			Text        string
		}
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range oracle.Cases {
		t.Run(tc.Method+tc.Path, func(t *testing.T) {
			key := "create"
			switch {
			case tc.Method == "GET":
				key = "detail"
			case tc.Method == "PUT":
				key = "update"
			case tc.Path == "/settings":
				key = "settings"
			case strings.HasSuffix(tc.Path, "/state"):
				key = "state"
			}
			req := sdk.HTTPRequest{Key: key, Path: tc.Path, Method: tc.Method, Body: tc.Body, Query: map[string][]string{}}
			for k, v := range tc.Query {
				req.Query[k] = []string{v}
			}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			host := &brushConfigEntryHost{}
			p := &Standard{core: NewUninitialized(host)}
			raw, err = p.http(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			var response sdk.HTTPResponse
			if err = json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if host.saved != nil || len(host.queued) != 0 || len(p.core.Config().Tasks) != 0 {
				t.Fatal("rejected request changed task configuration or queued work")
			}
			if err = response.Validate(); err != nil {
				t.Fatal(err)
			}
			if tc.Status == 500 {
				if response.Status != tc.Status || response.Headers["Content-Type"] != tc.ContentType || string(response.Body) != tc.Text {
					t.Fatalf("HTTP %d %s %q", response.Status, response.Headers["Content-Type"], response.Body)
				}
				return
			}
			var body struct{ Detail []any }
			if err = json.Unmarshal(response.Body, &body); err != nil {
				t.Fatal(err)
			}
			if response.Status != 422 || !reflect.DeepEqual(body.Detail, tc.Detail) {
				t.Fatalf("HTTP %d\n got %s\nwant %+v", response.Status, response.Body, tc.Detail)
			}
		})
	}
}
