package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type Notification struct {
	Source     string          `json:"source,omitempty"`
	ID         string          `json:"id"`
	JobID      string          `json:"job_id"`
	WorkflowID string          `json:"workflow_id"`
	Title      string          `json:"title"`
	Message    string          `json:"message"`
	MType      string          `json:"mtype,omitempty"`
	Channel    string          `json:"channel,omitempty"`
	Image      string          `json:"image,omitempty"`
	UserID     *string         `json:"userid,omitempty"`
	Action     json.RawMessage `json:"action,omitempty"`
	Note       json.RawMessage `json:"note,omitempty"`
	Link       string          `json:"link,omitempty"`
	CreatedAt  string          `json:"created_at"`
	Read       bool            `json:"read"`
}

// Notification content can include private image and action URLs. Keep the
// ordinary record safe for diagnostics, and only restore its encrypted content
// when serving the authenticated notification history.
type notificationRecord struct {
	Notification
	ProtectedContent   []byte `json:"protected_content,omitempty"`
	SourceRegisteredAt string `json:"source_registered_at,omitempty"`
}

func (a *App) saveProtectedNotification(ctx context.Context, n Notification) error {
	sealed, err := a.Store.Seal(n)
	if err != nil {
		return err
	}
	projection := Notification{ID: n.ID, CreatedAt: n.CreatedAt, Read: n.Read}
	return a.Store.Put(ctx, "notification", n.ID, notificationRecord{Notification: projection, ProtectedContent: sealed})
}

func (a *App) openNotification(record notificationRecord) (Notification, error) {
	n := record.Notification
	if len(record.ProtectedContent) == 0 {
		return n, nil
	}
	if err := a.Store.Unseal(record.ProtectedContent, &n); err != nil {
		return Notification{}, err
	}
	// Read state changes independently of the immutable encrypted content.
	n.Read = record.Read
	return n, nil
}

func (a *App) notifyFailed(ctx context.Context, j Job, message string) {
	n := Notification{ID: "notice_" + j.ID, JobID: j.ID, WorkflowID: j.WorkflowID, Title: a.clean(jobActionLabel(j.Kind) + "失败"), Message: a.clean(message), CreatedAt: now()}
	_ = a.Store.Put(ctx, "notification", n.ID, n)
}

const notificationOrderIndex = `CREATE INDEX IF NOT EXISTS notification_read_time_uid ON message(read,updated_at DESC,uid) WHERE record_kind='notification'`
const notificationUnreadSQL = `SELECT COUNT(*) FROM message INDEXED BY notification_read_time_uid WHERE record_kind='notification' AND read=0`
const notificationListSQL = `SELECT payload,(` + notificationUnreadSQL + `) FROM message INDEXED BY notification_read_time_uid
 WHERE record_kind='notification' ORDER BY read ASC NULLS LAST,updated_at DESC,uid LIMIT ?`

func (a *App) notifications(ctx context.Context) (any, error) {
	// The scalar count and the ordered page share one SQLite snapshot. A
	// notification arriving during a poll cannot produce a badge without a row.
	rows, err := a.Store.reader().QueryContext(ctx, notificationListSQL, 100)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []json.RawMessage{}
	var unread int
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw, &unread); err != nil {
			return nil, err
		}
		var record notificationRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		n, err := a.openNotification(record)
		if err != nil {
			return nil, err
		}
		content, err := json.Marshal(n)
		if err != nil {
			return nil, err
		}
		items = append(items, content)
	}
	return map[string]any{"items": items, "unread": unread}, rows.Err()
}

func (a *App) notificationAPI(r *http.Request, p string) (any, error) {
	if p == "read-all" && r.Method == http.MethodPost {
		// Keep history order, encrypted content and non-notification messages intact.
		// The payload trigger also updates the indexed read column.
		result, err := a.Store.DB.ExecContext(r.Context(), `UPDATE message SET payload=CAST(json_set(payload,'$.read',json('true')) AS BLOB) WHERE record_kind='notification' AND read=0`)
		if err != nil {
			return nil, err
		}
		updated, err := result.RowsAffected()
		return map[string]int64{"updated": updated}, err
	}
	key := strings.TrimSuffix(p, "/read")
	if !strings.HasSuffix(p, "/read") || r.Method != "POST" {
		return nil, &APIError{"NOT_FOUND", "通知不存在", 404}
	}
	var record notificationRecord
	if e := a.Store.Get(r.Context(), "notification", key, &record); e != nil {
		return nil, e
	}
	record.Read = true
	if e := a.Store.Put(r.Context(), "notification", key, record); e != nil {
		return nil, e
	}
	return a.openNotification(record)
}
func requestAuditRecord(operation, request string, status int) map[string]any {
	return map[string]any{"time": now(), "actor": "admin", "operation": operation, "request_id": request, "http_status": status}
}

type organizeAuditKey struct{}

// This state belongs to one HTTP request and is never serialized into a job.
type organizeRequestAudit struct {
	id, request    string
	committed      bool
	displayPreview bool
}

func (audit *organizeRequestAudit) commit(ctx context.Context, tx *sql.Tx) error {
	if audit == nil {
		return tx.Commit()
	}
	// Auditing is best effort. A failed INSERT still allows SQLite to commit
	// the business records and the HTTP middleware to retry the audit row.
	auditErr := txPut(ctx, tx, "audit", audit.id, requestAuditRecord("POST.files", audit.request, http.StatusOK))
	err := tx.Commit()
	audit.committed = err == nil && auditErr == nil
	return err
}

func (a *App) auditRequest(ctx context.Context, operation, request string, status int) {
	var key string
	if audit, _ := ctx.Value(organizeAuditKey{}).(*organizeRequestAudit); audit != nil && audit.request == request && operation == "POST.files" {
		if audit.committed && status == http.StatusOK {
			return
		}
		if audit.displayPreview {
			// The fully encoded preview has already been delivered. A client
			// navigating away must not cancel its audit write.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
		}
		finish := a.performanceStages(ctx, "organize_request", "audit_write")
		defer finish("")
		key = audit.id // Reuse the row even following an uncertain commit.
	} else {
		key = id("audit_")
	}
	_ = a.Store.Put(ctx, "audit", key, requestAuditRecord(operation, request, status))
}
func (a *App) decisionRecords(ctx context.Context, wf string) ([]json.RawMessage, error) {
	rows, e := a.Store.reader().QueryContext(ctx, `SELECT payload FROM decision_log WHERE json_extract(payload,'$.workflow_id')=? ORDER BY updated_at LIMIT 10`, wf)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
