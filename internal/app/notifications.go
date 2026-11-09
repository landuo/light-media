package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	n := Notification{ID: "notice_" + j.ID, JobID: j.ID, WorkflowID: j.WorkflowID, Title: "任务需要处理", Message: a.clean(message), CreatedAt: now()}
	_ = a.Store.Put(ctx, "notification", n.ID, n)
}
func (a *App) notifications(ctx context.Context) (any, error) {
	items, e := a.Store.List(ctx, "notification", 100, 0)
	if e != nil {
		return nil, e
	}
	for i, raw := range items {
		var record notificationRecord
		if e = json.Unmarshal(raw, &record); e != nil {
			return nil, e
		}
		n, err := a.openNotification(record)
		if err != nil {
			return nil, err
		}
		items[i], e = json.Marshal(n)
		if e != nil {
			return nil, e
		}
	}
	var unread int
	e = a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='notification' AND json_extract(payload,'$.read')=0`).Scan(&unread)
	return map[string]any{"items": items, "unread": unread}, e
}
func (a *App) notificationAPI(r *http.Request, p string) (any, error) {
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
func (a *App) auditRequest(ctx context.Context, operation, request string, status int) {
	_ = a.Store.Put(ctx, "audit", id("audit_"), map[string]any{"time": now(), "actor": "admin", "operation": operation, "request_id": request, "http_status": status})
}
func (a *App) decisionRecords(ctx context.Context, wf string) ([]json.RawMessage, error) {
	rows, e := a.Store.reader().QueryContext(ctx, `SELECT payload FROM records WHERE kind='decision' AND json_extract(payload,'$.workflow_id')=? ORDER BY updated_at LIMIT 10`, wf)
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
