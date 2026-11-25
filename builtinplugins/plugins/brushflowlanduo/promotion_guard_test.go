package brushflowlanduo

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

type promotionHost struct {
	Host
	now             time.Time
	data            map[string]json.RawMessage
	torrents        []DownloadState
	torrentsErr     error
	torrentReads    int
	goneAfterDelete bool
	deleteUncertain bool
	messageErr      error
	completed       []DownloadState
	completedErr    error
	pauses          [][]string
	starts          [][]string
	deletes         [][]string
}

func (h *promotionHost) Now() time.Time { return h.now }
func (h *promotionHost) NewID() string  { return "run" }
func (h *promotionHost) Get(_ context.Context, key string) (any, error) {
	if value, ok := h.data[key]; ok {
		return value, nil
	}
	return nil, nil
}
func (h *promotionHost) Put(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err == nil {
		h.data[key] = raw
	}
	return err
}
func (*promotionHost) Sites(context.Context) ([]Site, error) {
	return []Site{{ID: 3, Name: "家园", URL: "https://hdhome.org", Domain: "hdhome.org"}}, nil
}
func (*promotionHost) Downloaders(context.Context) ([]Downloader, error) {
	return []Downloader{{Name: "qb", Kind: "qbittorrent"}}, nil
}
func (*promotionHost) Downloader(context.Context, string) (*Downloader, error) {
	return &Downloader{Name: "qb", Kind: "qbittorrent", Active: true}, nil
}
func (h *promotionHost) Completed(context.Context, string, []string) ([]DownloadState, error) {
	return h.completed, h.completedErr
}
func (h *promotionHost) Torrents(context.Context, string) ([]DownloadState, error) {
	h.torrentReads++
	if h.goneAfterDelete && h.torrentReads > 1 {
		return nil, nil
	}
	return h.torrents, h.torrentsErr
}
func (h *promotionHost) Pause(_ context.Context, _ string, hashes []string) (bool, error) {
	h.pauses = append(h.pauses, append([]string{}, hashes...))
	return true, nil
}
func (h *promotionHost) Start(_ context.Context, _ string, hashes []string) (bool, error) {
	h.starts = append(h.starts, append([]string{}, hashes...))
	return true, nil
}
func (h *promotionHost) Delete(_ context.Context, _ string, hashes []string, _ bool) (bool, error) {
	h.deletes = append(h.deletes, append([]string{}, hashes...))
	return !h.deleteUncertain, nil
}
func (*promotionHost) Reannounce(context.Context, string, []string) error { return nil }
func (*promotionHost) Event(context.Context, Event) error                 { return nil }
func (h *promotionHost) Message(context.Context, Notice) error            { return h.messageErr }
func (*promotionHost) Log(context.Context, string, string)                {}
func (*promotionHost) Tags(context.Context, string) ([]string, error)     { return nil, nil }
func (*promotionHost) DeleteTags(context.Context, string, []string) (bool, error) {
	return true, nil
}

func promotionTask() Task {
	return Task{Row{"id": "home", "name": "家园", "site_id": 3, "downloader": "qb", "enabled": true,
		"del_no_free": true, "freeleech": "free", "timezone_offset": 0, "check_interval": 5, "maxdlcount": 5}}
}

