package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type historyReplayNotice struct {
	Type  string `json:"mtype"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type historyReplayHost struct {
	*coreReplayHost
	originals, candidates []string
	firstIndexerError     error
	legacy                any
	adds, reads           []string
	histories             map[string]any
	successCaches         [][]any
	notices               []historyReplayNotice
}

func (h *historyReplayHost) Indexer(context.Context, string) (*Site, error) {
	if err := h.firstIndexerError; err != nil {
		h.firstIndexerError = nil
		return nil, err
	}
	return &Site{Fields: map[string]any{"id": 1, "url": "https://owned.example/", "name": "owned"}}, nil
}

func (h *historyReplayHost) CheckSite(context.Context, string) (bool, error) { return false, nil }

func (h *historyReplayHost) DownloadURL(_ context.Context, seed map[string]any, _ Site, _ string) (string, error) {
	return "https://owned.example/" + seed["info_hash"].(string), nil
}

func (h *historyReplayHost) Torrent(_ context.Context, _ Site, address string) (TorrentContent, error) {
	hash := strings.TrimSuffix(strings.TrimPrefix(address, "https://owned.example/"), "?https=1")
	return TorrentContent{Bytes: []byte(hash)}, nil
}

func (h *historyReplayHost) AddPaused(_ context.Context, _ Service, spec AddSpec) (AddReceipt, error) {
	hash := string(spec.Content)
	h.adds = append(h.adds, hash)
	return AddReceipt{SourceTruthy: true, Accepted: true, Outcome: "confirmed", Hash: hash}, nil
}

func (h *historyReplayHost) LoadHistory(_ context.Context, hash string) (any, error) {
	h.reads = append(h.reads, hash)
	if hash == h.originals[0] {
		return h.legacy, nil
	}
	return nil, nil
}

func (h *historyReplayHost) SaveHistory(_ context.Context, hash string, history any) error {
	h.histories[hash] = history
	return nil
}

func (h *historyReplayHost) SaveConfig(_ context.Context, config map[string]any) error {
	h.successCaches = append(h.successCaches, config["success_caches"].([]any))
	return nil
}

func (h *historyReplayHost) Notify(_ context.Context, title, message string) error {
	h.notices = append(h.notices, historyReplayNotice{Type: "站点", Title: title, Text: message})
	return nil
}

func (h *historyReplayHost) SeedInfo(_ context.Context, hashes []string) (APIResult, error) {
	rows := map[string]any{}
	for i, key := range hashes {
		rows[key] = map[string]any{"torrent": map[string]any{"sid": json.Number("1"), "info_hash": h.candidates[i]}}
	}
	return APIResult{Data: rows, ObjectKeys: hashes}, nil
}

func (h *historyReplayHost) TorrentURL(context.Context, any) (any, any, error) {
	return "https://owned.example/", "/download", nil
}

func TestSourceIYUUHistoryShapeKeepsSuccessfulPass222(t *testing.T) {
	var oracle struct {
		Originals, Candidates []string
		Legacy                any            `json:"legacy_history"`
		Adds                  []string       `json:"successful_adds"`
		Reads                 []string       `json:"history_reads"`
		Writes                map[string]any `json:"history_writes"`
		Caches                [][]any        `json:"persisted_success_caches"`
		Notifications         []historyReplayNotice
		Diagnostic            string
		FinalLog              string `json:"final_log"`
	}
	raw, err := os.ReadFile("reference/history-shape-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	legacy, err := cloneJSON(oracle.Legacy)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Ref: "owned", Name: "owned", Kind: "transmission"}
	completed := []NativeTorrent{}
	for _, hash := range oracle.Originals {
		completed = append(completed, NativeTorrent{Fields: map[string]any{"hashString": hash, "download_dir": "/owned", "total_size": json.Number("100")}})
	}
	host := &historyReplayHost{
		coreReplayHost: &coreReplayHost{services: []Service{service}, completed: map[string][]NativeTorrent{service.Name: completed}},
		originals:      oracle.Originals, candidates: oracle.Candidates, legacy: legacy, histories: map[string]any{},
	}
	cfg := NewConfig()
	cfg.values["downloaders"] = []any{service.Name}
	cfg.values["notify"] = true
	core, err := NewCore(cfg, host, host)
	if err != nil {
		t.Fatal(err)
	}
	var finalLog string
	ctx := WithSourceLogSink(t.Context(), func(_ context.Context, entry SourceLog) { finalLog = entry.Message })
	diagnostic, runErr := captureHistoryDiagnostic(t, func() error { return core.AutoSeed(ctx) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !reflect.DeepEqual(host.adds, oracle.Adds) || !reflect.DeepEqual(host.reads, oracle.Reads) ||
		!reflect.DeepEqual(host.histories, oracle.Writes) || !reflect.DeepEqual(host.successCaches, oracle.Caches) ||
		!reflect.DeepEqual(host.notices, oracle.Notifications) || !reflect.DeepEqual(host.legacy, oracle.Legacy) ||
		diagnostic != oracle.Diagnostic || finalLog != oracle.FinalLog {
		t.Fatalf("source history behavior diverged: adds %v, reads %v, writes %v, cache %v, notices %v, legacy %v, diagnostic %q, final log %q",
			host.adds, host.reads, host.histories, host.successCaches, host.notices, host.legacy, diagnostic, finalLog)
	}
}

func TestIYUUPreparationFailureDoesNotBlockTransmission(t *testing.T) {
	qb := Service{Name: "qb", Kind: "qbittorrent"}
	tr := Service{Name: "tr", Kind: "transmission"}
	size := json.Number("100")
	source, candidate := replayHash(1), replayHash(2)
	host := &historyReplayHost{
		coreReplayHost: &coreReplayHost{
			services: []Service{qb, tr},
			completed: map[string][]NativeTorrent{
				qb.Name: {{Fields: map[string]any{"hash": source, "save_path": "/owned", "total_size": size, "completed": size, "availability": size}}},
				tr.Name: {{Fields: map[string]any{"hashString": source, "download_dir": "/owned", "total_size": size}}},
			},
		},
		originals: []string{source}, candidates: []string{candidate}, histories: map[string]any{},
		firstIndexerError: errors.New("internal failure with private test data"),
	}
	cfg := NewConfig()
	cfg.values["downloaders"] = []any{qb.Name, tr.Name}
	core, err := NewCore(cfg, host, host)
	if err != nil {
		t.Fatal(err)
	}
	var warning string
	ctx := WithSourceLogSink(t.Context(), func(_ context.Context, log SourceLog) {
		if strings.Contains(log.Message, "读取站点配置") {
			warning = log.Message
		}
	})
	if err = core.AutoSeed(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(host.adds, []string{candidate}) || core.counters.Fail != 1 || core.counters.Success != 1 {
		t.Fatalf("expected qB preparation failure and TR add, got adds %v, counters %+v", host.adds, core.counters)
	}
	if !strings.Contains(warning, "下载器 qb") || strings.Contains(warning, "private test data") {
		t.Fatalf("expected a safe stage diagnostic, got %q", warning)
	}
}

type mutationIsolationReplay struct {
	*historyReplayHost
	rejected    string
	submissions []string
}

func (h *mutationIsolationReplay) SeedInfo(_ context.Context, hashes []string) (APIResult, error) {
	rows := map[string]any{}
	for _, hash := range hashes {
		candidates := h.candidates[2:]
		if hash == h.originals[0] {
			candidates = h.candidates[:2]
		}
		torrents := make([]any, 0, len(candidates))
		for _, candidate := range candidates {
			torrents = append(torrents, map[string]any{"sid": json.Number("1"), "info_hash": candidate})
		}
		rows[hash] = map[string]any{"torrent": torrents}
	}
	return APIResult{Data: rows, ObjectKeys: hashes}, nil
}

func (h *mutationIsolationReplay) AddPaused(ctx context.Context, service Service, spec AddSpec) (AddReceipt, error) {
	hash := string(spec.Content)
	h.submissions = append(h.submissions, service.Name+":"+hash)
	if hash == h.rejected {
		return AddReceipt{Outcome: "unknown"}, errors.New("private transport error")
	}
	return h.historyReplayHost.AddPaused(ctx, service, spec)
}

func (h *mutationIsolationReplay) RandomTag(context.Context, int) (string, error) {
	return "ownedtag01", nil
}

func (h *mutationIsolationReplay) FindByTag(context.Context, Service, string) (string, error) {
	return h.adds[len(h.adds)-1], nil
}

func TestIYUUAmbiguousQBAddKeepsHistoryAndScansIndependentTR(t *testing.T) {
	qb := Service{Name: "qb", Kind: "qbittorrent"}
	tr := Service{Name: "tr", Kind: "transmission"}
	size := json.Number("100")
	qbSource, trSource := replayHash(1), replayHash(2)
	qbAdded, qbUnknown, trAdded := replayHash(3), replayHash(4), replayHash(5)
	host := &mutationIsolationReplay{
		historyReplayHost: &historyReplayHost{
			coreReplayHost: &coreReplayHost{
				services: []Service{qb, tr},
				completed: map[string][]NativeTorrent{
					qb.Name: {{Fields: map[string]any{"hash": qbSource, "save_path": "/owned", "total_size": size, "completed": size, "availability": size}}},
					tr.Name: {{Fields: map[string]any{"hashString": trSource, "download_dir": "/owned", "total_size": size}}},
				},
			},
			originals: []string{qbSource, trSource}, candidates: []string{qbAdded, qbUnknown, trAdded}, histories: map[string]any{},
		},
		rejected: qbUnknown,
	}
	cfg := NewConfig()
	cfg.values["downloaders"] = []any{qb.Name, tr.Name}
	cfg.values["skipverify"] = true
	core, err := NewCore(cfg, host, host)
	if err != nil {
		t.Fatal(err)
	}
	err = core.AutoSeed(t.Context())
	var mutation *seedMutationError
	if !errors.As(err, &mutation) || strings.Contains(err.Error(), "private transport error") {
		t.Fatalf("expected a safe partial failure after scanning both downloaders, got %v", err)
	}
	expected := []string{"qb:" + qbAdded, "qb:" + qbUnknown, "tr:" + trAdded}
	if !reflect.DeepEqual(host.submissions, expected) {
		t.Fatalf("unexpected adds or retry: %v", host.submissions)
	}
	expectedHistory := map[string]any{
		qbSource: []any{map[string]any{"downloader": "qb", "torrents": []any{qbAdded}}},
		trSource: []any{map[string]any{"downloader": "tr", "torrents": []any{trAdded}}},
	}
	if !reflect.DeepEqual(host.histories, expectedHistory) || len(host.successCaches) != 1 ||
		!reflect.DeepEqual(host.successCaches[0], []any{qbAdded, trAdded}) {
		t.Fatalf("expected both confirmed adds to survive: history=%v, cache=%v", host.histories, host.successCaches)
	}
}

func captureHistoryDiagnostic(t *testing.T, run func() error) (string, error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	previous := os.Stderr
	os.Stderr = write
	defer func() {
		os.Stderr = previous
		_ = write.Close()
	}()
	runErr := run()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw)), runErr
}
