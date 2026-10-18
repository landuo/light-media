package brushflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryHost struct {
	mu                        sync.Mutex
	now                       time.Time
	seq                       int
	kv                        map[string]json.RawMessage
	config                    Row
	sites                     []Site
	downloaders               []Downloader
	users                     []UserData
	torrents                  map[string][]DownloadState
	candidates                []Row
	titles                    []string
	requests                  []Row
	pages                     map[string]Row
	actions                   []Row
	notices                   []Notice
	events                    []Event
	queued                    []Row
	downloadHash              string
	deleteResult, pauseResult bool
	deleteErr, pauseErr       error
}

func testHost() *memoryHost {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return &memoryHost{now: time.Date(2026, 10, 18, 12, 0, 0, 0, loc), kv: map[string]json.RawMessage{}, sites: []Site{{ID: 1, Name: "S", Domain: "https://pt.example", URL: "https://pt.example", Schema: "NexusPHP", Raw: Row{}}}, downloaders: []Downloader{{Name: "qb", Kind: "qbittorrent", Active: true}, {Name: "tr", Kind: "transmission", Active: true}}, torrents: map[string][]DownloadState{}, pages: map[string]Row{}, deleteResult: true, pauseResult: true}
}
func (h *memoryHost) Now() time.Time                                   { return h.now }
func (h *memoryHost) NewID() string                                    { h.seq++; return fmt.Sprintf("owned%027d", h.seq) }
func (h *memoryHost) RandomTag() string                                { return "ownedtag01" }
func (h *memoryHost) Sleep(ctx context.Context, d time.Duration) error { return ctx.Err() }
func (h *memoryHost) Get(_ context.Context, k string) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.kv[k]
	if len(b) == 0 {
		return nil, nil
	}
	if b[0] == '{' {
		return append(json.RawMessage{}, b...), nil
	}
	var v any
	e := json.Unmarshal(b, &v)
	return v, e
}
func (h *memoryHost) Put(_ context.Context, k string, v any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, e := json.Marshal(v)
	if e == nil {
		h.kv[k] = b
	}
	return e
}
func (h *memoryHost) Del(_ context.Context, k string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.kv, k)
	return nil
}
func (h *memoryHost) SaveConfig(_ context.Context, v Row) error         { h.config = v; return nil }
func (h *memoryHost) RefreshSchedules(context.Context, []Service) error { return nil }
func (h *memoryHost) Sites(context.Context) ([]Site, error)             { return h.sites, nil }
func (h *memoryHost) Downloaders(context.Context) ([]Downloader, error) { return h.downloaders, nil }
func (h *memoryHost) SiteUsers(context.Context) ([]UserData, error)     { return h.users, nil }
func (h *memoryHost) Torrents(_ context.Context, name string) ([]DownloadState, error) {
	return h.torrents[name], nil
}
func (h *memoryHost) Downloading(_ context.Context, name, tag string) ([]DownloadState, error) {
	r := []DownloadState{}
	for _, d := range h.torrents[name] {
		if contains(torrentLabels(d), tag) && !complete((&Core{h: h}).torrentInfo(d)) {
			r = append(r, d)
		}
	}
	return r, nil
}
func (h *memoryHost) Completed(_ context.Context, name string, tags []string) ([]DownloadState, error) {
	r := []DownloadState{}
	for _, d := range h.torrents[name] {
		match := true
		for _, tag := range tags {
			if !contains(torrentLabels(d), tag) {
				match = false
			}
		}
		if match && complete((&Core{h: h}).torrentInfo(d)) {
			r = append(r, d)
		}
	}
	return r, nil
}
func (h *memoryHost) TransferInfo(context.Context) ([]Transfer, error) { return []Transfer{{}}, nil }
func (h *memoryHost) Browse(context.Context, int, bool) ([]Row, error) {
	return append([]Row{}, h.candidates...), nil
}
func (h *memoryHost) SubscribeTitles(context.Context) ([]string, error) { return h.titles, nil }
func (h *memoryHost) SiteRequest(_ context.Context, id int, method, u string, data Row) (Row, error) {
	h.requests = append(h.requests, Row{"site": id, "method": method, "url": u, "data": data})
	return h.pages[u], nil
}
func (h *memoryHost) Download(_ context.Context, t Task, v Row, _ Row, tag string) (string, error) {
	h.actions = append(h.actions, Row{"op": "download", "task": t.ID(), "candidate": v, "tag": tag})
	return h.downloadHash, nil
}
func (h *memoryHost) Delete(_ context.Context, name string, hashes []string, files bool) (bool, error) {
	h.actions = append(h.actions, Row{"op": "delete", "downloader": name, "hashes": hashes, "files": files})
	if h.deleteResult && h.deleteErr == nil {
		left := []DownloadState{}
		for _, d := range h.torrents[name] {
			if !contains(hashes, torrentHash(d)) {
				left = append(left, d)
			}
		}
		h.torrents[name] = left
	}
	return h.deleteResult, h.deleteErr
}
func (h *memoryHost) Pause(_ context.Context, name string, hashes []string) (bool, error) {
	h.actions = append(h.actions, Row{"op": "pause", "downloader": name, "hashes": hashes})
	return h.pauseResult, h.pauseErr
}
func (h *memoryHost) Reannounce(_ context.Context, name string, hashes []string) error {
	h.actions = append(h.actions, Row{"op": "reannounce", "downloader": name, "hashes": hashes})
	return nil
}
func (h *memoryHost) Tags(context.Context, string) ([]string, error) { return []string{}, nil }
func (h *memoryHost) DeleteTags(_ context.Context, name string, tags []string) (bool, error) {
	h.actions = append(h.actions, Row{"op": "delete_tags", "downloader": name, "tags": tags})
	return true, nil
}
func (h *memoryHost) Queue(_ context.Context, id, op string) error {
	h.queued = append(h.queued, Row{"task_id": id, "op": op})
	return nil
}
func (h *memoryHost) Message(_ context.Context, n Notice) error {
	h.notices = append(h.notices, n)
	return nil
}
func (h *memoryHost) Event(_ context.Context, e Event) error {
	h.events = append(h.events, e)
	return nil
}
func (h *memoryHost) Log(context.Context, string, string) {}
func equalJSON(a, b any) bool {
	aa, e := json.Marshal(a)
	if e != nil {
		return false
	}
	bb, e := json.Marshal(b)
	if e != nil {
		return false
	}
	var x, y any
	json.Unmarshal(aa, &x)
	json.Unmarshal(bb, &y)
	return reflect.DeepEqual(x, y)
}
func TestActualSourceASTBranches(t *testing.T) {
	raw, e := os.ReadFile("reference/port-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Cases []struct {
			Method   string
			Args     Row
			Expected Row
		}
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	for i, c := range data.Cases {
		t.Run(fmt.Sprintf("%03d_%s", i, c.Method), func(t *testing.T) {
			h := testHost()
			p := newCore(h, Config{Settings: Row{}})
			var got any
			var err error
			switch c.Method {
			case "task_config":
				got = NormalizeTask(c.Args, h.NewID).Row
			case "model_task":
				got, err = ValidateTaskPayload(c.Args)
			case "model_settings":
				got, err = ValidateSettings(c.Args)
			case "deadline":
				wall, z, ok := parseDeadline(c.Args["value"])
				if ok {
					var offset any
					if z != nil {
						offset = *z
					}
					got = []any{wall.Format("2006-01-02T15:04:05"), offset}
				}
			case "duration":
				v := promotionDuration(c.Args["value"])
				if v != nil {
					got = *v
				}
			case "expiry_at":
				v := p.expiryAt(c.Args["value"], number(c.Args["offset"]))
				if v != nil {
					got = iso(*v)
				}
			case "complete":
				got = complete(c.Args)
			case "added_free":
				got = addedFree(c.Args)
			case "candidate":
				task := NormalizeTask(row(c.Args["config"]), h.NewID)
				records, e := recordSet(c.Args["records"])
				if e != nil {
					t.Fatal(e)
				}
				ok, why, e := p.candidateCondition(context.Background(), task, row(c.Args["torrent"]), records)
				err = e
				var reason any
				if why != "" {
					reason = why
				}
				got = []any{ok, reason}
			case "delete_condition":
				task := NormalizeTask(row(c.Args["config"]), h.NewID)
				ok, why, e := p.deleteCondition(context.Background(), task, row(c.Args["info"]), row(c.Args["record"]))
				err = e
				got = []any{ok, why}
			case "torrent_info":
				got = p.torrentInfo(DownloadState{Kind: text(c.Args["kind"]), Raw: row(c.Args["raw"])})
			case "global_select":
				candidates := []deleteCandidate{}
				for _, v := range list(c.Args["candidates"]) {
					r := row(v)
					candidates = append(candidates, deleteCandidate{Task: Task{row(r["task"])}, Hash: text(r["torrent_hash"]), Downloader: text(r["downloader_name"]), Size: number(r["size"]), PreReason: text(r["pre_delete_reason"]), ConditionalReason: text(r["conditional_reason"]), Proxy: truth(r["proxy_delete"]), Completed: truth(r["completed"]), HR: truth(r["hit_and_run"]), SeedingTime: number(r["seeding_time"])})
				}
				chosen, remaining, threshold := selectGlobal(candidates, number(c.Args["total"]), number(c.Args["min"]), number(c.Args["max"]))
				selected := []any{}
				for _, v := range chosen {
					selected = append(selected, Row{"hash": v.Candidate.Hash, "reason": v.Reason, "task_reasons": v.TaskReasons})
				}
				got = Row{"selected": selected, "remaining": remaining, "threshold": threshold}
			default:
				t.Fatal("unknown source method", c.Method)
			}
			if err != nil {
				if c.Expected["error_class"] == nil {
					t.Fatal(err)
				}
				return
			}
			if c.Expected["error_class"] != nil {
				t.Fatal("wanted source exception", c.Expected)
			}
			if !equalJSON(got, c.Expected["value"]) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(c.Expected["value"])
				t.Fatalf("Go %s\nsource %s", a, b)
			}
		})
	}
}
func TestRecordOrderRoundTripAndOverwrite(t *testing.T) {
	r := NewRecords()
	r.Set("b", Row{"x": 1})
	r.Set("a", Row{"x": 2})
	r.Set("b", Row{"x": 3})
	b, _ := json.Marshal(r)
	if string(b) != `{"b":{"x":3},"a":{"x":2}}` {
		t.Fatal(string(b))
	}
	n := NewRecords()
	if e := json.Unmarshal(b, n); e != nil || !reflect.DeepEqual(n.Order, []string{"b", "a"}) {
		t.Fatal(n, e)
	}
	n.Del("b")
	n.Set("b", Row{})
	if strings.Join(n.Order, ",") != "a,b" {
		t.Fatal(n.Order)
	}
}