func promotionNow() time.Time {
	return time.Date(2026, 11, 16, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
}

func promotionRecord(t Task, hash string, now time.Time, progress float64) Row {
	return Row{
		"task_id": t.ID(), "task_name": t.Name(), "site": t.SiteID(), "site_name": t.Name(),
		"title": "title-" + hash[:6], "size": float64(10 << 30),
		"freedate": iso(now.Add(2 * time.Hour)), "freedate_diff": nil,
		"downloadvolumefactor": 0, "volume_factor": "免费", "hit_and_run": false,
		"add_on": float64(now.Unix()), "progress": progress, "downloaded": float64(1 << 30),
		"uploaded": float64(0), "ratio": float64(0), "deleted": false,
		"promotion_verified_at": float64(now.Unix()) - 60,
		"promotion_probe_at":    float64(now.Unix()) - 60,
	}
}

func promotionTorrent(t Task, hash string, now time.Time, progress float64) DownloadState {
	return DownloadState{Kind: "qbittorrent", Raw: Row{
		"hash": hash, "name": "title-" + hash[:6], "state": "downloading", "progress": progress,
		"total_size": float64(10 << 30), "downloaded": float64(1 << 30), "uploaded": float64(0),
		"ratio": float64(0), "added_on": now.Unix() - 3600, "completion_on": 0,
		"last_activity": now.Unix() - 60, "tags": "刷流, " + t.Tag(),
	}}
}

func newPromotionCore(t *testing.T, records Rows, torrents []DownloadState) (*Core, *promotionHost, Task) {
	t.Helper()
	now := promotionNow()
	task := promotionTask()
	host := &promotionHost{now: now, data: map[string]json.RawMessage{}, torrents: torrents}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	host.data[taskKey(task.ID(), "torrents")] = raw
	core := newCore(host, Config{Settings: Row{"enabled": true}, Tasks: []Task{task}})
	return core, host, task
}

func TestFailSafePauseKeepsCompletedTorrents(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	staleComplete := "3333333333333333333333333333333333333333"
	stillRunning := "4444444444444444444444444444444444444444"
	records := Rows{
		staleComplete: promotionRecord(task, staleComplete, now, .5),
		stillRunning:  promotionRecord(task, stillRunning, now, .5),
	}
	core, host, _ := newPromotionCore(t, records, nil)
	host.torrentsErr = errors.New("plugin: host_operation_failed")
	host.completed = []DownloadState{promotionTorrent(task, staleComplete, now, 1)}

	if err := core.Check(context.Background(), task.ID(), true); err == nil {
		t.Fatal("a failed list read must still fail the check")
	}
	if len(host.pauses) != 1 || !slices.Equal(host.pauses[0], []string{stillRunning}) {
		t.Fatalf("a completed torrent was paused from stale progress: %v", host.pauses)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || truth(ts.Values[staleComplete]["promotion_paused_at"]) || !truth(ts.Values[stillRunning]["promotion_paused_at"]) {
		t.Fatalf("fail-safe pause state is wrong: %v %v", ts.Values, e)
	}
}

func TestCheckPausesUnfinishedWhenDownloaderListFails(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	incomplete := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	finished := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	records := Rows{
		incomplete: promotionRecord(task, incomplete, now, .5),
		finished:   promotionRecord(task, finished, now, 1),
	}
	core, host, _ := newPromotionCore(t, records, nil)
	host.torrentsErr = errors.New("plugin: host_operation_failed")

	err := core.Check(context.Background(), task.ID(), true)
	if err == nil {
		t.Fatal("a failed daemon read must fail the check")
	}
	if len(host.pauses) != 1 || !slices.Equal(host.pauses[0], []string{incomplete}) {
		t.Fatalf("only the unfinished stored hash may be paused: %v", host.pauses)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[incomplete]["promotion_paused_at"]) ||
		text(ts.Values[incomplete]["promotion_pause_reason"]) != promotionPauseVerify ||
		truth(ts.Values[finished]["promotion_paused_at"]) {
		t.Fatalf("fail-closed pause state not persisted: %v %v", ts.Values, e)
	}
	runs, e := core.runs(context.Background(), task.ID())
	if e != nil || len(runs) != 1 {
		t.Fatalf("one failed check report expected: %v %v", runs, e)
	}
	report := row(runs[0])
	if report["result"] != "downloader_error" || number(report["paused_count"]) != 1 || report["error"] == nil {
		t.Fatalf("failed check report lost the pause or the daemon error: %v", report)
	}
	// The promotion clock must honor the failure backoff instead of retrying
	// the broken daemon every minute.
	reads := host.torrentReads
	if e := core.CheckPromotion(context.Background(), task.ID()); e != nil || host.torrentReads != reads {
		t.Fatalf("promotion clock ignored the failure backoff: %v %d/%d", e, reads, host.torrentReads)
	}
}

func TestPromotionClockDeletesInsideSafetyMargin(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "cccccccccccccccccccccccccccccccccccccccc"
	record := promotionRecord(task, hash, now, .5)
	record["freedate"] = iso(now.Add(15 * time.Minute))
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	if len(host.deletes) != 1 || !slices.Equal(host.deletes[0], []string{hash}) {
		t.Fatalf("a deadline inside the margin must delete before it bills: %v", host.deletes)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["deleted"]) {
		t.Fatalf("deleted record not retained: %v %v", ts.Values[hash], e)
	}
}

func TestCheckSurvivesNotificationChannelFailure(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "2222222222222222222222222222222222222222"
	record := promotionRecord(task, hash, now, .5)
	record["freedate"] = iso(now.Add(15 * time.Minute))
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})
	host.messageErr = errors.New("plugin: host_operation_failed")

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatalf("a notification channel failure disabled the check: %v", err)
	}
	if len(host.deletes) != 1 || !slices.Equal(host.deletes[0], []string{hash}) {
		t.Fatalf("promotion delete did not run: %v", host.deletes)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["deleted"]) {
		t.Fatalf("delete state lost after notification failure: %v %v", ts.Values[hash], e)
	}
}

