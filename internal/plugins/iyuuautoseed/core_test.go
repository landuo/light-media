package iyuuautoseed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
)

type businessHost struct {
	t        *testing.T
	scenario map[string]any
	kind     string
	calls    []any
	writes   []any
	messages []any
	history  map[string]any
}

func (h *businessHost) record(m map[string]any) { h.calls = append(h.calls, m) }
func (h *businessHost) Inventory(context.Context) ([]Service, error) {
	v := []Service{{Ref: "source-ref", Name: "source", Kind: h.kind, Inactive: truth(h.scenario["inactive"])}}
	if truth(h.scenario["with_auto"]) {
		v = append(v, Service{Ref: "destination-ref", Name: "destination", Kind: h.kind, Inactive: truth(h.scenario["inactive"])})
	}
	return v, nil
}
func defaultNative() map[string]any {
	return map[string]any{"hash": "original", "hashString": "original", "save_path": "/data/source", "download_dir": "/data/source", "tags": "original", "labels": []any{"original"}, "total_size": json.Number("1073741824"), "category": "original-category", "state": "pausedUP", "status_stopped": true, "percent_done": json.Number("1")}
}
func nativeRows(raw any) []NativeTorrent {
	if raw == nil {
		return nil
	}
	out := []NativeTorrent{}
	for _, v := range raw.([]any) {
		out = append(out, NativeTorrent{Fields: v.(map[string]any)})
	}
	return out
}
func (h *businessHost) Completed(_ context.Context, s Service) ([]NativeTorrent, error) {
	h.record(map[string]any{"op": "completed", "service": "source"})
	if raw, ok := h.scenario["completed"]; ok {
		return nativeRows(raw), nil
	}
	return []NativeTorrent{{Fields: defaultNative()}}, nil
}
func (h *businessHost) Torrents(_ context.Context, s Service, ids []string) ([]NativeTorrent, error) {
	h.record(map[string]any{"op": "torrents", "service": s.Name, "ids": ids})
	if len(ids) == 1 && ids[0] == "seed-hash" {
		if truth(h.scenario["exists"]) {
			return []NativeTorrent{{Fields: defaultNative()}}, nil
		}
		return []NativeTorrent{}, nil
	}
	if rows, ok := h.scenario["recheck"]; ok {
		return nativeRows(rows), nil
	}
	row := defaultNative()
	row["hash"] = "added-hash"
	row["hashString"] = "added-hash"
	return []NativeTorrent{{Fields: row}}, nil
}
func (h *businessHost) AddPaused(_ context.Context, s Service, spec AddSpec) (AddReceipt, error) {
	args := map[string]any{"content": base64.StdEncoding.EncodeToString(spec.Content), "download_dir": spec.SavePath, "is_paused": spec.Paused}
	if spec.Magnet != "" {
		args["content"] = spec.Magnet
	}
	if h.kind == "qbittorrent" {
		args["tag"] = spec.Tags
		args["category"] = spec.Category
		args["is_skip_checking"] = spec.SkipChecking
		args["ignore_category_check"] = spec.IgnoreCategoryCheck
	} else {
		args["labels"] = spec.Tags
	}
	h.record(map[string]any{"op": "add", "service": s.Name, "args": args})
	accepted := true
	if v, ok := h.scenario["add_ok"]; ok {
		accepted = truth(v)
	}
	sourceTruthy := accepted
	if h.kind == "qbittorrent" {
		sourceTruthy = !truth(h.scenario["bare_false"])
	}
	outcome := "confirmed"
	if !accepted || truth(h.scenario["bare_false"]) {
		outcome = "rejected"
	}
	return AddReceipt{SourceTruthy: sourceTruthy, Accepted: accepted, Hash: "added-hash", Outcome: outcome, ReceiptID: "synthetic-add"}, nil
}
func (h *businessHost) FindByTag(_ context.Context, s Service, tag string) (string, error) {
	h.record(map[string]any{"op": "find_tag", "service": s.Name, "tag": tag})
	if v, ok := h.scenario["found_hash"]; ok {
		if v == nil {
			return "", nil
		}
		return v.(string), nil
	}
	return "added-hash", nil
}
func (h *businessHost) receipt(key string) EffectReceipt {
	accepted := true
	if x, ok := h.scenario[key]; ok {
		accepted = truth(x)
	}
	outcome := "confirmed"
	if !accepted {
		outcome = "rejected"
	}
	return EffectReceipt{Accepted: accepted, Outcome: outcome, ReceiptID: "synthetic-" + key}
}
func (h *businessHost) SetCategory(_ context.Context, s Service, id string, c any) (EffectReceipt, error) {
	h.record(map[string]any{"op": "category", "service": s.Name, "hash_string": id, "category": c})
	return h.receipt("category_ok"), nil
}
func (h *businessHost) Recheck(_ context.Context, s Service, ids []string) (EffectReceipt, error) {
	h.record(map[string]any{"op": "recheck", "service": s.Name, "ids": ids})
	return h.receipt("recheck_ok"), nil
}
func (h *businessHost) Start(_ context.Context, s Service, ids []string) (EffectReceipt, error) {
	h.record(map[string]any{"op": "start", "service": s.Name, "ids": ids})
	return h.receipt("start_ok"), nil
}
func (h *businessHost) Indexer(_ context.Context, domain string) (*Site, error) {
	h.record(map[string]any{"op": "indexer", "domain": domain})
	if truth(h.scenario["missing_site"]) {
		return nil, nil
	}
	fields := map[string]any{"id": json.Number("1"), "name": "synthetic-site", "url": "https://source.invalid/", "passkey": "synthetic-passkey", "uid": "12", "cookie": "synthetic-cookie", "ua": "synthetic-UA", "proxy": false}
	if values, ok := h.scenario["site"].(map[string]any); ok {
		for k, v := range values {
			fields[k] = v
		}
	}
	return &Site{Ref: "site-ref", Fields: fields}, nil
}
func (h *businessHost) CheckSite(ctx context.Context, domain string) (bool, error) {
	h.record(map[string]any{"op": "check_site", "domain": domain})
	limited := truth(h.scenario["limited"])
	if limited {
		sourceLog(ctx, 1132, "synthetic-limit")
	}
	return limited, nil
}
func (h *businessHost) DownloadURL(_ context.Context, seed map[string]any, site Site, template string) (string, error) {
	return site.Fields["url"].(string) + strings.ReplaceAll(template, "{id}", pyString(seed["torrent_id"])), nil
}
func (h *businessHost) Torrent(_ context.Context, site Site, url string) (TorrentContent, error) {
	h.record(map[string]any{"op": "torrent_content", "args": map[string]any{"url": url, "cookie": site.Fields["cookie"], "ua": site.Fields["ua"], "proxy": site.Fields["proxy"]}})
	if truth(h.scenario["content_error"]) {
		return TorrentContent{ErrorMessage: h.scenario["content_error"].(string)}, nil
	}
	return TorrentContent{Bytes: []byte("synthetic-torrent")}, nil
}
func (h *businessHost) LoadHistory(_ context.Context, key string) (any, error) {
	return cloneJSON(h.history[key])
}
func (h *businessHost) SaveHistory(_ context.Context, key string, v any) error {
	cp, e := cloneJSON(v)
	if e != nil {
		return e
	}
	h.history[key] = cp
	return nil
}
func (h *businessHost) SaveConfig(_ context.Context, v map[string]any) error {
	cp, e := cloneJSON(v)
	if e != nil {
		return e
	}
	h.writes = append(h.writes, cp)
	return nil
}
func (h *businessHost) Notify(_ context.Context, title, text string) error {
	h.messages = append(h.messages, map[string]any{"mtype": "SiteMessage", "title": title, "text": text})
	return nil
}
func (h *businessHost) RandomTag(context.Context, int) (string, error) { return "0123456789", nil }

