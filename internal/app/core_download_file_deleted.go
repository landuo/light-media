package app

// Source: MoviePilot app/chain/download.py DownloadChain.download_file_deleted.
// The source emits DownloadDeleted whenever its tagged list is nonempty, even
// if remove_torrents returns False. Source publication and daemon confirmation
// are deliberately separate facts. A src-only event is an exact source no-op.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"light-media/internal/download"
)

type historyDownloadIdentity struct {
	Hash, AcceptedID, AcceptedDigest string
}

type historyDownloadFileFact struct {
	DirectoryRetained                  bool `json:"DirectoryRetained,omitempty"`
	Version                            int
	ID, HistoryID, PlanID, FileID, Src string
	Snapshot                           importSnapshot
	Identity                           historyDownloadIdentity
	SettingsDigest                     string
}

type coreDownloadRemoval struct {
	Version                                                   int
	ID, JobID, SourceDigest, State, DefaultID, EndpointDigest string
	// Raw objects are private and sealed. The source event carries only the
	// normalized original DownloaderTorrent projection, never credentials.
	Raw            map[string]json.RawMessage
	Torrents       []map[string]any
	SourceReturned bool
	SourceReturn   *bool
	EventID        string
}

type coreDownloadRemovalRoot struct {
	Version                int
	ID, JobID, Digest, Tip string
	Sequence               int64
}
type coreDownloadRemovalEntry struct {
	Version                                          int
	ID, JobID, SourceDigest, Previous, Digest, State string
	Sequence                                         int64
}

func coreDeleteReview() error {
	return bad("CORE_DOWNLOAD_DELETE_REVIEW", "下载器操作结果或持久证据需要核对，未重新发送删除")
}
func coreDeleteInvalid() error {
	return bad("CORE_DOWNLOAD_DELETE_INVALID", "文件删除与下载历史的可信绑定无法核对，未操作下载器")
}
func coreDeleteDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func coreDeleteSettingsDigest(c Settings) string {
	// Include credentials and enabled/default selection in the private digest:
	// a queued old operation must not silently target a changed endpoint/account.
	b, _ := json.Marshal(struct {
		Downloaders []DownloaderConfig
		Default     string
	}{c.Downloaders, c.DefaultDownloader})
	return coreDeleteDigest(b)
}

// The client cannot introduce a hash in a history-delete request. Resolve only
// the actual linked import intent and its authenticated accepted download fact.
func (a *App) historyDownloadIdentity(ctx context.Context, item transferHistoryItem) (historyDownloadIdentity, error) {
	var out historyDownloadIdentity
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
		return out, err
	}
	selected, _, _, err := a.historyPlannedFile(ctx, plan, item)
	if err != nil || selected.Source != item.Source {
		return out, coreDeleteInvalid()
	}
	if plan.IntentID == "" {
		return out, nil
	}
	var in Intent
	if err := a.Store.Get(ctx, "intent", plan.IntentID, &in); err != nil {
		return out, err
	}
	if validImportIntent(plan, in) != nil || in.ImportPlanID != plan.ID || in.Hash == "" {
		return out, coreDeleteInvalid()
	}
	acceptedID := domainEventID("download.accepted", in.ID)
	var eventRaw, sealed []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, acceptedID).Scan(&eventRaw); err != nil {
		return out, coreDeleteInvalid()
	}
	var event DomainEvent
	if json.Unmarshal(eventRaw, &event) != nil {
		return out, coreDeleteInvalid()
	}
	accepted, err := a.downloadAddedSnapshot(ctx, event)
	if err != nil || accepted.DownloadKind != "intent" || accepted.DownloadID != in.ID || !sameHash(accepted.Hash, in.Hash) {
		return out, coreDeleteInvalid()
	}
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_added' AND id=?`, acceptedID).Scan(&sealed); err != nil {
		return out, err
	}
	return historyDownloadIdentity{Hash: accepted.Hash, AcceptedID: acceptedID, AcceptedDigest: coreDeleteDigest(sealed)}, nil
}