func TestCheckConfirmsDeleteWhenReceiptIsIncomplete(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "1111111111111111111111111111111111111111"
	record := promotionRecord(task, hash, now, .5)
	record["freedate"] = iso(now.Add(15 * time.Minute))
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})
	host.deleteUncertain = true
	host.goneAfterDelete = true

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatalf("a bookkeeping-only delete gap failed the check: %v", err)
	}
	if len(host.deletes) != 1 || !slices.Equal(host.deletes[0], []string{hash}) {
		t.Fatalf("delete was not attempted: %v", host.deletes)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["deleted"]) {
		t.Fatalf("list-confirmed delete not recorded: %v %v", ts.Values[hash], e)
	}
	runs, e := core.runs(context.Background(), task.ID())
	if e != nil || len(runs) != 1 || number(row(runs[0])["deleted_count"]) != 1 {
		t.Fatalf("delete count not reported: %v %v", runs, e)
	}
}

func TestProtectPromotionLeavesSafePausedRecordForBrushPhase(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "dddddddddddddddddddddddddddddddddddddddd"
	record := promotionRecord(task, hash, now, .5)
	record["promotion_paused_at"] = float64(now.Unix()) - 60
	record["promotion_pause_reason"] = promotionPauseVerify
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	// The host authorizes start only in the brush phase. A check must not try
	// to resume, and must not delete a safe paused record.
	if len(host.starts) != 0 || len(host.pauses) != 0 || len(host.deletes) != 0 {
		t.Fatalf("check changed a safe paused record: starts=%v pauses=%v deletes=%v", host.starts, host.pauses, host.deletes)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["promotion_paused_at"]) || text(ts.Values[hash]["promotion_pause_reason"]) != promotionPauseVerify || truth(ts.Values[hash]["deleted"]) {
		t.Fatalf("safe paused marker changed: %v %v", ts.Values[hash], e)
	}
}

func TestProtectPromotionPausesUnknownDeadline(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	record := promotionRecord(task, hash, now, .5)
	record["freedate"], record["freedate_diff"] = nil, nil
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	if len(host.pauses) != 1 || !slices.Equal(host.pauses[0], []string{hash}) || len(host.deletes) != 0 {
		t.Fatalf("an unprovable free window must pause, not delete: pauses=%v deletes=%v", host.pauses, host.deletes)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["promotion_paused_at"]) || text(ts.Values[hash]["promotion_pause_reason"]) != promotionPauseVerify {
		t.Fatalf("unknown deadline was not recorded as a verification pause: %v %v", ts.Values[hash], e)
	}
}

