package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSourceNamesActualFixedAST(t *testing.T) {
	data, e := os.ReadFile("../plugins/brushflow/reference/framework/names-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Kind     string
			Raw      json.RawMessage
			Expected []string
		}
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	for i, row := range fixture.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/3/"+row.Kind+"/123" || !strings.Contains(r.URL.Query().Get("append_to_response"), "translations") || r.URL.Query().Get("api_key") != "synthetic-secret" {
					t.Error("source recognition request", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(row.Raw)
			})
			got, e := client.SourceNames(context.Background(), row.Kind, 123)
			if e != nil || !reflect.DeepEqual(got, row.Expected) {
				t.Fatal("source names", got, row.Expected, e)
			}
		})
	}
}
