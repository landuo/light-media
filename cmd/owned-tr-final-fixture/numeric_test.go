package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

//go:embed testdata/source-tr-numeric.json
var ownedNumericSource []byte

// This small gate validates only the extended fixed fixture and real SDK route.
// Native HTTP and normal installed-worker evidence are separate gates.
func TestOwnedTRNumericFixedSourceInputs(t *testing.T) {
	hash := sha256.Sum256(ownedNumericSource)
	if hex.EncodeToString(hash[:]) != "1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d" {
		t.Fatal("original raw changed")
	}
	var raw struct {
		Cases []struct {
			ID string `json:"id"`
			Input struct { Args map[string]any `json:"args"` } `json:"input"`
			Outcome struct { Kind string `json:"kind"`; Value bool `json:"value"`; Class string `json:"class"` } `json:"outcome"`
		} `json:"cases"`
	}
	if json.Unmarshal(ownedNumericSource, &raw) != nil || len(raw.Cases) != 14 {
		t.Fatal("original cases missing")
	}
	manifest := fixtureManifest()
	if err := manifest.Validate(); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(manifest.Capabilities, []string{plugins.TransmissionSetTagsCapability, plugins.TransmissionChangeCapability}) {
		t.Fatal("fixture gained permission")
	}
	for _, c := range raw.Cases[:4] {
		t.Run(c.ID, func(t *testing.T) {
			x := c.Input.Args
			operation := map[string]any{"kind":"transmission", "op":"change", "downloader_id":fixtureTRID, "hash_string":strings.Repeat("a",40)}
			for _, field := range []string{"upload_limit","download_limit","ratio_limit","seeding_time_limit"} { operation[field] = x[field] }
			body, err := json.Marshal(map[string]any{"operations":[]any{operation}})
			if err != nil { t.Fatal(err) }
			request, err := json.Marshal(plugins.HTTPRequest{Key:"tr_final", Method:"POST", Path:"/tr-final", Body:body})
			if err != nil { t.Fatal(err) }
			object := &fixtureObject{}
			handler := object.handle(manifest)
			if _, err := handler(context.Background(),"init",json.RawMessage(`{}`),nil); err != nil { t.Fatal(err) }
			calls := 0
			out, err := handler(context.Background(),"http",request,func(_ context.Context,capability string,packet json.RawMessage)(json.RawMessage,error){
				calls++
				var actual map[string]any
				if capability != plugins.TransmissionChangeCapability || json.Unmarshal(packet,&actual) != nil { t.Fatal("SDK capability/JSON") }
				want := map[string]any{"op":"invoke", "downloader_id":fixtureTRID, "hash_string":strings.Repeat("a",40)}
				for _, field := range []string{"upload_limit","download_limit","ratio_limit","seeding_time_limit"} { want[field] = x[field] }
				if !reflect.DeepEqual(actual,want) { t.Fatal("original numeric strings changed",actual,want) }
				if c.Outcome.Kind == "return" { return json.Marshal(map[string]any{"value":c.Outcome.Value}) }
				if c.Outcome.Kind != "uncaught_exception" || c.Outcome.Class != "ValueError" { t.Fatal("unexpected source outcome") }
				return json.RawMessage(`{"kind":"source_error","source_error":"ValueError"}`),nil
			})
			var response plugins.HTTPResponse
			var snapshot fixtureSnapshot
			if err != nil || calls != 1 || json.Unmarshal(out,&response) != nil || response.Status != 200 || json.Unmarshal(response.Body,&snapshot) != nil { t.Fatal("worker route",err,string(out)) }
			if len(snapshot.Results) != 1 || snapshot.Completed != 1 || snapshot.Invocation != 1 || snapshot.InitCount != 1 { t.Fatal("worker counters",snapshot) }
			attempt := snapshot.Results[0]
			if attempt.HostCalls.Total != 1 || attempt.HostCalls.Succeeded != 1 || attempt.HostCalls.Failed != 0 { t.Fatal("logical callback count",attempt) }
			if c.Outcome.Kind == "return" {
				if !snapshot.OK || !attempt.OK || string(attempt.Result) != "true" { t.Fatal("source true lost",snapshot) }
			} else if snapshot.OK || attempt.OK || attempt.SourceError != "ValueError" || attempt.ErrorCode != "source_exception" || attempt.Result != nil { t.Fatal("source exception lost",snapshot) }
			operation["upload_limit"] = "1_001" // Not one of the fixed fixture values.
			bad, err := json.Marshal(operation)
			if err != nil { t.Fatal(err) }
			if _, err := decodeOperation(bad); err == nil { t.Fatal("fixture allowlist expanded beyond selected values") }
		})
	}
}
