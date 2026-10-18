package seedrescue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHost struct {
	mu          sync.Mutex
	data        map[string]json.RawMessage
	config      Object
	clock       time.Time
	sites       map[int]Object
	indexes     map[string]Object
	rss         func(context.Context, string, Object) ([]Object, bool, error)
	seed        func(context.Context, []string) (any, string, error)
	torrentURL  func(any) (string, string, error)
	detail      func(context.Context, string, Object, string) (DetailResult, error)
	search      func(context.Context, string, string, int) ([]Object, error)
	metadata    func(Object) ([]byte, string, error)
	query       func([]string) ([]Object, error)
	downloading []Object
	add         func(AddRequest) (bool, error)
	adds        int
	logs        []string
	notices     []Object
	writes      []string
	guardErr    error
}

func newFake() *fakeHost {
	return &fakeHost{data: map[string]json.RawMessage{}, clock: time.Date(2026, 10, 18, 1, 2, 3, 0, time.UTC), sites: map[int]Object{1: {"id": 1, "name": "Target", "url": "https://target.test/", "domain": "target.test", "schema": "nexus"}}, indexes: map[string]Object{"source.test": {"id": 2, "name": "Source", "url": "https://source.test/", "schema": "nexus", "cookie": "fixture-secret-cookie"}}}
}
func (h *fakeHost) Guard(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return h.guardErr
}
func (h *fakeHost) Now() time.Time { return h.clock }
func (h *fakeHost) LoadData(ctx context.Context, key string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.data[key]...), nil
}
func (h *fakeHost) SaveData(ctx context.Context, key string, b json.RawMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.data[key] = append([]byte(nil), b...)
	h.writes = append(h.writes, key)
	return nil
}
func (h *fakeHost) UpdateConfig(ctx context.Context, c Object) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config = clone(c)
	return nil
}
func (h *fakeHost) IYUUAvailable(context.Context) bool { return true }
func (h *fakeHost) IYUUEnabled(context.Context) bool   { return true }
func (h *fakeHost) Service(context.Context, string) (*Service, error) {
	return &Service{"qb", "qbittorrent", true}, nil
}
func (h *fakeHost) Site(ctx context.Context, id int) (Object, error) { return clone(h.sites[id]), nil }
func (h *fakeHost) Indexer(ctx context.Context, d string) (Object, error) {
	return clone(h.indexes[d]), nil
}
func (h *fakeHost) Options(context.Context) (Object, error) { return Object{}, nil }
func (h *fakeHost) RSS(ctx context.Context, u string, s Object) ([]Object, bool, error) {
	if h.rss != nil {
		return h.rss(ctx, u, s)
	}
	return []Object{}, true, nil
}
func (h *fakeHost) SeedInfo(ctx context.Context, hashes []string) (any, string, error) {
	if h.seed != nil {
		return h.seed(ctx, hashes)
	}
	return Object{}, "", nil
}
func (h *fakeHost) TorrentURL(ctx context.Context, sid any) (string, string, error) {
	if h.torrentURL != nil {
		return h.torrentURL(sid)
	}
	return "https://source.test/", "download.php?id={id}", nil
}
func (h *fakeHost) Detail(ctx context.Context, d string, s Object, id string) (DetailResult, error) {
	if h.detail != nil {
		return h.detail(ctx, d, s, id)
	}
	return DetailResult{Status: "ready", Torrent: Object{"title": "Source fixture", "page_url": "https://source.test/details.php?id=" + id, "enclosure": "https://source.test/download.php?id=" + id + "&passkey=fixture-secret", "seeders": 3, "downloadvolumefactor": 0}}, nil
}
func (h *fakeHost) Search(ctx context.Context, d, k string, page int) ([]Object, error) {
	if h.search != nil {
		return h.search(ctx, d, k, page)
	}
	return []Object{}, nil
}
func (h *fakeHost) DownloadMetadata(ctx context.Context, t Object) ([]byte, string, error) {
	if h.metadata != nil {
		return h.metadata(t)
	}
	return fixtureTorrent, "", nil
}
func (h *fakeHost) QueryTorrents(ctx context.Context, name string, hashes []string) ([]Object, error) {
	if h.query != nil {
		return h.query(hashes)
	}
	return []Object{}, nil
}
func (h *fakeHost) DownloadingTorrents(context.Context, string, string) ([]Object, error) {
	if h.downloading == nil {
		return []Object{}, nil
	}
	return h.downloading, nil
}
func (h *fakeHost) AddTorrent(ctx context.Context, req AddRequest) (bool, error) {
	h.mu.Lock()
	h.adds++
	h.mu.Unlock()
	if h.add != nil {
		return h.add(req)
	}
	return true, nil
}
func (h *fakeHost) PostMessage(ctx context.Context, v Object) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notices = append(h.notices, clone(v))
	return nil
}
func (h *fakeHost) Log(ctx context.Context, l, m string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, l+":"+m)
}
func (h *fakeHost) ValidateCron(c string) error {
	if c == "bad" {
		return errors.New("invalid cron")
	}
	return nil
}

