package apprise

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestOriginalAppriseJSONRequests(t *testing.T) {
	raw, err := os.ReadFile("testdata/json-native-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		URL    string
		Status int
		Added  bool
		Result any
		Calls  []map[string]any
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 70 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			calls := []map[string]any{}
			client := New(func(ctx context.Context, r Request) (Response, error) {
				var payload any
				if err := json.Unmarshal(r.Data, &payload); err != nil {
					t.Fatal(err)
				}
				var auth any
				if r.Auth != nil {
					auth = r.Auth
				}
				calls = append(calls, map[string]any{"method": r.Method, "url": r.URL, "payload": payload, "headers": r.Headers, "params": r.Params, "auth": auth, "verify": r.Verify, "timeout": r.Timeout})
				return Response{Status: c.Status}, nil
			})
			added, err := client.Add(c.URL)
			if err != nil || added != c.Added {
				t.Fatal(added, err, c.Added)
			}
			result, err := client.Notify(context.Background(), "  合成标题<&>  ", "正文\n第二行  ", nil)
			if err != nil || result != c.Result {
				t.Fatal(result, err, c.Result)
			}
			b, _ := json.Marshal(calls)
			var normalized []map[string]any
			json.Unmarshal(b, &normalized)
			if !reflect.DeepEqual(normalized, c.Calls) {
				t.Fatalf("calls got=%s expected=%v", b, c.Calls)
			}
		})
	}
}