func (a *App) publishHistoryDownloadFileDeleted(ctx context.Context, item transferHistoryItem) error {
	var removal historySourceRemoval
	if err := a.Store.Get(ctx, "history_source_removal", item.executionFileID(), &removal); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !removal.succeeded() || removal.File.Path != item.Source || removal.HistoryID != item.executionFileID() {
		return coreDeleteInvalid()
	}
	identity, err := a.historyDownloadIdentity(ctx, item)
	if err != nil {
		return err
	}
	// The fixed endpoint publishes after a successful directory no-op too.
	// Keep that fact distinct from physical deletion; downloader ownership
	// checks and delete_file=False remain unchanged.
	fact := historyDownloadFileFact{Version: 1, HistoryID: item.ID, PlanID: item.PlanID, FileID: item.executionFileID(), Src: item.Source, Snapshot: removal.File.Snapshot, Identity: identity, SettingsDigest: coreDeleteSettingsDigest(a.Settings()), DirectoryRetained: removal.File.DirectoryRetained}
	stableFact := fact
	stableFact.SettingsDigest = ""
	stable, _ := json.Marshal(stableFact)
	fact.ID = "hfe_" + hex.EncodeToString(a.pluginStore.fileHistoryMAC(stable))
	raw, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("history_download_file_event", "system", fact.ID, raw)
	if err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='history_download_file_event' AND id=?`, fact.ID).Scan(&old)
	if err == nil {
		opened, e := a.pluginStore.unseal("history_download_file_event", "system", fact.ID, old)
		var priorFact historyDownloadFileFact
		if e != nil || json.Unmarshal(opened, &priorFact) != nil {
			return coreDeleteInvalid()
		}
		comparePrior := priorFact
		comparePrior.SettingsDigest = ""
		compareNext := fact
		compareNext.SettingsDigest = ""
		comparePriorRaw, _ := json.Marshal(comparePrior)
		compareNextRaw, _ := json.Marshal(compareNext)
		if !bytes.Equal(comparePriorRaw, compareNextRaw) {
			return coreDeleteInvalid()
		}
		if _, e = a.pluginStore.historyDownloadFileFact(ctx, tx, fact.ID); e != nil {
			return e
		}
		if identity.Hash != "" {
			if _, e = a.readCoreDownloadRemoval(ctx, tx, fact.ID); e != nil {
				return e
			}
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Existing jobs/events without their source fact are evidence loss, never a
	// new initial operation. This is distinct from whole-database rollback.
	var prior int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM domain_events WHERE event_id=?)+(SELECT COUNT(*) FROM jobs WHERE kind='download_file_deleted' AND json_extract(payload,'$.id')=?)`, domainEventID("downloadfile.deleted", fact.ID), fact.ID).Scan(&prior); err != nil {
		return err
	}
	if prior != 0 {
		return coreDeleteReview()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('history_download_file_event',?,?,?)`, fact.ID, sealed, now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_download_file_events(file_event_id,instance_id,generation,sealed) VALUES(?,'system',1,?)`, fact.ID, sealed); err != nil {
		return err
	}
	if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("downloadfile.deleted", fact.ID), Type: "downloadfile.deleted", FileEventID: fact.ID}); err != nil {
		return err
	}
	if identity.Hash != "" {
		job, e := enqueueTx(ctx, tx, "download_file_deleted", map[string]string{"id": fact.ID}, item.WorkflowID)
		if e != nil {
			return e
		}
		r := coreDownloadRemoval{Version: 1, ID: fact.ID, JobID: job.ID, SourceDigest: coreDeleteDigest(raw), State: "planned", Raw: map[string]json.RawMessage{}, Torrents: []map[string]any{}}
		if e = a.writeCoreDownloadRemoval(ctx, tx, r, true); e != nil {
			return e
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.emitStandardBusinessEventCoreState(ctx, "downloadfile.deleted", map[string]any{"src": fact.Src, "hash": fact.Identity.Hash}, true)
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return nil
}

func (p *PluginStore) fileHistoryMAC(raw []byte) []byte {
	h := hmac.New(sha256.New, p.store.key)
	h.Write([]byte("history-download-file-event/v1\x00"))
	h.Write(raw)
	return h.Sum(nil)
}

func (p *PluginStore) historyDownloadFileFact(ctx context.Context, q pluginRemoveLinkJournalReader, key string) (historyDownloadFileFact, error) {
	var f historyDownloadFileFact
	if !strings.HasPrefix(key, "hfe_") || !pluginFileEventIDPattern.MatchString(key) {
		return f, coreDeleteInvalid()
	}
	var sealed, tableSealed []byte
	if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='history_download_file_event' AND id=?`, key).Scan(&sealed); err != nil {
		return f, err
	}
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_download_file_events WHERE file_event_id=? AND instance_id='system' AND generation=1`, key).Scan(&tableSealed); err != nil {
		return f, err
	}
	if !bytes.Equal(sealed, tableSealed) {
		return f, coreDeleteInvalid()
	}
	raw, err := p.unseal("history_download_file_event", "system", key, sealed)
	if err != nil {
		return f, err
	}
	if json.Unmarshal(raw, &f) != nil || f.Version != 1 || f.ID != key || f.FileID == "" || f.PlanID == "" || !importPath(f.Src) {
		return f, coreDeleteInvalid()
	}
	identity := f
	identity.ID = ""
	identity.SettingsDigest = ""
	stable, _ := json.Marshal(identity)
	if key != "hfe_"+hex.EncodeToString(p.fileHistoryMAC(stable)) {
		return f, coreDeleteInvalid()
	}
	var sourceEventRaw []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("downloadfile.deleted", key)).Scan(&sourceEventRaw); err != nil {
		return f, err
	}
	var sourceEvent DomainEvent
	if json.Unmarshal(sourceEventRaw, &sourceEvent) != nil || sourceEvent.Type != "downloadfile.deleted" || sourceEvent.FileEventID != key || sourceEvent.EventID != domainEventID("downloadfile.deleted", key) {
		return f, coreDeleteInvalid()
	}
	if _, err = validateDomainEvent(sourceEvent); err != nil {
		return f, err
	}
	var planRaw, removalRaw []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_plan' AND id=?`, f.PlanID).Scan(&planRaw); err != nil {
		return f, err
	}
	var plan ImportPlan
	if json.Unmarshal(planRaw, &plan) != nil || plan.ID != f.PlanID {
		return f, coreDeleteInvalid()
	}
	matched := false
	for _, file := range plan.Files {
		if file.ID == f.FileID && file.Source == f.Src && planSnapshot(file) == f.Snapshot {
			matched = true
			break
		}
	}
	if !matched {
		return f, coreDeleteInvalid()
	}
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='history_source_removal' AND id=?`, f.FileID).Scan(&removalRaw); err != nil {
		return f, err
	}
	var removal historySourceRemoval
	if json.Unmarshal(removalRaw, &removal) != nil || removal.HistoryID != f.FileID || !removal.succeeded() || removal.File.DirectoryRetained != f.DirectoryRetained || removal.File.Snapshot != f.Snapshot || removal.File.Path != f.Src {
		return f, coreDeleteInvalid()
	}
	if f.Identity.Hash != "" {
		var acceptedSealed, eventRaw, intentRaw []byte
		if f.Identity.AcceptedID != domainEventID("download.accepted", plan.IntentID) || plan.IntentID == "" {
			return f, coreDeleteInvalid()
		}
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_added' AND id=?`, f.Identity.AcceptedID).Scan(&acceptedSealed); err != nil {
			return f, err
		}
		if coreDeleteDigest(acceptedSealed) != f.Identity.AcceptedDigest {
			return f, coreDeleteInvalid()
		}
		opened, e := p.unseal("download_added", "system", f.Identity.AcceptedID, acceptedSealed)
		if e != nil {
			return f, e
		}
		var accepted downloadAddedSnapshot
		if json.Unmarshal(opened, &accepted) != nil || accepted.DownloadKind != "intent" || accepted.DownloadID != plan.IntentID || !sameHash(accepted.Hash, f.Identity.Hash) {
			return f, coreDeleteInvalid()
		}
		if err = q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, f.Identity.AcceptedID).Scan(&eventRaw); err != nil {
			return f, err
		}
		var event DomainEvent
		if json.Unmarshal(eventRaw, &event) != nil || event.SubjectID != accepted.DownloadID || event.JobID != accepted.JobID || event.Type != "download.accepted" {
			return f, coreDeleteInvalid()
		}
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, plan.IntentID).Scan(&intentRaw); err != nil {
			return f, err
		}
		var in Intent
		if json.Unmarshal(intentRaw, &in) != nil || in.ID != accepted.DownloadID || !sameHash(in.Hash, f.Identity.Hash) {
			return f, coreDeleteInvalid()
		}
	} else if f.Identity.AcceptedID != "" || f.Identity.AcceptedDigest != "" {
		return f, coreDeleteInvalid()
	}
	return f, nil
}

