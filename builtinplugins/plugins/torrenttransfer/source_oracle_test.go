package torrenttransfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const sourceHash = "1111111111111111111111111111111111111111"
const addedHash = "2222222222222222222222222222222222222222"

type oracleHost struct {
	config, scenario map[string]any
	calls            []any
	logs             []Log
	history          map[string]any
	writes           []any
	messages         []any
	files            map[string][]byte
	added            bool
	fromType, toType string
}

func newOracleHost(config, s map[string]any) *oracleHost {
	h := &oracleHost{config: config, scenario: s, calls: []any{}, logs: []Log{}, history: map[string]any{}, writes: []any{}, messages: []any{}, files: map[string][]byte{}, fromType: "qbittorrent", toType: "qbittorrent"}
	if v, ok := s["fromtype"].(string); ok {
		h.fromType = v
	}
	if v, ok := s["totype"].(string); ok {
		h.toType = v
	}
	if !truth(s["missing_file"]) {
		b, _ := bencode(map[string]any{"announce": "https://tracker.invalid/announce", "info": map[string]any{"name": "owned synthetic", "length": newByteInteger(1)}})
		if v, ok := s["torrent_b64"].(string); ok {
			b, _ = base64.StdEncoding.DecodeString(v)
		}
		h.files["/backup/"+sourceHash+".torrent"] = b
	}
	if v, ok := s["resume_b64"].(string); ok {
		h.files["/backup/"+sourceHash+".fastresume"], _ = base64.StdEncoding.DecodeString(v)
	}
	return h
}
func (h *oracleHost) call(op string, m map[string]any) {
	if m == nil {
		m = map[string]any{}
	}
	m["op"] = op
	h.calls = append(h.calls, m)
}
func (h *oracleHost) Log(_ context.Context, l Log) { h.logs = append(h.logs, l) }
func (h *oracleHost) Service(_ context.Context, n string) (*Service, error) {
	h.call("service", map[string]any{"name": n})
	if h.scenario["missing_service"] == n {
		return nil, nil
	}
	typ := h.fromType
	if n == "destination" {
		typ = h.toType
	} else if n != "source" {
		return nil, nil
	}
	return &Service{Name: n, Ref: n, Type: typ, Inactive: h.scenario["inactive"] == n}, nil
}
func (h *oracleHost) Exists(_ context.Context, p string) (bool, error) {
	h.call("exists", map[string]any{"path": p})
	_, ok := h.files[p]
	return (p == "/backup" && !truth(h.scenario["missing_root"])) || ok, nil
}
func (h *oracleHost) ReadFile(_ context.Context, p string) ([]byte, error) {
	h.call("read", map[string]any{"path": p})
	b, ok := h.files[p]
	if !ok {
		return nil, fmt.Errorf("missing fixture file")
	}
	return append([]byte{}, b...), nil
}
func (h *oracleHost) WriteTemp(_ context.Context, n string, b []byte) (string, error) {
	h.call("temp", map[string]any{"name": n, "content": base64.StdEncoding.EncodeToString(b)})
	p := path.Join("/temp", n)
	h.files[p] = append([]byte{}, b...)
	return p, nil
}
func qbSource(hash string) Torrent {
	return Torrent{"hash": hash, "save_path": "/source/电影", "tags": "已整理, 自有", "category": "电影", "state": "pausedUP", "progress": json.Number("1"), "amount_left": json.Number("0")}
}
func trSource(hash string) Torrent {
	return Torrent{"hashString": hash, "download_dir": "/source/电影", "labels": []any{"已整理", "自有"}, "status": map[string]any{"stopped": true}, "percent_done": json.Number("1")}
}
func (h *oracleHost) original() Torrent {
	if v, ok := h.scenario["torrent"].(map[string]any); ok {
		return Torrent(v)
	}
	if h.fromType == "qbittorrent" {
		return qbSource(sourceHash)
	}
	return trSource(sourceHash)
}
func (h *oracleHost) Completed(_ context.Context, s Service) ([]Torrent, error) {
	h.call("completed", map[string]any{"name": s.Name})
	if truth(h.scenario["no_torrents"]) {
		return []Torrent{}, nil
	}
	return []Torrent{h.original()}, nil
}
func (h *oracleHost) Torrents(_ context.Context, s Service, ids []string) ([]Torrent, error) {
	h.call("torrents", map[string]any{"name": s.Name, "ids": ids})
	if !h.added {
		if truth(h.scenario["duplicate"]) {
			return []Torrent{h.original()}, nil
		}
		return []Torrent{}, nil
	}
	switch h.scenario["recheck"] {
	case "none":
		return nil, nil
	case "empty":
		return []Torrent{}, nil
	}
	t := qbSource(addedHash)
	if s.Type != "qbittorrent" {
		t = trSource(addedHash)
		if h.scenario["recheck"] == "waiting" {
			t["status"] = map[string]any{"stopped": false}
		}
	} else if h.scenario["recheck"] == "waiting" {
		t["state"] = "checkingUP"
	}
	return []Torrent{t}, nil
}
func (h *oracleHost) AddPaused(_ context.Context, s Service, spec AddSpec) (AddResult, error) {
	if truth(h.config["remainoldtag"]) {
		sort.Strings(spec.Tags)
	}
	h.call("add", map[string]any{"name": s.Name, "spec": spec})
	h.added = !truth(h.scenario["add_failure"])
	return AddResult{Accepted: h.added, SourceTruthy: s.Type == "qbittorrent" || h.added, Hash: addedHash}, nil
}
func (h *oracleHost) FindByTag(_ context.Context, s Service, tag string) (string, error) {
	h.call("find", map[string]any{"name": s.Name, "tag": tag})
	if h.added && !truth(h.scenario["find_missing"]) {
		return addedHash, nil
	}
	return "", nil
}
func (h *oracleHost) effect(op string, s Service, ids []string) (EffectResult, error) {
	h.call(op, map[string]any{"name": s.Name, "ids": ids})
	return EffectResult{Accepted: !truth(h.scenario["control_false"])}, nil
}
func (h *oracleHost) Recheck(_ context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect("recheck", s, ids)
}
func (h *oracleHost) Start(_ context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect("start", s, ids)
}
func (h *oracleHost) DeleteSource(_ context.Context, s Service, id string) (EffectResult, error) {
	return h.effect("delete", s, []string{id})
}
func (h *oracleHost) SaveData(_ context.Context, k string, v any) error {
	h.call("save", map[string]any{"key": k, "value": v})
	h.history[k] = v
	return nil
}
func (h *oracleHost) SaveConfig(_ context.Context, v map[string]any) error {
	h.call("config", map[string]any{"value": v})
	h.writes = append(h.writes, v)
	return nil
}
func (h *oracleHost) SystemMessage(_ context.Context, title, text string) error {
	h.call("system", map[string]any{"title": title, "text": text})
	return nil
}
func (h *oracleHost) Notify(_ context.Context, n Notice) error {
	v := map[string]any{"mtype": n.Type, "title": n.Title, "text": n.Text}
	h.call("notice", map[string]any{"value": v})
	h.messages = append(h.messages, v)
	return nil
}
func (h *oracleHost) RandomTag(_ context.Context, n int) (string, error) {
	h.call("random", map[string]any{"n": n})
	return strings.Repeat("r", n), nil
}
func normalJSON(v any) any {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	x, e := decode(b)
	if e != nil {
		panic(e)
	}
	return x
}
func TestInstalledWholeClassOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var root struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Name             string
			Config, Scenario map[string]any
			Expected         map[string]any
		}
		Paths []struct {
			Save     string
			From, To any
			Result   any
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e = d.Decode(&root); e != nil {
		t.Fatal(e)
	}
	if root.Source != "e5f513a57cf42ee8778744ba30cf669f93a5a3e6c7589ca0b2e3b9c483651bd5" {
		t.Fatal("source identity")
	}
	for _, tc := range root.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx := context.Background()
			h := newOracleHost(tc.Config, tc.Scenario)
			c := NewConfig()
			r, e := c.Init(ctx, tc.Config, h)
			if e != nil {
				t.Fatal(e)
			}
			core, e := NewCore(c, h)
			if e != nil {
				t.Fatal(e)
			}
			if q, ok := tc.Scenario["queue"].(map[string]any); ok {
				for k, a := range q {
					for _, v := range a.([]any) {
						core.state.Recheck[k] = append(core.state.Recheck[k], v.(string))
					}
				}
			}
			core.state.recheckRunning = truth(tc.Scenario["running"])
			if truth(tc.Scenario["stop"]) {
				core.Stop()
			}
			if !truth(tc.Scenario["init_only"]) {
				if truth(tc.Scenario["only_recheck"]) {
					e = core.CheckRecheck(ctx)
				} else {
					_, e = core.Transfer(ctx)
				}
			}
			jobs := []any{}
			for _, j := range r.Jobs {
				jobs = append(jobs, map[string]any{"id": j.ID, "kind": j.Kind, "delay": int(j.Delay.Seconds()), "interval": int(j.Interval.Seconds())})
			}
			var fault any
			if e != nil {
				fault = map[string]any{"type": "HostError", "message": e.Error()}
			}
			actual := map[string]any{"calls": h.calls, "logs": h.logs, "history": h.history, "messages": h.messages, "writes": h.writes, "queue": core.state.Recheck, "running": core.state.recheckRunning, "state": core.Config.State(), "jobs": jobs, "error": fault, "stdout": []any{}}
			// The source renders dependency exception details, so compare the exact
			// event line/level/prefix and all business state. No broad log removal.
			want := normalJSON(tc.Expected).(map[string]any)
			got := normalJSON(actual).(map[string]any)
			for _, m := range []map[string]any{want, got} {
				for _, a := range m["logs"].([]any) {
					l := a.(map[string]any)
					if l["line"] == json.Number("829") || l["line"] == json.Number("861") {
						msg := l["message"].(string)
						if i := strings.Index(msg, "："); i >= 0 {
							l["message"] = msg[:i+len("：")] + "<dependency-exception>"
						}
					}
				}
			}
			if !reflect.DeepEqual(want, got) {
				wb, _ := json.MarshalIndent(want, "", " ")
				gb, _ := json.MarshalIndent(got, "", " ")
				t.Fatalf("source mismatch\nwant%s\ngot%s", wb, gb)
			}
		})
	}
	for i, tc := range root.Paths {
		a, _ := tc.From.(string)
		b, _ := tc.To.(string)
		v := ConvertSavePath(tc.Save, a, b)
		var got any
		if v != nil {
			got = *v
		}
		if !reflect.DeepEqual(tc.Result, got) {
			t.Errorf("path%d want%v got%v", i, tc.Result, got)
		}
	}
}
