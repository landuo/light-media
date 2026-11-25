package mpserverstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIndependentActual312TextPropertyErrors(t *testing.T) {
	raw, e := os.ReadFile("reference/source-text-review.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Encoding string
			Error    *struct{ Type, Message string }
			Result   *struct {
				Metrics       bool    `json:"metrics_present"`
				SnapshotError *string `json:"snapshot_error"`
				Status        int
			}
			Sample bool     `json:"sample_present"`
			Logs   []string `json:"log_levels"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		t.Run(c.Encoding, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset="+c.Encoding)
				_, _ = fmt.Fprint(w, "Active connections: 7\nserver accepts handled requests\n10 9 20\nReading:1 Writing:2 Waiting:4")
			}))
			defer server.Close()
			environment := []string{}
			native := &NativeNetwork{environment: &environment}
			machine := New(nil, nil)
			machine.URL = server.URL
			logs := []string{}
			snapshot, e := machine.LoadWith(context.Background(), false, native, func(ctx context.Context, level, message string) error {
				logs = append(logs, strings.ToLower(level))
				return nil
			})
			if c.Error != nil {
				if e == nil || e.Error() != c.Error.Message {
					t.Fatalf("source text attribute error %q got %v", c.Error.Message, e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if (len(snapshot.Metrics) > 0) != c.Result.Metrics {
					t.Fatal("source metrics presence", snapshot)
				}
				want := ""
				if c.Result.SnapshotError != nil {
					want = *c.Result.SnapshotError
				}
				if snapshot.Error != want {
					t.Fatal("source snapshot error", snapshot.Error, want)
				}
			}
			if (machine.last() != nil) != c.Sample || strings.Join(logs, ",") != strings.Join(c.Logs, ",") {
				t.Fatal("source property timing/log/sample mismatch", machine.last(), logs, c.Logs)
			}
		})
	}
}