func (a *App) writeCoreDownloadRemoval(ctx context.Context, tx *sql.Tx, r coreDownloadRemoval, initialize bool) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("core_download_removal", "system", r.ID, raw)
	if err != nil {
		return err
	}
	root := coreDownloadRemovalRoot{Version: 1, ID: r.ID, JobID: r.JobID, Digest: coreDeleteDigest(raw), Sequence: 1}
	if initialize {
		var prior int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE (kind IN ('core_download_removal','core_download_removal_root') AND id=?) OR (kind='core_download_removal_chain' AND substr(id,1,69)=?)`, r.ID, r.ID+":").Scan(&prior); err != nil {
			return err
		}
		if prior != 0 || r.State != "planned" {
			return coreDeleteReview()
		}
	} else {
		var previousSealed []byte
		var previous coreDownloadRemovalRoot
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='core_download_removal_root' AND id=?`, r.ID).Scan(&previousSealed); err != nil {
			return coreDeleteReview()
		}
		previousRaw, e := a.pluginStore.unseal("core_download_removal_root", "system", r.ID, previousSealed)
		if e != nil || json.Unmarshal(previousRaw, &previous) != nil || previous.ID != r.ID || previous.JobID != r.JobID || previous.Sequence < 1 || previous.Sequence >= 3 {
			return coreDeleteReview()
		}
		root.Sequence = previous.Sequence + 1
		root.Tip = previous.Tip
	}
	entry := coreDownloadRemovalEntry{Version: 1, ID: r.ID, JobID: r.JobID, SourceDigest: r.SourceDigest, Previous: root.Tip, Digest: root.Digest, State: r.State, Sequence: root.Sequence}
	entryRaw, _ := json.Marshal(entry)
	entryKey := fmt.Sprintf("%s:%012d", r.ID, entry.Sequence)
	entrySealed, e := a.pluginStore.seal("core_download_removal_chain", "system", entryKey, entryRaw)
	if e != nil {
		return e
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('core_download_removal_chain',?,?,?)`, entryKey, entrySealed, now()); err != nil {
		return err
	}
	root.Tip = coreDeleteDigest(entryRaw)
	rootRaw, _ := json.Marshal(root)
	rootSealed, err := a.pluginStore.seal("core_download_removal_root", "system", r.ID, rootRaw)
	if err != nil {
		return err
	}
	for _, row := range []struct {
		kind string
		body []byte
	}{{"core_download_removal", sealed}, {"core_download_removal_root", rootSealed}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, row.kind, r.ID, row.body, now()); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) readCoreDownloadRemoval(ctx context.Context, q pluginRemoveLinkJournalReader, key string) (coreDownloadRemoval, error) {
	var r coreDownloadRemoval
	var sealed, rootSealed []byte
	if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='core_download_removal' AND id=?`, key).Scan(&sealed); err != nil {
		return r, coreDeleteReview()
	}
	if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='core_download_removal_root' AND id=?`, key).Scan(&rootSealed); err != nil {
		return r, coreDeleteReview()
	}
	raw, err := a.pluginStore.unseal("core_download_removal", "system", key, sealed)
	if err != nil {
		return r, err
	}
	rootRaw, err := a.pluginStore.unseal("core_download_removal_root", "system", key, rootSealed)
	if err != nil {
		return r, err
	}
	var root coreDownloadRemovalRoot
	if json.Unmarshal(raw, &r) != nil || json.Unmarshal(rootRaw, &root) != nil || root.Version != 1 || r.Version != 1 || root.ID != key || r.ID != key || root.JobID != r.JobID || root.Digest != coreDeleteDigest(raw) {
		return r, coreDeleteReview()
	}
	if root.Sequence < 1 || root.Sequence > 3 {
		return r, coreDeleteReview()
	}
	var total int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='core_download_removal_chain' AND substr(id,1,69)=?`, key+":").Scan(&total); err != nil {
		return r, err
	}
	if int64(total) != root.Sequence {
		return r, coreDeleteReview()
	}
	tip, priorState := "", ""
	for sequence := int64(1); sequence <= root.Sequence; sequence++ {
		entryKey := fmt.Sprintf("%s:%012d", key, sequence)
		var entrySealed []byte
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='core_download_removal_chain' AND id=?`, entryKey).Scan(&entrySealed); err != nil {
			return r, coreDeleteReview()
		}
		entryRaw, e := a.pluginStore.unseal("core_download_removal_chain", "system", entryKey, entrySealed)
		if e != nil {
			return r, e
		}
		var entry coreDownloadRemovalEntry
		if json.Unmarshal(entryRaw, &entry) != nil || entry.Version != 1 || entry.ID != key || entry.JobID != r.JobID || entry.Sequence != sequence || entry.Previous != tip || entry.SourceDigest != r.SourceDigest {
			return r, coreDeleteReview()
		}
		if sequence == 1 && entry.State != "planned" {
			return r, coreDeleteReview()
		}
		if sequence == 2 && entry.State != "dispatching" && entry.State != "no_match" && entry.State != "default_unavailable" {
			return r, coreDeleteReview()
		}
		if sequence == 3 && (priorState != "dispatching" || entry.State == "planned" || entry.State == "dispatching") {
			return r, coreDeleteReview()
		}
		tip = coreDeleteDigest(entryRaw)
		priorState = entry.State
		if sequence == root.Sequence && (entry.Digest != root.Digest || entry.State != r.State) {
			return r, coreDeleteReview()
		}
	}
	if tip != root.Tip {
		return r, coreDeleteReview()
	}
	f, err := a.pluginStore.historyDownloadFileFact(ctx, q, key)
	if err != nil {
		return r, err
	}
	factRaw, _ := json.Marshal(f)
	if f.Identity.Hash == "" || r.SourceDigest != coreDeleteDigest(factRaw) {
		return r, coreDeleteInvalid()
	}
	var payload []byte
	var kind, state string
	if err = q.QueryRowContext(ctx, `SELECT kind,state,payload FROM jobs WHERE id=?`, r.JobID).Scan(&kind, &state, &payload); err != nil {
		return r, err
	}
	var p struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &p) != nil || kind != "download_file_deleted" || p.ID != key {
		return r, coreDeleteInvalid()
	}
	return r, nil
}

func (a *App) runDownloadFileDeleted(ctx context.Context, job Job, key string) error {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	r, err := a.readCoreDownloadRemoval(ctx, a.Store.DB, key)
	if err != nil {
		return err
	}
	if r.JobID != job.ID || a.jobCancelled(ctx, job) != nil {
		return coreDeleteInvalid()
	}
	if r.State != "planned" {
		if r.State == "dispatching" || r.State == "unknown" {
			return coreDeleteReview()
		}
		return nil
	}
	fact, err := a.pluginStore.historyDownloadFileFact(ctx, a.Store.DB, key)
	if err != nil {
		return err
	}
	settings := a.Settings()
	if fact.SettingsDigest != coreDeleteSettingsDigest(settings) {
		return coreDeleteReview()
	}
	clients := map[string]*download.Client{}
	for _, d := range settings.Downloaders {
		if !downloaderEnabled(d) || coreDownloaderKind(d.Kind) == "" {
			continue
		}
		client, e := download.New(d.Config)
		if e == nil {
			clients[d.ID] = client
		}
	}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		kindItems := []map[string]any{}
		for _, d := range settings.Downloaders {
			if coreDownloaderKind(d.Kind) != kind || clients[d.ID] == nil {
				continue
			}
			raw, e := clients[d.ID].SourceTorrentSnapshots(ctx, fact.Identity.Hash)
			if e != nil {
				a.event(ctx, job, "WARN", "download", "file_deleted_list_failed", "下载器列表读取失败；保留原版空结果行为，未推断删种成功")
				continue
			}
			r.Raw[d.ID] = append(json.RawMessage(nil), raw...)
			items, e := a.coreDeletedTorrentSnapshots(ctx, d, fact.Identity.Hash, raw)
			if e != nil {
				kindItems = nil
				a.event(ctx, job, "WARN", "download", "file_deleted_projection_failed", "下载器模块无法构造原版任务快照；已保留该模块失败并继续其他模块")
				break
			}
			kindItems = append(kindItems, items...)
		}
		r.Torrents = append(r.Torrents, kindItems...)
	}
	if len(r.Torrents) == 0 {
		a.event(ctx, job, "INFO", "download", "file_deleted_no_match", "没有符合内置标签与原历史标识的下载任务，未发送删种请求")
		r.State = "no_match"
		return a.commitCoreDownloadRemoval(ctx, r, false)
	}
	var target *DownloaderConfig
	if settings.DefaultDownloader != "" {
		for i := range settings.Downloaders {
			if settings.Downloaders[i].ID == settings.DefaultDownloader {
				target = &settings.Downloaders[i]
				break
			}
		}
	} else if len(settings.Downloaders) > 0 {
		target = &settings.Downloaders[0]
	}
	if target == nil || !downloaderEnabled(*target) || clients[target.ID] == nil {
		r.State, r.SourceReturned = "default_unavailable", true
		return a.commitCoreDownloadRemoval(ctx, r, true)
	}
	r.EndpointDigest = coreDeleteEndpointDigest(*target)
	if err = a.guardCoreDownloadUnknown(ctx, r, fact.Identity.Hash); err != nil {
		return err
	}
	a.event(ctx, job, "WARN", "download", "file_deleted_removing", "根据已删除源文件的历史通知，向默认下载器请求保留数据删种")
	r.DefaultID, r.State = target.ID, "dispatching"
	if err = a.commitCoreDownloadRemoval(ctx, r, false); err != nil {
		return err
	}
	// The durable dispatching checkpoint is a no-replay boundary. No retry can
	// turn a lost acknowledgement or missing result row into another DELETE.
	err = clients[target.ID].SourceRemoveKeepData(ctx, fact.Identity.Hash)
	returned := err == nil
	r.SourceReturn, r.SourceReturned = &returned, true
	if err != nil {
		if download.MutationNotStarted(err) {
			r.State = "not_started"
		} else {
			r.State = "unknown"
		}
	} else {
		r.State = "acknowledged"
		check, e := clients[target.ID].SourceTorrentSnapshots(ctx, fact.Identity.Hash)
		if e != nil {
			r.State = "unknown"
		} else {
			present, e := coreNativeContains(coreDownloaderKind(target.Kind), fact.Identity.Hash, check)
			if e != nil {
				r.State = "unknown"
			} else if !present {
				r.State = "confirmed_absent"
				if before, ok := r.Raw[target.ID]; ok {
					wasPresent, readErr := coreNativeContains(coreDownloaderKind(target.Kind), fact.Identity.Hash, before)
					if readErr == nil {
						if wasPresent {
							r.State = "confirmed"
						} else {
							r.State = "already_absent"
						}
					}
				}
			}
		}
	}
	// Source False still publishes its original snapshot. This publication does
	// not claim that every listed instance (or even the default) was removed.
	if e := a.commitCoreDownloadRemoval(context.WithoutCancel(ctx), r, true); e != nil {
		return e
	}
	if r.State == "unknown" {
		a.event(context.WithoutCancel(ctx), job, "WARN", "download", "file_deleted_unknown", "删种源事件已提交，但下载器结果未知；已保留证据，后续不会重复发送删除")
		return coreDeleteReview()
	}
	a.event(ctx, job, "INFO", "download", "file_deleted_source_published", "已按原版触发条件发布源事件；下载器确认结果单独保存在操作收据中")
	return nil
}

func coreDownloaderKind(kind string) string {
	switch kind {
	case "qb", "qbittorrent":
		return "qbittorrent"
	case "tr", "transmission":
		return "transmission"
	}
	return ""
}

func (a *App) commitCoreDownloadRemoval(ctx context.Context, r coreDownloadRemoval, publish bool) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prior, err := a.readCoreDownloadRemoval(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if prior.JobID != r.JobID || prior.SourceDigest != r.SourceDigest || (prior.State != "planned" && prior.State != "dispatching") {
		return coreDeleteReview()
	}
	if publish {
		if !r.SourceReturned || len(r.Torrents) == 0 {
			return coreDeleteInvalid()
		}
		r.EventID = "dde_" + r.ID[4:]
	}
	if err = a.writeCoreDownloadRemoval(ctx, tx, r, false); err != nil {
		return err
	}
	if publish {
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("download.deleted", r.EventID), Type: "download.deleted", DownloadDeletedID: r.EventID}); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if publish {
		a.emitStandardDownloadDeleted(ctx, r.EventID)
	}
	return nil
}

func validateDownloadDeletedDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) { return in, bad("INVALID_DOMAIN_EVENT", "删种源事件身份无效") }
	if in.PluginTriggerID != "" || in.SourceSiteID != 0 || in.NoticeID != "" || in.FileEventID != "" || (in.SubjectKind != "" && in.SubjectKind != "download_deleted") || (in.SubjectID != "" && in.SubjectID != in.DownloadDeletedID) || (in.Purpose != "" && in.Purpose != "download_deletion") {
		return invalid()
	}
	if in.Type != "download.deleted" || len(in.DownloadDeletedID) != 68 || !strings.HasPrefix(in.DownloadDeletedID, "dde_") || !pluginStorageDigest.MatchString(in.DownloadDeletedID[4:]) || in.EventID != domainEventID(in.Type, in.DownloadDeletedID) {
		return invalid()
	}
	copy := in
	copy.Type = "downloadfile.deleted"
	copy.DownloadDeletedID = ""
	copy.FileEventID = "hfe_" + in.DownloadDeletedID[4:]
	copy.EventID = domainEventID(copy.Type, copy.FileEventID)
	if copy.SubjectKind == "download_deleted" {
		copy.SubjectKind = "file_event"
	}
	if copy.SubjectID == in.DownloadDeletedID {
		copy.SubjectID = copy.FileEventID
	}
	if copy.Purpose == "download_deletion" {
		copy.Purpose = "file_deletion"
	}
	copy, err := validateDownloadFileDomainEvent(copy)
	if err != nil {
		return invalid()
	}
	in.SchemaVersion, in.OccurredAt, in.Origin = copy.SchemaVersion, copy.OccurredAt, copy.Origin
	in.SubjectKind, in.SubjectID, in.Purpose = "download_deleted", in.DownloadDeletedID, "download_deletion"
	return in, nil
}

func (a *App) downloadDeletedSourceData(ctx context.Context, q pluginRemoveLinkJournalReader, key string) (map[string]any, error) {
	if len(key) != 68 || !strings.HasPrefix(key, "dde_") {
		return nil, coreDeleteInvalid()
	}
	r, err := a.readCoreDownloadRemoval(ctx, q, "hfe_"+key[4:])
	if err != nil {
		return nil, err
	}
	if r.EventID != key || !r.SourceReturned || len(r.Torrents) == 0 || r.State == "planned" || r.State == "dispatching" {
		return nil, coreDeleteInvalid()
	}
	fact, err := a.pluginStore.historyDownloadFileFact(ctx, q, r.ID)
	if err != nil {
		return nil, err
	}
	var eventRaw []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("download.deleted", key)).Scan(&eventRaw); err != nil {
		return nil, err
	}
	var event DomainEvent
	if json.Unmarshal(eventRaw, &event) != nil || event.Type != "download.deleted" || event.DownloadDeletedID != key || event.EventID != domainEventID("download.deleted", key) {
		return nil, coreDeleteInvalid()
	}
	if _, err = validateDomainEvent(event); err != nil {
		return nil, err
	}
	return map[string]any{"hash": fact.Identity.Hash, "torrents": r.Torrents}, nil
}

// A tagged source belonging to an authenticated accepted intent can be deleted
// by the explicit original history action. Unrelated overlapping tasks retain
// the existing protection. No path-to-hash inference is involved.
func (a *App) historyDownloadTaskAuthorized(ctx context.Context, item transferHistoryItem, d DownloaderConfig, task download.Task) (bool, error) {
	identity, err := a.historyDownloadIdentity(ctx, item)
	if err != nil {
		return false, err
	}
	if identity.Hash == "" || !sameHash(identity.Hash, task.Hash) {
		return false, nil
	}
	var plan ImportPlan
	if err = a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
		return false, err
	}
	var in Intent
	if err = a.Store.Get(ctx, "intent", plan.IntentID, &in); err != nil {
		return false, err
	}
	if in.DownloaderID != d.ID || in.DownloaderSnapshot != downloaderSnapshot(d) {
		return false, nil
	}
	client, err := download.New(d.Config)
	if err != nil {
		return false, err
	}
	files, err := client.Files(ctx, task.Hash)
	if err != nil {
		return false, err
	}
	for _, file := range files {
		path, e := mapDownloaderPath(a.Settings(), d, filepath.Join(task.DownloadDir, filepath.FromSlash(file.Path)))
		if e == nil && filepath.Clean(path) == filepath.Clean(item.Source) {
			return true, nil
		}
	}
	return false, nil
}

func validateDownloadDeletedBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records r JOIN records a ON a.id=r.id AND a.kind='core_download_removal_root' WHERE r.kind='core_download_removal' AND r.id=?`, "hfe_"+event.DownloadDeletedID[4:]).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return coreDeleteInvalid()
	}
	return nil
}

