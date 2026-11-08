package browserrender

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed testdata/source_fs_token_original7.json
var sourceTokenOriginal7 []byte

func TestIndependentExistingFSNonfiniteTokenBoundaries(t *testing.T) {
	data := sourceTokenOriginal7
	var e error
	var oracle struct {
		Rows []struct {
			Token   string
			RawHex  string `json:"raw_hex"`
			Private struct {
				IsNone bool `json:"is_none"`
				Logs   []struct{ Level string }
			}
		}
	}
	if e = json.Unmarshal(data, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, r := range oracle.Rows {
		t.Run(r.Token, func(t *testing.T) {
			body, e := hex.DecodeString(r.RawHex)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.Write(body)
			}))
			defer peer.Close()
			levels := []string{}
			c := Config{FlareSolverrURL: peer.URL, HTTPClient: peer.Client()}
			got, e := c.flareSolverrValue(context.Background(), Request{URL: peer.URL + "/attendance.php", SourceTimeout: json.RawMessage(`60`), SourceLog: func(level, code string) { levels = append(levels, strings.ToLower(level)) }})
			want := []string{}
			for _, l := range r.Private.Logs {
				want = append(want, l.Level)
			}
			if e != nil || (got == nil) != r.Private.IsNone || strings.Join(levels, ",") != strings.Join(want, ",") {
				t.Fatalf("actual original private FS JSON semantics gotNone=%v err=%v logs=%v sourceNone=%v logs=%v", got == nil, e, levels, r.Private.IsNone, want)
			}
			if calls != 1 {
				t.Fatal("original one FS POST", calls)
			}
		})
	}
	if len(oracle.Rows) != 7 {
		t.Fatal(len(oracle.Rows))
	}
}
