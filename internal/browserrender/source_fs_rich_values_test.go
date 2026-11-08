package browserrender

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/source_fs_values_original44.json
var sourceFSOriginal44 []byte

func TestSourceFSValueOriginalHTTP44(t *testing.T) {
	var o struct {
		Rows []struct {
			Name         string
			RawHex       string `json:"raw_hex"`
			ContentType  string `json:"content_type"`
			ExpectedJSON string `json:"expected_json"`
			Status       int
			Success      bool
			Logs         []string
		}
	}
	if e := json.Unmarshal(sourceFSOriginal44, &o); e != nil {
		t.Fatal(e)
	}
	for _, r := range o.Rows {
		t.Run(r.Name, func(t *testing.T) {
			data, e := hex.DecodeString(r.RawHex)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				calls++
				if r.ContentType != "" {
					w.Header().Set("Content-Type", r.ContentType)
				} else {
					w.Header()["Content-Type"] = nil
				}
				w.WriteHeader(r.Status)
				w.Write(data)
			}))
			defer peer.Close()
			levels := []string{}
			c := Config{FlareSolverrURL: peer.URL, HTTPClient: peer.Client()}
			value, e := c.flareSolverrValue(context.Background(), Request{URL: peer.URL + "/attendance.php", SourceTimeout: json.RawMessage(`60`), SourceLog: func(level, code string) { levels = append(levels, strings.ToLower(level)) }})
			if e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatal("original1FS HTTP", calls)
			}
			if r.Success {
				if value == nil {
					t.Fatalf("source returned solution, target absent")
				}
				fields, e := sourceFSJSONFields(value.raw)
				if e != nil {
					t.Fatal(e)
				}
				if strings.HasPrefix(r.Name, "depth-") {
					depth := 0
					for _, b := range fields["response"] {
						if b == '[' {
							depth++
						}
					}
					var want struct {
						Depth int `json:"actual_response_depth"`
					}
					if json.Unmarshal([]byte(r.ExpectedJSON), &want) != nil || depth != want.Depth {
						t.Fatalf("source depth got=%d want=%s", depth, r.ExpectedJSON)
					}
				} else {
					expected, e := sourceFSJSONFields([]byte(r.ExpectedJSON))
					if e != nil {
						t.Fatal(e)
					}
					// Compare decoded JSON structure with UseNumber; raw special constants and
					// huge integers also compare exactly before any float64 projection.
					actual := strings.TrimSpace(string(fields["response"]))
					want := strings.TrimSpace(string(expected["response"]))
					if actual != want {
						if r.Name == "value-1e999" {
							n, _ := strconv.ParseFloat(actual, 64)
							if !math.IsInf(n, 1) || want != "Infinity" {
								t.Fatalf("source overflow got=%q want=%q", actual, want)
							}
							return
						}
						var a, b any
						da := json.NewDecoder(strings.NewReader(string(sourceFSJSONMask([]byte(actual)))))
						da.UseNumber()
						db := json.NewDecoder(strings.NewReader(string(sourceFSJSONMask([]byte(want)))))
						db.UseNumber()
						if da.Decode(&a) != nil || db.Decode(&b) != nil {
							t.Fatalf("source raw differs got=%q want=%q", actual, want)
						}
						aa, _ := json.Marshal(a)
						bb, _ := json.Marshal(b)
						if string(aa) != string(bb) {
							t.Fatalf("source value differs got=%s want=%s", aa, bb)
						}
					}
				}
			} else if value != nil {
				t.Fatal("original None target solution")
			}
			if strings.Join(levels, ",") != strings.Join(r.Logs, ",") {
				t.Fatalf("source log stages differ got=%v want=%v", levels, r.Logs)
			}
		})
	}
	if len(o.Rows) != 44 {
		t.Fatal(len(o.Rows))
	}
}
