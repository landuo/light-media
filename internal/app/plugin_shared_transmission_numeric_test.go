package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"light-media/internal/plugins"
)

//go:embed testdata/source-tr-numeric.json
var appTRNumericSource []byte

// Real App fixed factory -> native HTTP client. Only owned loopback fixed
// session-get/torrent-set replies; no daemon, download, worker or real media.
func TestOriginalTRNumericNativeSourceCases(t *testing.T) {
	hash := sha256.Sum256(appTRNumericSource)
	if hex.EncodeToString(hash[:]) != "1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d" { t.Fatal("original raw changed") }
	var oracle struct {
		Cases []struct {
			ID string `json:"id"`
			Input struct { Args map[string]any `json:"args"` } `json:"input"`
			Outcome struct { Kind string `json:"kind"`; Class string `json:"class"` } `json:"outcome"`
			Trace []struct { Event string `json:"event"`; Kwargs map[string]any `json:"kwargs"` } `json:"trace"`
		} `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(appTRNumericSource)); d.UseNumber()
	if d.Decode(&oracle) != nil || d.Decode(new(any)) != io.EOF || len(oracle.Cases) != 14 { t.Fatal("original cases missing") }
	a, scope, _ := ownedDownloaderCommandApp(t)
	ctx := context.Background()
	var mu sync.Mutex
	var methods []string
	var selected []map[string]any
	// Existing TR client always sends Basic auth; this owned config has empty
	// username/password, whose exact header is Basic Og==. Not arbitrary auth.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Basic Og==" || r.Header.Get("Proxy-Authorization") != "" { t.Error("unexpected owned request"); w.WriteHeader(400); return }
		var packet struct { Method string `json:"method"`; Arguments map[string]any `json:"arguments"` }
		d := json.NewDecoder(io.LimitReader(r.Body,4097)); d.UseNumber(); d.DisallowUnknownFields()
		if d.Decode(&packet) != nil || d.Decode(new(any)) != io.EOF { t.Error("invalid fixed frame"); w.WriteHeader(400); return }
		mu.Lock(); methods = append(methods,packet.Method)
		if packet.Method == "torrent-set" { selected = append(selected,packet.Arguments) }
		mu.Unlock()
		switch packet.Method {
		case "session-get":
			if len(packet.Arguments) != 0 { t.Error("session arguments changed") }
			_, _ = io.WriteString(w,`{"result":"success","arguments":{"rpc-version":15}}`)
		case "torrent-set":
			_, _ = io.WriteString(w,`{"result":"success","arguments":{}}`)
		default: t.Error("unselected native method",packet.Method); w.WriteHeader(400)
		}
	})); defer peer.Close()
	a.cfgMu.Lock(); a.config.Downloaders[1].BaseURL = peer.URL+"/transmission/rpc"; a.cfgMu.Unlock()
	// The invalid original case is between successes, checking prewrite failure
	// and recovery without a platform/source-error classification downgrade.
	for _, index := range []int{0,3,1,2} {
		c := oracle.Cases[index]
		t.Run(c.ID,func(t *testing.T){
			packet := map[string]any{"op":"invoke","downloader_id":"owned-tr","hash_string":strings.Repeat("a",40)}
			for _, field := range []string{"upload_limit","download_limit","ratio_limit","seeding_time_limit"} { packet[field] = c.Input.Args[field] }
			mu.Lock(); before, selectedBefore := len(methods),len(selected); mu.Unlock()
			out, err := a.originalSharedTRFinalResource(ctx,scope,plugins.TransmissionChangeCapability,ownedCommandPacket(t,packet))
			if err != nil { t.Fatal("fixed native App path failed",err) }
			mu.Lock(); defer mu.Unlock()
			if c.Outcome.Kind == "uncaught_exception" {
				if c.Outcome.Class != "ValueError" || string(out) != `{"kind":"source_error","source_error":"ValueError"}` || len(methods) != before || len(selected) != selectedBefore { t.Fatal("source failure reached HTTP or lost classification",string(out),methods) }
				return
			}
			if c.Outcome.Kind != "return" || string(out) != `{"value":true}` || len(methods) != before+2 || len(selected) != selectedBefore+1 || !reflect.DeepEqual(methods[before:],[]string{"session-get","torrent-set"}) { t.Fatal("extra/replayed native request or false success",string(out),methods) }
			var want map[string]any
			for _, event := range c.Trace { if event.Event == "recording_client_call" { if want != nil { t.Fatal("multiple source calls") }; want = event.Kwargs } }
			if want == nil || want["ids"] != "owned-numeric" { t.Fatal("source kwargs missing") }
			// Only fixture selector is adapted. Numeric fields and JSON number
			// tokens (notably 125.0) remain from the complete source raw.
			want["ids"] = []any{strings.Repeat("a",40)}
			if !reflect.DeepEqual(selected[selectedBefore],want) { t.Fatal("native wire differs from real source types/values",selected[selectedBefore],want) }
		})
	}
	mu.Lock(); defer mu.Unlock()
	if len(methods) != 6 || len(selected) != 3 { t.Fatal("finite HTTP totals",methods) }
}
