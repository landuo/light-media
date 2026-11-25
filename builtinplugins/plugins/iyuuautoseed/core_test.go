package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

type seedReplay struct {
	BatchSizes []int      `json:"batch_sizes"`
	BatchEnds  [][]string `json:"batch_ends"`
	SiteLookup []int      `json:"site_lookups"`
	Counters   Counters   `json:"counters"`
	ErrorCache []string   `json:"error_caches"`
	Logs       []SourceLog
}

type recheckCall struct {
	Operation  string
	Downloader string
	Hashes     []string
}

type recheckReplay struct {
	Calls        []recheckCall
	Logs         []SourceLog
	Pending      []string
	Checked      []string
	FreshChecked []string `json:"fresh_checked"`
}

type coreReplayAPI struct{ trace *seedReplay }

func (a coreReplayAPI) SeedInfo(_ context.Context, hashes []string) (APIResult, error) {
	a.trace.BatchSizes = append(a.trace.BatchSizes, len(hashes))
	a.trace.BatchEnds = append(a.trace.BatchEnds, []string{hashes[0], hashes[len(hashes)-1]})
	candidate := func(n, sid int) map[string]any {
		return map[string]any{"info_hash": replayHash(n), "sid": sid}
	}
	key := hashes[0]
	switch len(a.trace.BatchSizes) {
	case 1:
		torrents := []any{candidate(201, 1), candidate(202, 2), candidate(203, 3), candidate(204, 4)}
		return APIResult{Data: map[string]any{key: map[string]any{"torrent": torrents}}, ObjectKeys: []string{key}}, nil
	case 3:
		return APIResult{Data: map[string]any{key: map[string]any{"torrent": candidate(201, 99)}}, ObjectKeys: []string{key}}, nil
	default:
		return APIResult{Data: map[string]any{}}, nil
	}
}

func (a coreReplayAPI) TorrentURL(_ context.Context, sid any) (any, any, error) {
	a.trace.SiteLookup = append(a.trace.SiteLookup, sid.(int))
	return nil, nil, nil
}

type coreReplayHost struct {
	Host
	t         *testing.T
	core      *Core
	services  []Service
	completed map[string][]NativeTorrent
	saved     map[string]any
	queued    map[string]NativeTorrent
	calls     []recheckCall
}

