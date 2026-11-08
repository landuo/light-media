package browserrender

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"testing"
)

//go:embed testdata/source_native_json_projection_original5.json
var nativeSourceOriginal5 []byte

func TestNativeSourceJSONProjectionMatrix(t *testing.T) {
	var oracle struct {
		Rows []struct {
			Name           string
			RawHex         string `json:"raw_hex"`
			SourceFallback bool   `json:"source_fallback"`
			NativeFallback bool   `json:"native_fallback"`
		}
	}
	if e := json.Unmarshal(nativeSourceOriginal5, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"native", "source"} {
		for _, r := range oracle.Rows {
			t.Run(mode+"/"+r.Name, func(t *testing.T) {
				data, e := hex.DecodeString(r.RawHex)
				if e != nil {
					t.Fatal(e)
				}
				calls := 0
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
					io.Copy(io.Discard, q.Body)
					calls++
					w.Header().Set("Content-Type", "application/json")
					w.Write(data)
				}))
				defer peer.Close()
				c := Config{FlareSolverrURL: peer.URL, HTTPClient: peer.Client()}
				request := Request{URL: peer.URL + "/attendance.php"}
				if mode == "source" {
					request.SourceTimeout = json.RawMessage(`60`)
				}
				value, err := c.flareSolverr(context.Background(), request)
				fallback := err != nil || len(value) == 0
				msg := ""
				if err != nil {
					msg = err.Error()
				}
				text, _ := value["response"].(string)
				numberType := ""
				if u, ok := value["unused"].(map[string]any); ok {
					numberType = fmt.Sprintf("%T", u["n"])
				}
				observation := map[string]any{"mode": mode, "case": r.Name, "fallback": fallback, "error": msg, "response": text, "solution_unused_type": numberType, "http": calls}
				record, _ := json.Marshal(observation)
				t.Log("NATIVE_SOURCE_JSON_OBSERVATION", string(record))
				if calls != 1 {
					t.Fatal("real FS HTTP1", calls)
				}
				want := r.NativeFallback
				if mode == "source" {
					want = r.SourceFallback
				}
				if fallback != want {
					t.Fatalf("original native/source projection differs fallback=%v want=%v response=%q error=%v", fallback, want, text, err)
				}
				if !want && text != "owned native" {
					t.Fatal("original projected response", text)
				}
				if mode == "native" && r.Name == "normal-solution-unused" && numberType != "float64" {
					t.Fatal("legacy native numeric map type lost", numberType)
				}
			})
		}
	}
	if len(oracle.Rows) != 5 {
		t.Fatal(len(oracle.Rows))
	}
}