func TestProtectPromotionDoesNotResumeNeverFreeTorrent(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "ffffffffffffffffffffffffffffffffffffffff"
	record := promotionRecord(task, hash, now, .5)
	record["downloadvolumefactor"], record["volume_factor"] = 1, "普通"
	record["promotion_paused_at"] = float64(now.Unix()) - 60
	record["promotion_pause_reason"] = promotionPauseNotFree
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .5)})

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	if len(host.starts) != 0 {
		t.Fatalf("a record that was never free must stay paused: %v", host.starts)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || !truth(ts.Values[hash]["promotion_paused_at"]) || text(ts.Values[hash]["promotion_pause_reason"]) != promotionPauseNotFree {
		t.Fatalf("non-free pause state changed: %v %v", ts.Values[hash], e)
	}
}

func TestMergeLiveProgressOverridesStaleDetailRow(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "6666666666666666666666666666666666666666"
	core, _, _ := newPromotionCore(t, Rows{}, []DownloadState{promotionTorrent(task, hash, now, .75)})
	row := Row{"hash": hash, "progress": nil, "downloaded": float64(0)}
	core.mergeLiveProgress(context.Background(), task, []any{row})
	if number(row["progress"]) != .75 || number(row["downloaded"]) == 0 {
		t.Fatalf("stale detail row was not overlaid: %v", row)
	}
}

func TestDownloadBudgetRefreshesStoredProgress(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "7777777777777777777777777777777777777777"
	core, _, _ := newPromotionCore(t, Rows{hash: promotionRecord(task, hash, now, .1)}, []DownloadState{promotionTorrent(task, hash, now, .75)})
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil {
		t.Fatal(e)
	}
	budget, e := core.downloadBudget(context.Background(), task, ts)
	if e != nil || budget.taskCount != 1 || number(ts.Values[hash]["progress"]) != .75 {
		t.Fatalf("budget read did not refresh progress: %v %v %v", budget, ts.Values[hash], e)
	}
}

func TestCheckPersistsDaemonProgress(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "5555555555555555555555555555555555555555"
	record := promotionRecord(task, hash, now, .1)
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .75)})
	core.h.(*promotionHost).torrents[0].Raw["downloaded"] = float64(7 << 30)
	core.h.(*promotionHost).torrents[0].Raw["uploaded"] = float64(1 << 30)
	core.h.(*promotionHost).torrents[0].Raw["ratio"] = 0.14

	if err := core.Check(context.Background(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || number(ts.Values[hash]["progress"]) != .75 || number(ts.Values[hash]["downloaded"]) != float64(7<<30) {
		t.Fatalf("daemon progress was not persisted: %v %v", ts.Values[hash], e)
	}
	if host.torrentReads == 0 {
		t.Fatal("no daemon read")
	}
}

func TestCheckPersistsProgressBeforeLaterFailure(t *testing.T) {
	now := promotionNow()
	task := promotionTask()
	hash := "8888888888888888888888888888888888888888"
	record := promotionRecord(task, hash, now, .1)
	record["freedate"] = iso(now.Add(15 * time.Minute))
	core, host, _ := newPromotionCore(t, Rows{hash: record}, []DownloadState{promotionTorrent(task, hash, now, .75)})
	host.deleteUncertain = true

	if err := core.Check(context.Background(), task.ID(), true); err == nil {
		t.Fatal("a present, unconfirmed delete must still fail the check")
	}
	ts, e := core.records(context.Background(), task.ID(), "torrents")
	if e != nil || number(ts.Values[hash]["progress"]) != .75 {
		t.Fatalf("progress was lost when the later delete failed: %v %v", ts.Values[hash], e)
	}
}