type businessAPI struct{ h *businessHost }

func (a businessAPI) SeedInfo(_ context.Context, hashes []string) (APIResult, error) {
	sort.Strings(hashes)
	a.h.record(map[string]any{"op": "seed_info", "hashes": append([]string{}, hashes...)})
	if x, ok := a.h.scenario["seed_result"]; ok {
		r := APIResult{Data: x}
		if message, ok := a.h.scenario["seed_message"].(string); ok {
			r.Failure = &Failure{sourceMessage: message}
		}
		if m, ok := x.(map[string]any); ok {
			for k := range m {
				r.ObjectKeys = append(r.ObjectKeys, k)
			}
		}
		return r, nil
	}
	seeds, ok := a.h.scenario["seeds"]
	if !ok {
		seeds = []any{map[string]any{"sid": json.Number("1"), "torrent_id": json.Number("7"), "info_hash": "seed-hash"}}
	}
	return APIResult{Data: map[string]any{hashes[0]: map[string]any{"torrent": seeds}}, ObjectKeys: []string{hashes[0]}}, nil
}
func (a businessAPI) TorrentURL(_ context.Context, sid any) (any, any, error) {
	a.h.record(map[string]any{"op": "torrent_url", "sid": sid})
	if truth(a.h.scenario["missing_template"]) {
		return nil, nil, nil
	}
	return "https://source.invalid/", "download.php?id={id}", nil
}

