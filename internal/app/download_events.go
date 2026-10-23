package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

// The raw context and recognition inputs are private and frozen before Add.
// The parsed fields are frozen too, so later parser/configuration changes
// cannot reinterpret an accepted download. Raw inputs stay private for audit.
type downloadContextSnapshot struct {
	SourceMeta   json.RawMessage          `json:"source_meta,omitempty"`
	DownloadID   string                   `json:"download_id"`
	DownloadKind string                   `json:"download_kind"`
	Hash         string                   `json:"hash"`
	Title        string                   `json:"title"`
	Subtitle     *string                  `json:"subtitle"`
	Options      releasemeta.Options      `json:"options"`
	Metadata     *subscribegroup.Metadata `json:"metadata"`
	Seasons      string                   `json:"seasons"`
}
type downloadAddedSnapshot struct {
	JobID        string                   `json:"job_id"`
	WorkflowID   string                   `json:"workflow_id"`
	DownloadID   string                   `json:"download_id"`
	DownloadKind string                   `json:"download_kind"`
	Hash         string                   `json:"hash"`
	SiteID       string                   `json:"site_id"`
	MediaType    string                   `json:"media_type"`
	TMDBID       int64                    `json:"tmdb_id"`
	Seasons      string                   `json:"seasons"`
	Metadata     *subscribegroup.Metadata `json:"metadata"`
	HasContext   bool                     `json:"has_context"`
	HasHistory   bool                     `json:"has_history"`
	Context      *downloadContextSnapshot `json:"context,omitempty"`
}

