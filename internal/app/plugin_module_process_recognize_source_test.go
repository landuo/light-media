package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeOwnedEventsProcessEvidence(t *testing.T, name string, body []byte) {
	t.Helper()
	dir := os.Getenv("PLUGIN_PROCESS_EVENTS36_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if filepath.Base(name) != name || os.MkdirAll(dir, 0700) != nil || os.WriteFile(filepath.Join(dir, name), body, 0600) != nil {
		t.Error("save owned process evidence", name)
	}
}

func copyOwnedEventsProcessEvidence(t *testing.T, name, path string) {
	t.Helper()
	if os.Getenv("PLUGIN_PROCESS_EVENTS36_EVIDENCE_DIR") == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Error("read owned process evidence", name, err)
		return
	}
	writeOwnedEventsProcessEvidence(t, name, body)
}

// Expectations come from the fixed SubscribeChain build_subscribe_meta and
// search recognition call replay. Only the owned subscription's input columns
// replace the oracle record; parsed MetaInfo extras remain visible and intact.
func requireOwnedSubscriptionRecognizeSource(t *testing.T, rows []orderObservation, title, year, kind string, id int64, season any) {
	t.Helper()
	path := os.Getenv("PLUGIN_PROCESS_SYNC_RECOGNIZE_ORACLE")
	if path == "" {
		path = "../../testdata/standard-module-host/recognize-source-oracle.json"
	}
	raw, err := os.ReadFile(path)
	var oracle struct {
		Caller struct {
			Method string
			Kwargs map[string]json.RawMessage
		} `json:"sync_subscription_caller"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.Caller.Method != "recognize_media" || len(oracle.Caller.Kwargs) != 10 {
		t.Fatal("fixed synchronous subscription recognition oracle", err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(oracle.Caller.Kwargs["meta"], &fields) != nil || len(fields) != 10 {
		t.Fatal("fixed build_subscribe_meta assigned fields")
	}
	wantMeta := map[string]any{"org_string": title, "year": year, "begin_season": season, "type": kind, "tmdbid": id, "doubanid": nil, "bangumiid": nil, "anilistid": nil, "media_source": "themoviedb", "media_id": strconv.FormatInt(id, 10)}
	if year == "" {
		wantMeta["year"] = nil
	}
	seen := 0
	for _, row := range rows {
		if row.Method != oracle.Caller.Method {
			continue
		}
		var input struct {
			Args   []json.RawMessage
			Kwargs map[string]json.RawMessage
		}
		if row.PID <= 0 || json.Unmarshal(row.Input, &input) != nil || input.Args == nil || len(input.Args) != 0 {
			t.Fatal("actual synchronous recognition SDK envelope")
		}
		var meta map[string]json.RawMessage
		if json.Unmarshal(input.Kwargs["meta"], &meta) != nil || len(meta) <= len(fields) {
			t.Fatal("actual synchronous recognition omitted full MetaInfo")
		}
		for key := range fields {
			want, _ := json.Marshal(wantMeta[key])
			if !originalBaseJSONEqual(meta[key], want) {
				t.Fatal("actual source subscription MetaInfo field", key, string(meta[key]), string(want))
			}
		}
		kwargs := map[string]json.RawMessage{}
		for key, value := range oracle.Caller.Kwargs {
			kwargs[key] = value
		}
		kwargs["meta"] = input.Kwargs["meta"]
		kwargs["mtype"], _ = json.Marshal(kind)
		kwargs["tmdbid"] = json.RawMessage(strconv.FormatInt(id, 10))
		kwargs["mediaid"], _ = json.Marshal(strconv.FormatInt(id, 10))
		kwargs["episode_group"] = json.RawMessage(`null`)
		want, _ := json.Marshal(kwargs)
		got, _ := json.Marshal(input.Kwargs)
		if !originalBaseJSONEqual(got, want) {
			t.Fatal("actual fixed synchronous subscription recognition kwargs", string(got))
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("actual synchronous subscription recognition not observed")
	}
}