// Names/IDs do not partition effects at the same configured daemon endpoint.
// This is a remote operation identity, not an application filesystem grant.
func coreDeleteEndpointDigest(d DownloaderConfig) string {
	endpoint := strings.TrimRight(d.BaseURL, "/")
	if parsed, err := url.Parse(d.BaseURL); err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		if coreDownloaderKind(d.Kind) == "transmission" && parsed.Path == "" {
			parsed.Path = "/transmission/rpc"
		}
		endpoint = parsed.String()
	}
	return coreDeleteDigest([]byte(coreDownloaderKind(d.Kind) + "\x00" + endpoint))
}

// Several deleted episodes can belong to the same torrent. Their source events
// remain distinct, but a queued later event cannot replay an unresolved remote
// removal. Read immutable source facts in bounded pages: a missing result/root
// is evidence loss, never permission to omit an earlier unknown operation.
// Caller holds downloadMu, including settings publication and other core work.
func (a *App) guardCoreDownloadUnknown(ctx context.Context, current coreDownloadRemoval, hash string) error {
	after := ""
	for {
		rows, err := a.Store.DB.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='history_download_file_event' AND id>? ORDER BY id LIMIT 100`, after)
		if err != nil {
			return err
		}
		type factRow struct {
			ID     string
			Sealed []byte
		}
		page := []factRow{}
		for rows.Next() {
			var row factRow
			if err = rows.Scan(&row.ID, &row.Sealed); err != nil {
				break
			}
			page = append(page, row)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, row := range page {
			after = row.ID
			if row.ID == current.ID {
				continue
			}
			raw, e := a.pluginStore.unseal("history_download_file_event", "system", row.ID, row.Sealed)
			if e != nil {
				return e
			}
			var f historyDownloadFileFact
			if json.Unmarshal(raw, &f) != nil || f.Version != 1 || f.ID != row.ID {
				return coreDeleteInvalid()
			}
			if f.Identity.Hash == "" || !sameHash(f.Identity.Hash, hash) {
				continue
			}
			previous, e := a.readCoreDownloadRemoval(ctx, a.Store.DB, row.ID)
			if e != nil {
				return coreDeleteReview()
			}
			if (previous.State == "unknown" || previous.State == "dispatching") && (previous.EndpointDigest == current.EndpointDigest || previous.EndpointDigest == "") {
				return coreDeleteReview()
			}
		}
		if len(page) < 100 {
			return nil
		}
	}
}