func normalizeHistory(v map[string]any) {
	for _, raw := range v {
		rows, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			values, ok := m["torrents"].([]any)
			if !ok {
				continue
			}
			sort.SliceStable(values, func(i, j int) bool { return pyString(values[i]) < pyString(values[j]) })
		}
	}
}
func TestInstalledBusinessOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/business-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	doc, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	cases := doc.(map[string]any)["cases"].([]any)
	if len(cases) != 45 {
		t.Fatalf("case count %d", len(cases))
	}
	for _, raw := range cases {
		item := raw.(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			scenario := item["scenario"].(map[string]any)
			h := &businessHost{t: t, scenario: scenario, kind: "qbittorrent", calls: []any{}, writes: []any{}, messages: []any{}, history: map[string]any{}}
			if s, ok := scenario["kind"].(string); ok {
				h.kind = s
			}
			if history, ok := scenario["history"].(map[string]any); ok {
				h.history, _ = cloneMap(history)
			}
			config := classConfig()
			if _, e := config.Init(item["config"].(map[string]any), []any{json.Number("1"), json.Number("2"), "1"}); e != nil {
				t.Fatal(e)
			}
			core, e := NewCore(config, h, businessAPI{h})
			if e != nil {
				t.Fatal(e)
			}
			if q, ok := scenario["initial_queue"].(map[string]any); ok {
				for k, v := range q {
					for _, s := range v.([]any) {
						core.queue[k] = append(core.queue[k], s.(string))
					}
				}
			}
			if truth(scenario["only_recheck"]) {
				e = core.CheckRecheck(context.Background())
			} else {
				e = core.AutoSeed(context.Background())
			}
			var errClass any
			if e != nil {
				var fault *SourceFault
				if !errors.As(e, &fault) {
					t.Fatal(e)
				}
				errClass = fault.Class
			}
			state, e := core.Snapshot()
			if e != nil {
				t.Fatal(e)
			}
			want := item["result"].(map[string]any)
			normalizeHistory(h.history)
			normalizeHistory(want["history"].(map[string]any))
			got := map[string]any{"calls": h.calls, "queue": state.Queue, "recheck_running": state.RecheckRunning, "counts": state.Counters, "writes": h.writes, "messages": h.messages, "history": h.history, "error": errClass}
			assertJSON(t, got, want)
			if item["name"] == "qb-false-tuple-still-polls" {
				if len(state.Attempts) == 0 || state.Attempts[0].Accepted || state.Attempts[0].Outcome != "rejected" || state.Counters.Success != 1 {
					t.Fatal("source-attempt/receipt distinction lost")
				}
			}
		})
	}
}
func TestCoreIsolationStopAndUnknown(t *testing.T) {
	h := &businessHost{t: t, scenario: map[string]any{}, kind: "qbittorrent", history: map[string]any{}}
	cfg := classConfig()
	_, _ = cfg.Init(map[string]any{"downloaders": []any{"source"}}, nil)
	c, e := NewCore(cfg, h, businessAPI{h})
	if e != nil {
		t.Fatal(e)
	}
	s, _ := c.Snapshot()
	s.Config["downloaders"].([]any)[0] = "changed"
	s.Queue["foreign"] = []string{"x"}
	again, _ := c.Snapshot()
	if again.Config["downloaders"].([]any)[0] != "source" || len(again.Queue) != 0 {
		t.Fatal("snapshot aliases state")
	}
	e = c.record("add", Service{Ref: "source-ref"}, "x", EffectReceipt{Outcome: "unknown"})
	if e == nil {
		t.Fatal("unknown effect accepted")
	}
	c.Stop()
	if e = c.AutoSeed(context.Background()); e == nil {
		t.Fatal("stopped owner ran")
	}
	if len(h.calls) != 0 {
		t.Fatal("native IO after stop")
	}
}

func TestInstalledBusinessLogOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/business-log-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	doc, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	cases := doc.(map[string]any)["cases"].([]any)
	if len(cases) != 50 {
		t.Fatalf("case count %d", len(cases))
	}
	for _, raw := range cases {
		item := raw.(map[string]any)
		t.Run(item["name"].(string), func(t *testing.T) {
			logs := []any{}
			ctx := WithSourceLogSink(context.Background(), func(_ context.Context, event SourceLog) {
				logs = append(logs, map[string]any{"line": event.Line, "level": event.Level, "message": event.Message})
			})
			scenario := item["scenario"].(map[string]any)
			h := &businessHost{t: t, scenario: scenario, kind: "qbittorrent", calls: []any{}, writes: []any{}, messages: []any{}, history: map[string]any{}}
			if s, ok := scenario["kind"].(string); ok {
				h.kind = s
			}
			if history, ok := scenario["history"].(map[string]any); ok {
				h.history, _ = cloneMap(history)
			}
			config := classConfig()
			if _, e := config.InitContext(ctx, item["config"].(map[string]any), []any{json.Number("1"), json.Number("2"), "1"}); e != nil {
				t.Fatal(e)
			}
			core, e := NewCore(config, h, businessAPI{h})
			if e != nil {
				t.Fatal(e)
			}
			if q, ok := scenario["initial_queue"].(map[string]any); ok {
				for k, v := range q {
					for _, s := range v.([]any) {
						core.queue[k] = append(core.queue[k], s.(string))
					}
				}
			}
			if truth(scenario["only_recheck"]) {
				e = core.CheckRecheck(ctx)
			} else {
				e = core.AutoSeed(ctx)
			}
			var errClass any
			if e != nil {
				var fault *SourceFault
				if !errors.As(e, &fault) {
					t.Fatal(e)
				}
				errClass = fault.Class
			}
			state, e := core.Snapshot()
			if e != nil {
				t.Fatal(e)
			}
			want := item["result"].(map[string]any)
			normalizeHistory(h.history)
			normalizeHistory(want["history"].(map[string]any))
			got := map[string]any{"calls": h.calls, "queue": state.Queue, "recheck_running": state.RecheckRunning, "counts": state.Counters, "writes": h.writes, "messages": h.messages, "history": h.history, "error": errClass, "logs": logs}
			assertJSON(t, got, want)
			if item["name"] == "qb-false-tuple-still-polls" {
				if len(state.Attempts) == 0 || state.Attempts[0].Accepted || state.Attempts[0].Outcome != "rejected" || state.Counters.Success != 1 {
					t.Fatal("source-attempt/receipt distinction lost")
				}
			}
		})
	}
}