var fixtureTorrent = []byte("d4:infod4:name1:xee")

func baseConfig() Object {
	return Object{"enabled": true, "token": "fixture-token", "target_site_id": 1, "source_site_ids": []int{2}, "rss_urls": "https://target.test/rss?passkey=fixture-secret", "downloader": "qb", "allowed_promotions": []string{Free, NonFree}, "selection_mode": "manual"}
}
func newMachine(t *testing.T, h *fakeHost) *Machine {
	t.Helper()
	m, e := New(context.Background(), h)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Init(context.Background(), baseConfig()); e != nil {
		t.Fatal(e)
	}
	return m
}
func fixtureJob() Object {
	hash, _ := InfoHashFromTorrent(fixtureTorrent)
	return Object{"target_hash": strings.Repeat("a", 40), "target_site_id": 1, "target_site_name": "Target", "target_torrent_id": "42", "target_page_url": "https://target.test/details.php?id=42", "title": "Film.2026.WEB-DL", "state": "AWAITING_SELECTION", "feed_key": "key", "created_at": "2026-10-18T01:02:03+00:00", "probes": []Object{}, "candidates": []Object{{"candidate_id": "2:7:" + hash, "iyuu_sid": 2, "site_id": 2, "site_name": "Source", "torrent_id": "7", "expected_hash": hash, "eligible": true}}}
}
func saveFixture(t *testing.T, m *Machine, job Object) {
	t.Helper()
	j := newJobs()
	j.set(pyString(job["target_hash"]), job)
	if e := m.saveJobs(context.Background(), j); e != nil {
		t.Fatal(e)
	}
}
func loadFixture(t *testing.T, m *Machine, hash string) Object {
	t.Helper()
	j, e := m.loadJobs(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return j.items[hash]
}

func TestSubmissionIntentAndUnknownNeverReissued(t *testing.T) {
	for _, mode := range []string{"false", "error"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			h := newFake()
			m := newMachine(t, h)
			j := fixtureJob()
			saveFixture(t, m, j)
			hash := pyString(j["target_hash"])
			sourceHash, _ := InfoHashFromTorrent(fixtureTorrent)
			h.add = func(req AddRequest) (bool, error) {
				persisted := loadFixture(t, m, hash)
				if persisted["state"] != "SOURCE_SUBMITTING" || persisted["source_hash"] != sourceHash {
					t.Fatal("add ran before durable intent")
				}
				if req.Category != Category || len(req.Tags) != 1 || req.Tags[0] != GlobalTag || string(req.Content) != string(fixtureTorrent) {
					t.Fatal("source downloader parameters changed")
				}
				if mode == "error" {
					return false, errors.New("fixture lost ack")
				}
				return false, nil
			}
			p := Selection{CandidateID: "2:7:" + sourceHash}
			r, e := m.DownloadCandidate(ctx, hash, p)
			if e != nil || r.Success {
				t.Fatalf("%+v %v", r, e)
			}
			saved := loadFixture(t, m, hash)
			if saved["state"] != "SOURCE_SUBMITTING" || h.adds != 1 {
				t.Fatal(saved, h.adds)
			}
			for i := 0; i < 2; i++ {
				r, e = m.DownloadCandidate(ctx, hash, p)
				if e != nil || r.Success {
					t.Fatal(r, e)
				}
				r, e = m.RetryJob(ctx, hash)
				if e != nil || r.Success {
					t.Fatal(r, e)
				}
				if e = m.Scan(ctx); e != nil {
					t.Fatal(e)
				}
			}
			if h.adds != 1 {
				t.Fatal("unknown request reissued", h.adds)
			}
			h.query = func(hashes []string) ([]Object, error) {
				if hashes[0] == sourceHash {
					return []Object{{"state": "uploading", "progress": 1, "amount_left": 0}}, nil
				}
				return nil, nil
			}
			if e = m.Scan(ctx); e != nil {
				t.Fatal(e)
			}
			saved = loadFixture(t, m, hash)
			if saved["state"] != "WAITING_IYUU" || h.adds != 1 {
				t.Fatal(saved, h.adds)
			}
		})
	}
}
func TestFreshPromotionRiskAndConfirmation(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	j := fixtureJob()
	saveFixture(t, m, j)
	hash := pyString(j["target_hash"])
	sourceHash, _ := InfoHashFromTorrent(fixtureTorrent)
	factor := .5
	h.detail = func(context.Context, string, Object, string) (DetailResult, error) {
		return DetailResult{Status: "ready", Torrent: Object{"title": "Source", "enclosure": "https://source.test/download.php?id=7&passkey=fixture-secret", "page_url": "https://source.test/details.php?id=7&passkey=fixture-secret", "seeders": 3, "downloadvolumefactor": factor}}, nil
	}
	p := Selection{CandidateID: "2:7:" + sourceHash}
	r, e := m.DownloadCandidate(ctx, hash, p)
	if e != nil || r.Success || r.Data.(map[string]any)["confirmation_required"] != true || h.adds != 0 {
		t.Fatal(r, e)
	}
	p.ConfirmedDownloadFactor = ptr(.5)
	p.SkipFutureRiskConfirmation = true
	factor = 1
	r, e = m.DownloadCandidate(ctx, hash, p)
	if e != nil || r.Success || h.adds != 0 || !m.configuration().ConfirmDownloadRisk {
		t.Fatal("stale confirmation accepted", r, e)
	}
	p.ConfirmedDownloadFactor = ptr(1.0)
	r, e = m.DownloadCandidate(ctx, hash, p)
	if e != nil || !r.Success || h.adds != 1 || m.configuration().ConfirmDownloadRisk {
		t.Fatal(r, e, h.adds)
	}
	h.mu.Lock()
	raw := string(h.data[JobsKey])
	h.mu.Unlock()
	if strings.Contains(raw, "fixture-secret") || strings.Contains(raw, "enclosure") || strings.Contains(raw, "site_cookie") {
		t.Fatal("secret persisted", raw)
	}
	r, e = m.DownloadCandidate(ctx, hash, p)
	if e != nil || !r.Success || h.adds != 1 {
		t.Fatal("confirmed retry was not idempotent", r, e)
	}
}
func TestRSSSnapshotsFailureThenArchiveAndColdStatus(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	rssState := 0
	h.rss = func(context.Context, string, Object) ([]Object, bool, error) {
		if rssState == 1 {
			return nil, false, errors.New("fixture fetch failure")
		}
		if rssState == 2 {
			return []Object{}, true, nil
		}
		return []Object{{"title": "Film.2026", "link": "https://target.test/details.php?id=42", "enclosure": "https://target.test/download.php?id=42&passkey=fixture-secret"}}, true, nil
	}
	if e := m.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, e := m.loadJobs(ctx)
	if e != nil || len(jobs.order) != 1 {
		t.Fatal(e, jobs)
	}
	hash := jobs.order[0]
	if jobs.items[hash]["rss_current"] != true || jobs.items[hash]["state"] != "SKIPPED_NO_IYUU_MATCH" {
		t.Fatal(jobs.items[hash])
	}
	rssState = 1
	if e = m.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if m.ScanStatus()["state"] != "failed" || loadFixture(t, m, hash)["rss_current"] != true {
		t.Fatal(m.ScanStatus())
	}
	rssState = 2
	if e = m.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if loadFixture(t, m, hash)["rss_current"] != false {
		t.Fatal("empty successful snapshot did not archive")
	}
	h.data[ScanStatusKey] = json.RawMessage(`{"state":"running","stage":"matching"}`)
	cold, e := New(ctx, h)
	if e != nil || cold.ScanStatus()["state"] != "interrupted" {
		t.Fatal(e, cold.ScanStatus())
	}
}
func TestSourceFailureDoesNotCauseSearchStorm(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{"unavailable", "unsupported", "partial"} {
		t.Run(status, func(t *testing.T) {
			h := newFake()
			m := newMachine(t, h)
			searches := 0
			details := 0
			h.detail = func(context.Context, string, Object, string) (DetailResult, error) {
				details++
				return DetailResult{Status: status, Error: ptr("fixed error")}, nil
			}
			h.search = func(context.Context, string, string, int) ([]Object, error) {
				searches++
				return []Object{{"page_url": "https://source.test/details.php?id=99"}}, nil
			}
			cs, ps, e := m.probeSources(ctx, []Match{{2, "7", strings.Repeat("b", 40)}, {2, "7", strings.Repeat("c", 40)}}, "Film.2026.WEB-DL")
			if e != nil || len(cs) != 2 || len(ps) != 2 || details != 1 {
				t.Fatal(e, cs, ps, details)
			}
			if status == "unavailable" && searches != 0 {
				t.Fatal("authentication/network unavailable triggered fallback", searches)
			}
			if searches > 8 {
				t.Fatal(searches)
			}
			if status != "unavailable" && searches == 0 {
				t.Fatal("missing fallback")
			}
		})
	}
}
func TestJobViewsFilterSameCandidateAndDoNotLeak(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	jobs := newJobs()
	for i := 0; i < 3; i++ {
		j := fixtureJob()
		j["target_hash"] = fmt.Sprintf("%040d", i)
		j["rss_current"] = true
		j["rss_last_seen_at"] = fmt.Sprint(i)
		j["target_seeders"] = i
		j["candidates"] = []Object{{"site_id": 2, "promotion": Free}, {"site_id": 3, "promotion": NonFree}}
		jobs.set(pyString(j["target_hash"]), j)
	}
	if e := m.saveJobs(ctx, jobs); e != nil {
		t.Fatal(e)
	}
	r, e := m.GetStatus(ctx, StatusFilter{SourceSiteID: ptr(2), SourcePromotion: NonFree, PageSize: 1})
	if e != nil || !r.Success || r.Data.(map[string]any)["pagination"].(map[string]any)["total"] != 0 {
		t.Fatal(r, e)
	}
	r, e = m.GetStatus(ctx, StatusFilter{MinSeeders: ptr(1), Page: 9, PageSize: 1})
	if e != nil {
		t.Fatal(e)
	}
	d := r.Data.(map[string]any)
	if len(d["jobs"].([]Object)) != 1 || d["pagination"].(map[string]any)["page"] != 2 {
		t.Fatal(d)
	}
}
func TestRetryBackoffRetentionAndNotification(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	job := fixtureJob()
	for i := 1; i <= 5; i++ {
		if e := m.deferRetry(ctx, job, "fixture retry", ""); e != nil {
			t.Fatal(e)
		}
		if i < 5 {
			next, e := parseTime(job["next_retry_at"])
			if e != nil || next.Sub(h.clock) != time.Duration(5*(1<<(i-1)))*time.Minute {
				t.Fatal(job)
			}
		}
	}
	if job["state"] != "FAILED_MATCHING" || job["next_retry_at"] != nil || len(h.notices) != 1 {
		t.Fatal(job, h.notices)
	}
	if e := m.finish(ctx, job, "FAILED_MATCHING", ptr(pyString(job["reason"]))); e != nil || len(h.notices) != 1 {
		t.Fatal(e, h.notices)
	}
	jobs := newJobs()
	for i := 0; i < 1100; i++ {
		j := Object{"state": "IGNORED", "created_at": fmt.Sprintf("%04d", i)}
		jobs.set(fmt.Sprint(i), j)
	}
	jobs.set("current", Object{"rss_current": true, "state": "IGNORED"})
	jobs.set("unknown", Object{"state": "SOURCE_SUBMITTING"})
	jobs.set("retry", Object{"state": "WAITING_SOURCE_RETRY"})
	if e := m.saveJobs(ctx, jobs); e != nil {
		t.Fatal(e)
	}
	kept, e := m.loadJobs(ctx)
	if e != nil || len(kept.order) != 1003 || kept.items["99"] != nil || kept.items["100"] == nil {
		t.Fatal(e, len(kept.order))
	}
}
func TestProbePerSiteSerialAndSixGlobalSlots(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	cfg := baseConfig()
	cfg["source_site_ids"] = []int{2, 3, 4, 5, 6, 7, 8, 9}
	if e := m.Init(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	for i := 2; i <= 9; i++ {
		d := fmt.Sprintf("s%d.test", i)
		h.indexes[d] = Object{"id": i, "name": d, "url": "https://" + d + "/", "schema": "nexus"}
	}
	h.torrentURL = func(sid any) (string, string, error) {
		return fmt.Sprintf("https://s%d.test/", asInt(sid, 0)), "download.php?id={id}", nil
	}
	var mu sync.Mutex
	active, maxAll := 0, 0
	per := map[string]int{}
	bad := false
	h.detail = func(ctx context.Context, d string, _ Object, _ string) (DetailResult, error) {
		mu.Lock()
		active++
		per[d]++
		if active > maxAll {
			maxAll = active
		}
		if per[d] > 1 {
			bad = true
		}
		mu.Unlock()
		time.Sleep(time.Millisecond * 3)
		mu.Lock()
		active--
		per[d]--
		mu.Unlock()
		return DetailResult{Status: "unavailable", Error: ptr("fixed")}, nil
	}
	var wg sync.WaitGroup
	for run := 0; run < 4; run++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms := []Match{}
			for i := 2; i <= 9; i++ {
				ms = append(ms, Match{i, "1", strings.Repeat("b", 40)})
			}
			_, _, e := m.probeSources(ctx, ms, "Film")
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if bad || maxAll > 6 || maxAll < 2 {
		t.Fatal(bad, maxAll)
	}
}
func TestStopDoesNotResurrectPersistedScan(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	entered := make(chan struct{})
	release := make(chan struct{})
	h.rss = func(context.Context, string, Object) ([]Object, bool, error) {
		close(entered)
		<-release
		return []Object{}, true, nil
	}
	r, e := m.ScanNow(ctx)
	if e != nil || !r.Success {
		t.Fatal(r, e)
	}
	<-entered
	stopCtx, cancel := context.WithCancel(ctx)
	cancel()
	_ = m.Stop(stopCtx)
	if m.ScanStatus()["state"] != "interrupted" {
		t.Fatal(m.ScanStatus())
	}
	close(release)
	m.workerMu.Lock()
	done := m.workerDone
	m.workerMu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scan did not exit")
	}
	if m.Busy() || m.ScanStatus()["state"] != "interrupted" {
		t.Fatal(m.ScanStatus())
	}
}

