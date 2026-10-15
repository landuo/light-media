package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type Notification struct {
	ID         string `json:"id"`
	JobID      string `json:"job_id"`
	WorkflowID string `json:"workflow_id"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
	Read       bool   `json:"read"`
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
	var unread int
	e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='notification' AND json_extract(payload,'$.read')=0`).Scan(&unread)
	return map[string]any{"items": items, "unread": unread}, e
}
func (a *App) notificationAPI(r *http.Request, p string) (any, error) {
	key := strings.TrimSuffix(p, "/read")
	if !strings.HasSuffix(p, "/read") || r.Method != "POST" {
		return nil, &APIError{"NOT_FOUND", "通知不存在", 404}
	}
	var n Notification
	if e := a.Store.Get(r.Context(), "notification", key, &n); e != nil {
		return nil, e
	}
	n.Read = true
	if e := a.Store.Put(r.Context(), "notification", key, n); e != nil {
		return nil, e
	}
	return n, nil
}
func (a *App) auditRequest(ctx context.Context, operation, request string, status int) {
	_ = a.Store.Put(ctx, "audit", id("audit_"), map[string]any{"time": now(), "actor": "admin", "operation": operation, "request_id": request, "http_status": status})
}
func (a *App) decisionRecords(ctx context.Context, wf string) ([]json.RawMessage, error) {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='decision' AND json_extract(payload,'$.workflow_id')=? ORDER BY updated_at LIMIT 10`, wf)
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
