package app

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"light-media/internal/sites"
)

// The six source EventType.SystemError fields. This is a dedicated typed event,
// not an unrestricted DomainEvent payload or a NoticeMessage notification.
type SystemErrorEvent struct {
	Type         string `json:"type"`
	ModuleID     string `json:"module_id"`
	ModuleName   string `json:"module_name"`
	ModuleMethod string `json:"module_method"`
	Error        string `json:"error"`
	Traceback    string `json:"traceback"`
}

// Display-name evidence is the fixed public Indexer source, not a claim of
// byte-identical installed Indexer. Chain identity and method are installed proof.
const siteSystemModuleName = "站点索引"
const siteSystemModuleNameSource = "public:1528176beacc0f9f8d8d9cbf1adf048b0da67b5c/app/modules/indexer/__init__.py"
const siteNativeDiagnosticHeader = "Go native parser diagnostic (not Python traceback):\n"

var systemErrorIDPattern = regexp.MustCompile(`^sye_[a-f0-9]{64}$`)
var nativeSiteFramePattern = regexp.MustCompile(`^light-media/internal/sites\.[A-Za-z0-9_.*()\[\]/-]+:[0-9]+$`)

type siteSystemErrorReceipt struct {
	Version                                                         int
	ID, Collection, FailureKind, ModuleNameSource, DiagnosticSource string
	Origin                                                          siteCollectionOrigin
	Data                                                            SystemErrorEvent
}

// Stable safe native descriptions retain distinct audited failure classes in
// the original helper dedup key, without changing shared parser Error()/codes
// or adding artificial note metadata. They are not reconstructed Python errors.
func siteNativeModuleErrorText(kind string) string {
	switch kind {
	case "hh-missing-panel":
		return "Go 原生解析失败：HHanClub 缺少用户信息面板（hh-missing-panel）"
	case "mt-invalid-json":
		return "Go 原生解析失败：M-Team 返回的 JSON 格式无效（mt-invalid-json）"
	case "mt-null-data":
		return "Go 原生解析失败：M-Team data 字段为空（mt-null-data）"
	case "mt-invalid-integer":
		return "Go 原生解析失败：M-Team 整数字段格式无效（mt-invalid-integer）"
	default:
		return ""
	}
}
func validSiteSystemError(r siteSystemErrorReceipt) bool {
	if r.Version != 1 || !systemErrorIDPattern.MatchString(r.ID) || r.ModuleNameSource != siteSystemModuleNameSource || r.DiagnosticSource != "go-native-parser-stack/v1" {
		return false
	}
	switch r.FailureKind {
	case "hh-missing-panel", "mt-invalid-json", "mt-null-data", "mt-invalid-integer":
	default:
		return false
	}
	d := r.Data
	if d.Type != "module" || d.ModuleID != "IndexerModule" || d.ModuleName != siteSystemModuleName || d.ModuleMethod != "refresh_userdata" || d.Error != siteNativeModuleErrorText(r.FailureKind) || !strings.HasPrefix(d.Traceback, siteNativeDiagnosticHeader) || len(d.Traceback) > 16384 {
		return false
	}
	frames := strings.Split(strings.TrimSuffix(strings.TrimPrefix(d.Traceback, siteNativeDiagnosticHeader), "\n"), "\n")
	if len(frames) == 0 || len(frames) > 32 {
		return false
	}
	for _, frame := range frames {
		if !nativeSiteFramePattern.MatchString(frame) {
			return false
		}
	}
	return true
}
func systemErrorEvidenceError() error {
	return bad("SYSTEM_ERROR_EVIDENCE_INVALID", "系统错误来源证据无法确认")
}
func (p *PluginStore) systemErrorReceipt(ctx context.Context, q subscriptionCompletionReader, key string) (siteSystemErrorReceipt, error) {
	var r siteSystemErrorReceipt
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_system_error' AND id=?`, key).Scan(&sealed); err != nil {
		return r, err
	}
	raw, err := p.unseal("site_system_error", "system", key, sealed)
	if err != nil || cleanupJournalStrict(raw, &r) != nil || !validSiteSystemError(r) || r.ID != key || r.ID != "sye_"+p.noticeID("site-system-error", r.Collection) {
		return r, systemErrorEvidenceError()
	}
	collection, err := p.readSiteMessageCollection(ctx, q, r.Collection)
	if err != nil || collection.Origin != r.Origin || collection.Snapshot.SourceData != nil || (collection.Stage != "collected" && collection.Stage != "complete") {
		return r, systemErrorEvidenceError()
	}
	return r, nil
}
func (p *PluginStore) systemErrorEvent(ctx context.Context, q subscriptionCompletionReader, key string) (SystemErrorEvent, error) {
	r, e := p.systemErrorReceipt(ctx, q, key)
	return r.Data, e
}
func validateSystemErrorDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) { return in, bad("INVALID_DOMAIN_EVENT", "系统错误事件契约无效") }
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	if in.Type != "system.error" || in.SchemaVersion != 1 || in.Sequence != 0 || !systemErrorIDPattern.MatchString(in.SystemErrorID) || in.EventID != domainEventID("system.error", in.SystemErrorID) {
		return invalid()
	}
	if in.PluginTriggerID != "" || in.JobID != "" || in.MediaID != "" || in.WorkflowID != "" || in.IntentID != "" || in.NativeDownloadID != "" || in.DownloadDeletedID != "" || in.FileEventID != "" || in.NoticeID != "" || in.AssetID != "" || in.SiteID != "" || in.SourceSiteID != 0 || in.SubscriptionID != "" || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "system_error"
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "system_error"
	}
	if in.SubjectID == "" {
		in.SubjectID = in.SystemErrorID
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.Purpose != "system_error" || in.SubjectKind != "system_error" || in.SubjectID != in.SystemErrorID || in.Origin != "core" {
		return invalid()
	}
	if in.OccurredAt == "" {
		in.OccurredAt = now()
	}
	at, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}
func validateSystemErrorBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var found string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM records WHERE kind='site_system_error' AND id=?`, event.SystemErrorID).Scan(&found); err != nil {
		return systemErrorEvidenceError()
	}
	return nil
}

// Fresh execution only. Recovery consumes the sealed collection; it never calls
// this producer or replays its volatile UI queue. Origin comes from the verified
// collection/run, not any field supplied by the plugin/API request.
func (a *App) stageSiteSystemErrorTx(ctx context.Context, tx *sql.Tx, origin siteCollectionOrigin, siteID string, nativeErr error) (func(bool), error) {
	kind, stack, known := sites.SourceUserModuleFailureDiagnostic(nativeErr)
	if !known {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := a.pluginStore
	collectionID := p.noticeID("site-collection", origin.Kind, origin.ID, siteID)
	collection, err := p.readSiteMessageCollection(ctx, tx, collectionID)
	if err != nil {
		return nil, err
	}
	if collection.Origin != origin || collection.Stage != "collected" || collection.Snapshot.SourceData != nil {
		return nil, systemErrorEvidenceError()
	}
	if err = p.siteCollectionOwner(ctx, tx, collection); err != nil {
		return nil, err
	}
	if err = a.requireSiteCollectionAuthorityTx(ctx, tx, collection); err != nil {
		return nil, err
	}
	key := "sye_" + p.noticeID("site-system-error", collectionID)
	if _, err = p.systemErrorReceipt(ctx, tx, key); err == nil {
		return nil, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	receipt := siteSystemErrorReceipt{Version: 1, ID: key, Collection: collectionID, FailureKind: kind, ModuleNameSource: siteSystemModuleNameSource, DiagnosticSource: "go-native-parser-stack/v1", Origin: collection.Origin, Data: SystemErrorEvent{Type: "module", ModuleID: "IndexerModule", ModuleName: siteSystemModuleName, ModuleMethod: "refresh_userdata", Error: siteNativeModuleErrorText(kind), Traceback: stack}}
	if !validSiteSystemError(receipt) {
		return nil, systemErrorEvidenceError()
	}
	sealed, err := p.seal("site_system_error", "system", key, mustCleanSeedJSON(receipt))
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('site_system_error',?,?,?)`, key, sealed, now()); err != nil {
		return nil, err
	}
	// Original helper.put precedes send_event. Stage the helper under one mutex,
	// then the typed event; only a committed transaction releases visibility.
	// On rollback restore both FIFO and duplicate cache, with no SSE escape.
	m := &a.systemMessages
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("system_message_stopped")
	}
	count := len(m.queue)
	oldOrder := append([]string(nil), m.order...)
	oldRecent := make(map[string]time.Time, len(m.recent))
	for k, v := range m.recent {
		oldRecent[k] = v
	}
	finished := false
	finish := func(committed bool) {
		if finished {
			return
		}
		finished = true
		if !committed {
			for i := count; i < len(m.queue); i++ {
				m.queue[i] = nil
			}
			m.queue = m.queue[:count]
			m.order = oldOrder
			m.recent = oldRecent
		}
		m.mu.Unlock()
	}
	title := receipt.Data.ModuleName + "发生了错误"
	m.putLocked("system", &title, receipt.Data.Error, nil, time.Now())
	if err = recordDomainEventTx(ctx, tx, DomainEvent{Type: "system.error", EventID: domainEventID("system.error", key), SystemErrorID: key}); err != nil {
		finish(false)
		return nil, err
	}
	return finish, nil
}
