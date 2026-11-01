package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

//go:embed testdata/recognition-id-source01.json
var originalRecognitionIDSource01 []byte

// 只投影完整源观测中的八个 get_info case；其它原节点/trace 元数据仍保存在原JSON。
// GetInfo 是 recording port，不将该根称为 provider HTTP/cache/完整 MediaInfo 门。
func TestOriginalRecognitionIDActualSource(t *testing.T) {
	var oracle struct {
		Status string `json:"status"`
		Cases  []struct {
			ID    string `json:"id"`
			Input struct {
				Operation string `json:"operation"`
				Args      struct {
					TMDBID int64   `json:"tmdbid"`
					MType  *string `json:"mtype"`
				} `json:"args"`
				Steps []struct {
					Reply  json.RawMessage                  `json:"reply"`
					Raises *struct{ Class, Message string } `json:"raises"`
				} `json:"steps"`
			} `json:"input"`
			Outcome struct {
				Kind, Class, Message string
				Value                json.RawMessage `json:"value"`
			} `json:"outcome"`
			Trace     []json.RawMessage `json:"trace"`
			ReadCount int               `json:"recording_read_count"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(originalRecognitionIDSource01, &oracle); err != nil || oracle.Status != "ORIGINAL_S11_ID_RECORDING_OBSERVATION_COMPLETE" || len(oracle.Cases) != 12 {
		t.Fatalf("source observation invalid: %v", err)
	}
	equalJSON := func(left, right json.RawMessage) bool {
		if len(left) == 0 {
			left = json.RawMessage(`null`)
		}
		if len(right) == 0 {
			right = json.RawMessage(`null`)
		}
		a, ea := decodeSourceJSON(left)
		b, eb := decodeSourceJSON(right)
		return ea == nil && eb == nil && reflect.DeepEqual(a, b)
	}
	selected := 0
	for _, tc := range oracle.Cases {
		if tc.Input.Operation != "get_info" {
			continue
		}
		selected++
		t.Run(tc.ID, func(t *testing.T) {
			mtype := ""
			if tc.Input.Args.MType != nil {
				var ok bool
				mtype, ok = map[string]string{"MOVIE": "电影", "TV": "电视剧"}[*tc.Input.Args.MType]
				if !ok {
					t.Fatal("unobserved source enum")
				}
			}
			reads := 0
			actual := []map[string]any{}
			var supplied error
			ports := originalRecognitionIDPorts{
				GetInfo: func(_ context.Context, kind string, id int64) (json.RawMessage, error) {
					reads++
					actual = append(actual, map[string]any{"event": "recording_read", "method": "get_info", "ordinal": reads, "kwargs": map[string]any{"mtype": kind, "tmdbid": id}})
					if reads > len(tc.Input.Steps) {
						t.Fatal("unexpected native read")
					}
					step := tc.Input.Steps[reads-1]
					if step.Raises != nil {
						supplied = errors.New(step.Raises.Message)
						actual = append(actual, map[string]any{"event": "recording_exception", "class": step.Raises.Class, "message": step.Raises.Message})
						return nil, supplied
					}
					value, err := decodeSourceJSON(step.Reply)
					if err != nil {
						t.Fatal(err)
					}
					actual = append(actual, map[string]any{"event": "recording_return", "value": value})
					return step.Reply, nil
				},
				Log: func(level, message string) {
					actual = append(actual, map[string]any{"event": "log", "level": level, "message": message})
				},
			}
			got, err := originalRecognitionIDInfo(context.Background(), tc.Input.Args.TMDBID, mtype, ports)
			if tc.Outcome.Kind == "uncaught_exception" {
				if supplied == nil || err != supplied || tc.Outcome.Class != "RuntimeError" || err.Error() != tc.Outcome.Message {
					t.Fatalf("dependency error lost: %v", err)
				}
			} else if err != nil || !equalJSON(got, tc.Outcome.Value) {
				t.Fatalf("source result changed: %s, %v; want %s", got, err, tc.Outcome.Value)
			}
			if reads != tc.ReadCount || len(actual) != len(tc.Trace) {
				t.Fatalf("source trace length changed: %d/%d", reads, len(actual))
			}
			for i, raw := range tc.Trace {
				value, err := decodeSourceJSON(raw)
				if err != nil || sourceMap(value) == nil {
					t.Fatal("bad source trace")
				}
				want := sourceMap(value)
				// Python runtime-type metadata stays in the source record; it is not
				// a Go execution claim. All actual call/return/error/log fields compare.
				delete(want, "python_types")
				left, err := json.Marshal(actual[i])
				if err != nil {
					t.Fatal(err)
				}
				right, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				if !equalJSON(left, right) {
					t.Fatalf("trace %d: %s != %s", i, left, right)
				}
			}
		})
	}
	if selected != 8 {
		t.Fatalf("expected eight actual selector cases, got %d", selected)
	}
}