func (h *coreReplayHost) Inventory(context.Context, string, any) ([]Service, error) {
	return h.services, nil
}
func (h *coreReplayHost) Completed(_ context.Context, service Service) ([]NativeTorrent, error) {
	return h.completed[service.Name], nil
}
func (h *coreReplayHost) SaveConfig(_ context.Context, snapshot map[string]any) error {
	h.saved = snapshot
	return nil
}
func (h *coreReplayHost) Torrents(_ context.Context, service Service, ids []string) ([]NativeTorrent, error) {
	sorted := append([]string{}, ids...)
	sort.Strings(sorted)
	h.calls = append(h.calls, recheckCall{"list", service.Name, sorted})
	rows := []NativeTorrent{}
	for _, id := range ids {
		if row, ok := h.queued[id]; ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (h *coreReplayHost) Start(_ context.Context, service Service, ids []string) (EffectReceipt, error) {
	h.calls = append(h.calls, recheckCall{"start", service.Name, ids})
	return EffectReceipt{Accepted: true, Outcome: "confirmed"}, nil
}
func (h *coreReplayHost) Recheck(_ context.Context, service Service, ids []string) (EffectReceipt, error) {
	for _, id := range ids {
		if !h.core.autoRechecked[id] {
			h.t.Fatalf("recheck requested before marking %s", id)
		}
	}
	h.calls = append(h.calls, recheckCall{"recheck", service.Name, ids})
	return EffectReceipt{Accepted: true, Outcome: "confirmed"}, nil
}

func replayHash(n int) string { return fmt.Sprintf("%040x", n) }

func replayCompleted(n int, availability string, total, completed int64) NativeTorrent {
	return NativeTorrent{Fields: map[string]any{
		"hash": replayHash(n), "save_path": "/videos", "tags": "seed", "category": "movies",
		"availability": json.Number(availability), "total_size": json.Number(strconv.FormatInt(total, 10)),
		"completed": json.Number(strconv.FormatInt(completed, 10)),
	}}
}

type recheckLookupHost struct {
	Host
	inventory [][]Service
	reads     int
	checked   []string
}

func (h *recheckLookupHost) Inventory(context.Context, string, any) ([]Service, error) {
	if h.reads == len(h.inventory) {
		return nil, errors.New("unexpected inventory query")
	}
	rows := h.inventory[h.reads]
	h.reads++
	return rows, nil
}

func (h *recheckLookupHost) Torrents(_ context.Context, service Service, _ []string) ([]NativeTorrent, error) {
	h.checked = append(h.checked, service.Ref)
	return []NativeTorrent{}, nil
}

func TestCoreReplaysRecheckLookups222(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, Error    string
			Reads, Checked []string
			Running        bool
		}
	}
	raw, err := os.ReadFile("reference/core-recheck-lookups-oracle.json")
	if err != nil || json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 3 {
		t.Fatalf("missing original recheck lookup capture: %v", err)
	}
	old := Service{Ref: "old", Name: "owned", Kind: "qbittorrent"}
	current := Service{Ref: "new", Name: "owned", Kind: "qbittorrent"}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			config := NewConfig()
			config.values["auto_downloader"] = "owned"
			config.values["downloaders"] = []any{"owned"}
			host := &recheckLookupHost{checked: []string{}}
			switch row.Name {
			case "destination-refetched":
				host.inventory = [][]Service{{old}, {current}}
			case "destination-disappears":
				host.inventory = [][]Service{{old}, {}}
			case "selected-list-refetched":
				config.values["auto_downloader"] = "absent"
				host.inventory = [][]Service{{}, {old}, {current}}
			default:
				t.Fatalf("unknown source scenario %s", row.Name)
			}
			core, err := NewCore(config, host, coreReplayAPI{})
			if err != nil {
				t.Fatal(err)
			}
			core.queue["owned"] = []string{"queued"}
			err = core.CheckRecheck(t.Context())
			var fault *SourceFault
			if row.Error == "" && err != nil || row.Error != "" && (!errors.As(err, &fault) || fault.Class != row.Error) {
				t.Fatalf("source fault %q, got %v", row.Error, err)
			}
			if host.reads != len(row.Reads) || !reflect.DeepEqual(host.checked, row.Checked) || core.recheckRunning != row.Running {
				t.Fatalf("recheck lookup sequence differs: reads=%d checked=%v running=%v", host.reads, host.checked, core.recheckRunning)
			}
		})
	}
}