func (a *App) prepareDownloadContext(ctx context.Context, kind, key, hash string, resource sites.Resource) (downloadContextSnapshot, error) {
	ctx, err := a.ensureResourceRecognition(ctx)
	if err != nil {
		return downloadContextSnapshot{}, err
	}
	subtitle := resource.SourceSubtitle
	if subtitle == nil && resource.Subtitle != "" {
		text := resource.Subtitle
		subtitle = &text
	}
	if subtitle != nil {
		text := *subtitle
		subtitle = &text
	}
	options := ctx.Value(resourceRecognitionKey{}).(resourceRecognition).options
	parsed, err := releasemeta.Parse(resource.Title, subtitle, options)
	if err != nil {
		return downloadContextSnapshot{}, bad("RESOURCE_METADATA_FAILED", "下载资源元数据解析失败，请检查资源识别配置")
	}
	metadata := &subscribegroup.Metadata{Resolution: parsed.Resolution, Quality: parsed.Quality, Effect: parsed.Effect, Team: parsed.Team, Customization: parsed.Customization}
	return downloadContextSnapshot{DownloadID: key, DownloadKind: kind, Hash: hash, Title: resource.Title, Subtitle: subtitle, Options: options, Metadata: metadata, Seasons: parsed.Season}, nil
}
func (a *App) saveDownloadContextTx(ctx context.Context, tx *sql.Tx, snapshot downloadContextSnapshot) error {
	key := snapshot.DownloadKind + ":" + snapshot.DownloadID
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	// Leave room for the accepted fact's fixed identity envelope. Reject
	// before Add rather than storing a context that cannot be read back.
	if len(raw) > pluginDataValueLimit-(8<<10) {
		return bad("DOWNLOAD_CONTEXT_TOO_LARGE", "下载识别上下文超过允许范围，尚未提交下载")
	}
	sealed, err := a.pluginStore.seal("download_context", "system", key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('download_context',?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, key, sealed, now())
	return err
}
func (a *App) loadDownloadContext(ctx context.Context, kind, key, hash string) (*downloadContextSnapshot, error) {
	identity := kind + ":" + key
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_context' AND id=?`, identity).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} // Pre-producer legacy rows have no source Context.
	if err != nil {
		return nil, err
	}
	raw, err := a.pluginStore.unseal("download_context", "system", identity, sealed)
	if err != nil {
		return nil, err
	}
	var snapshot downloadContextSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.DownloadID != key || snapshot.DownloadKind != kind || !sameHash(snapshot.Hash, hash) {
		return nil, bad("INVALID_DOWNLOAD_CONTEXT", "下载上下文身份无法核对")
	}
	return &snapshot, nil
}
func (a *App) recordDownloadAddedTx(ctx context.Context, tx *sql.Tx, event DomainEvent, snapshot downloadAddedSnapshot) error {
	if tx == nil {
		return bad("DOMAIN_EVENT_TX_REQUIRED", "下载事实必须与业务状态在同一事务提交")
	}
	valid, err := validateDomainEvent(event)
	if err != nil || valid.Type != "download.accepted" || valid.SubjectID != snapshot.DownloadID || valid.SubjectKind != snapshot.DownloadKind {
		return bad("INVALID_DOWNLOAD_EVENT", "下载事实与上下文身份不符")
	}
	mediaID := ""
	if validMedia(snapshot.MediaType, snapshot.TMDBID) {
		mediaID = snapshot.MediaType + ":" + strconv.FormatInt(snapshot.TMDBID, 10)
	}
	if valid.MediaID != mediaID {
		return bad("INVALID_DOWNLOAD_EVENT", "下载事实与影视身份不符")
	}
	snapshot.JobID, snapshot.WorkflowID = valid.JobID, valid.WorkflowID
	decodedHash, hashErr := hex.DecodeString(snapshot.Hash)
	if hashErr != nil || len(decodedHash) != 20 && len(decodedHash) != 32 {
		return bad("INVALID_DOWNLOAD_EVENT", "下载历史的种子标识无效")
	}
	var accepted []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, snapshot.DownloadKind, snapshot.DownloadID).Scan(&accepted); err != nil {
		return err
	}
	var history struct {
		ID     string `json:"id"`
		Hash   string `json:"hash"`
		SiteID string `json:"site_id"`
		State  string `json:"state"`
	}
	if json.Unmarshal(accepted, &history) != nil || history.ID != snapshot.DownloadID || !sameHash(history.Hash, snapshot.Hash) || history.SiteID != snapshot.SiteID || history.State != "accepted" {
		return bad("INVALID_DOWNLOAD_EVENT", "下载器尚未接受资源，未发布下载事实")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(raw) > pluginDataValueLimit {
		return bad("DOWNLOAD_CONTEXT_TOO_LARGE", "下载识别上下文超过允许范围")
	}
	sealed, err := a.pluginStore.seal("download_added", "system", valid.EventID, raw)
	if err != nil {
		return err
	}
	// A retry preserves the original Context. Moving ciphertext to another
	// fact is prevented by authenticated event-ID binding and reader checks.
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('download_added',?,?,?) ON CONFLICT(kind,id) DO NOTHING`, valid.EventID, sealed, now()); err != nil {
		return err
	}
	return recordDomainEventTx(ctx, tx, valid)
}
func (a *App) downloadAddedSnapshot(ctx context.Context, event DomainEvent) (downloadAddedSnapshot, error) {
	return a.pluginStore.downloadAddedSnapshotFrom(ctx, a.Store.DB, event)
}

func (p *PluginStore) downloadAddedSnapshotFrom(ctx context.Context, q PluginProofReader, event DomainEvent) (downloadAddedSnapshot, error) {
	var snapshot downloadAddedSnapshot
	input := event
	input.Sequence = 0
	valid, err := validateDomainEvent(input)
	if err != nil || event.Sequence < 0 || valid.Type != "download.accepted" {
		return snapshot, bad("INVALID_DOWNLOAD_EVENT", "下载事件引用无法核对")
	}
	var sealed []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_added' AND id=?`, valid.EventID).Scan(&sealed); err != nil {
		return snapshot, err
	}
	raw, err := p.unseal("download_added", "system", valid.EventID, sealed)
	if err != nil {
		return snapshot, err
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.DownloadID != valid.SubjectID || snapshot.DownloadKind != valid.SubjectKind || snapshot.JobID != valid.JobID || snapshot.WorkflowID != valid.WorkflowID {
		return snapshot, bad("INVALID_DOWNLOAD_EVENT", "下载事件与保存上下文不符")
	}
	mediaID := ""
	if validMedia(snapshot.MediaType, snapshot.TMDBID) {
		mediaID = snapshot.MediaType + ":" + strconv.FormatInt(snapshot.TMDBID, 10)
	}
	if valid.MediaID != mediaID {
		return snapshot, bad("INVALID_DOWNLOAD_EVENT", "下载事件与影视身份不符")
	}
	var stored []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, snapshot.DownloadKind, snapshot.DownloadID).Scan(&stored); err != nil {
		return snapshot, err
	}
	var history struct {
		ID     string `json:"id"`
		Hash   string `json:"hash"`
		SiteID string `json:"site_id"`
	}
	if json.Unmarshal(stored, &history) != nil || history.ID != snapshot.DownloadID || !sameHash(history.Hash, snapshot.Hash) || history.SiteID != snapshot.SiteID {
		return snapshot, bad("INVALID_DOWNLOAD_EVENT", "下载历史已变化，无法核对事件上下文")
	}
	if source := snapshot.Context; source != nil {
		if source.DownloadID != snapshot.DownloadID || source.DownloadKind != snapshot.DownloadKind || !sameHash(source.Hash, snapshot.Hash) {
			return snapshot, bad("INVALID_DOWNLOAD_CONTEXT", "下载上下文不属于当前历史")
		}
		if source.Metadata != nil {
			value := *source.Metadata
			snapshot.Metadata = &value
			snapshot.Seasons = source.Seasons
		} else {
			// Older private snapshots retained only raw inputs. Keep a bounded
			// read path for those rows; new producers always parse before Add.
			meta, err := releasemeta.Parse(source.Title, source.Subtitle, source.Options)
			if err != nil {
				return snapshot, bad("RESOURCE_METADATA_FAILED", "下载资源元数据解析失败，请检查资源识别配置")
			}
			snapshot.Metadata = &subscribegroup.Metadata{Resolution: meta.Resolution, Quality: meta.Quality, Effect: meta.Effect, Team: meta.Team, Customization: meta.Customization}
			snapshot.Seasons = meta.Season
		}
	}
	return snapshot, nil
}

func (a *App) acceptNativeDownload(ctx context.Context, in NativeDownload, job Job) error {
	source, err := a.loadDownloadContext(ctx, "native_download", in.ID, in.Hash)
	if err != nil {
		return err
	}
	event := DomainEvent{EventID: domainEventID("download.accepted", "native_download:"+in.ID), Type: "download.accepted", WorkflowID: in.WorkflowID, JobID: job.ID, NativeDownloadID: in.ID}
	if validMedia(in.MediaType, in.TMDBID) {
		event.MediaID = in.MediaType + ":" + strconv.FormatInt(in.TMDBID, 10)
	}
	snapshot := downloadAddedSnapshot{DownloadID: in.ID, DownloadKind: "native_download", Hash: in.Hash, SiteID: in.SiteID, MediaType: in.MediaType, TMDBID: in.TMDBID, HasContext: source != nil, HasHistory: true, Context: source}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "native_download", in.ID, in); err != nil {
		return err
	}
	if err = a.recordDownloadAddedTx(ctx, tx, event, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) stageIntentDownload(ctx context.Context, in Intent, source downloadContextSnapshot) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "intent", in.ID, in); err != nil {
		return err
	}
	if err = a.saveDownloadContextTx(ctx, tx, source); err != nil {
		return err
	}
	return tx.Commit()
}