func TestTargetRSSFallbackIgnoresCachedFreeAndExistingTargetReuse(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	job := fixtureJob()
	actual, _ := InfoHashFromTorrent(fixtureTorrent)
	job["target_hash"] = actual
	job["target_download_factor"] = 0
	job["state"] = "SKIPPED_NO_IYUU_MATCH"
	job["feed_key"] = itemFeedKey(Object{"enclosure": "https://target.test/download.php?id=42&passkey=fixture-secret"})
	saveFixture(t, m, job)
	h.rss = func(context.Context, string, Object) ([]Object, bool, error) {
		return []Object{{"title": "Fixture", "link": "https://target.test/details.php?id=42", "enclosure": "https://target.test/download.php?id=42&passkey=fixture-secret"}}, true, nil
	}
	p := Selection{CandidateID: targetCandidateID(job)}
	r, e := m.DownloadCandidate(ctx, actual, p)
	if e != nil || r.Success || r.Data.(map[string]any)["promotion"] != Unknown || h.adds != 0 {
		t.Fatal(r, e)
	}
	h.query = func(hashes []string) ([]Object, error) {
		if hashes[0] == actual {
			return []Object{{"state": "uploading", "amount_left": 0, "progress": 1}}, nil
		}
		return nil, nil
	}
	r, e = m.DownloadCandidate(ctx, actual, p)
	if e != nil || !r.Success || h.adds != 0 || loadFixture(t, m, actual)["state"] != "TARGET_SEEDING" {
		t.Fatal(r, e)
	}
}
func TestOriginalOnceCronMetadataAndInvalidCron(t *testing.T) {
	ctx := context.Background()
	h := newFake()
	m := newMachine(t, h)
	cfg := baseConfig()
	cfg["onlyonce"] = true
	if e := m.Init(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	if h.config["onlyonce"] != false || m.Config()["onlyonce"] != false {
		t.Fatal("once was not persisted cleared")
	}
	ss, e := m.Services(ctx)
	if e != nil || len(ss) != 2 || ss[0].ID != "SeedRescue.Scan" || ss[1].RunAt.Sub(h.clock) != 3*time.Second {
		t.Fatal(ss, e)
	}
	cfg["cron"] = "bad"
	if e = m.Init(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	ss, e = m.Services(ctx)
	if e != nil || len(ss) != 1 || ss[0].ID != "SeedRescue.RunOnce" {
		t.Fatal(ss, e)
	}
	if len(APIs()) != 6 || len(Commands()) != 0 || len(m.Sidebar()) != 1 || len(m.Page()) != 0 {
		t.Fatal("source public metadata mismatch")
	}
	cfg["enabled"] = false
	cfg["onlyonce"] = false
	if e = m.Init(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	ss, e = m.Services(ctx)
	if e != nil || len(ss) != 0 || len(m.Sidebar()) != 0 {
		t.Fatal(ss, e)
	}
}