// The fixture was captured by executing the installed IYUU 2.22 methods at their
// original line numbers against deterministic, offline downloader and API inputs.
func TestCoreReplaysSource222(t *testing.T) {
	var expected struct {
		Source string        `json:"source_sha256"`
		Seed   seedReplay    `json:"auto_seed"`
		Check  recheckReplay `json:"recheck"`
	}
	raw, err := os.ReadFile("reference/core-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Source != "3cde4efa9ddd36f82318bed62a6a62ab9004257d8570538f937351b65970bdb7" {
		t.Fatalf("unexpected source fingerprint %s", expected.Source)
	}

	first := Service{Ref: "first", Name: "A", Kind: "qbittorrent"}
	second := Service{Ref: "second", Name: "B", Kind: "qbittorrent"}
	config := NewConfig()
	config.values["downloaders"] = []any{"A", "B"}
	config.values["error_caches"] = []any{replayHash(204)}
	config.values["token"] = "synthetic"
	config.values["size"] = json.Number("0")
	seedTrace := seedReplay{}
	api := coreReplayAPI{trace: &seedTrace}
	primary := make([]NativeTorrent, 0, 204)
	primary = append(primary, replayCompleted(1, "-1", 2000000000, 2000000000))
	for i := 2; i <= 201; i++ {
		primary = append(primary, replayCompleted(i, "1", 2000000000, 2000000000))
	}
	primary = append(primary, replayCompleted(202, "0.99", 2000000000, 2000000000),
		replayCompleted(203, "1", 9007199254740993, 9007199254740992),
		replayCompleted(204, "1", 2000000000, 2000000000))
	seedHost := &coreReplayHost{
		services: []Service{first, second},
		completed: map[string][]NativeTorrent{
			"A": primary,
			"B": {replayCompleted(500, "1", 2000000000, 2000000000)},
		},
	}
	seedCore, err := NewCore(config, seedHost, api)
	if err != nil {
		t.Fatal(err)
	}
	seedCtx := WithSourceLogSink(context.Background(), func(_ context.Context, event SourceLog) {
		seedTrace.Logs = append(seedTrace.Logs, event)
	})
	if err := seedCore.AutoSeed(seedCtx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := seedCore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	seedTrace.Counters = snapshot.Counters
	for _, hash := range seedHost.saved["error_caches"].([]any) {
		seedTrace.ErrorCache = append(seedTrace.ErrorCache, hash.(string))
	}
	if !reflect.DeepEqual(seedTrace, expected.Seed) {
		t.Errorf("completed torrent replay\ngot  %+v\nwant %+v", seedTrace, expected.Seed)
	}

	checkTrace := recheckReplay{}
	checkCtx := WithSourceLogSink(context.Background(), func(_ context.Context, event SourceLog) {
		if event.Method == "check_recheck_service" {
			checkTrace.Logs = append(checkTrace.Logs, event)
		}
	})
	queue := []string{"start", "fallback-stopped", "fallback-paused", "partial", "checking"}
	checkHost := &coreReplayHost{
		t: t, services: []Service{first},
		queued: map[string]NativeTorrent{
			"start":            {Fields: map[string]any{"hash": "start", "state": "stoppedUP", "progress": json.Number("1")}},
			"fallback-stopped": {Fields: map[string]any{"hash": "fallback-stopped", "state": "stoppedDL", "progress": json.Number("0")}},
			"fallback-paused":  {Fields: map[string]any{"hash": "fallback-paused", "state": "pausedDL"}},
			"partial":          {Fields: map[string]any{"hash": "partial", "state": "pausedDL", "progress": json.Number("0.5")}},
			"checking":         {Fields: map[string]any{"hash": "checking", "state": "checkingDL", "progress": json.Number("0")}},
		},
	}
	checked := func(core *Core) []string {
		state, err := core.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		var hashes []string
		for hash := range state.AutoRechecked {
			hashes = append(hashes, hash)
		}
		sort.Strings(hashes)
		return hashes
	}
	initialize := func() *Core {
		core, err := NewCore(config, checkHost, api)
		if err != nil {
			t.Fatal(err)
		}
		core.queue["A"] = append([]string{}, queue...)
		checkHost.core = core
		return core
	}
	checkCore := initialize()
	for range 2 {
		if err := checkCore.CheckRecheck(checkCtx); err != nil {
			t.Fatal(err)
		}
	}
	checkTrace.Pending = append([]string{}, checkCore.queue["A"]...)
	sort.Strings(checkTrace.Pending)
	checkTrace.Checked = checked(checkCore)
	fresh := initialize()
	if err := fresh.CheckRecheck(checkCtx); err != nil {
		t.Fatal(err)
	}
	checkTrace.FreshChecked = checked(fresh)
	checkTrace.Calls = checkHost.calls
	if !reflect.DeepEqual(checkTrace, expected.Check) {
		t.Errorf("supplemental recheck replay\ngot  %+v\nwant %+v", checkTrace, expected.Check)
	}
}
