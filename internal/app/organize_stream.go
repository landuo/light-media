package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Job changes wake only dialogs following that job. Subscribing before the first
// state read also covers a worker that finishes while admission is returning.
type organizeLive struct {
	mu       sync.Mutex
	watchers map[chan struct{}]map[string]bool
	copies   sync.Map // job ID -> current source, byte count, and size
}

type organizeLiveCopy struct {
	source string
	size   int64
	bytes  *atomic.Int64
}

func (live *organizeLive) subscribe(ids map[string]bool) (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	live.mu.Lock()
	if live.watchers == nil {
		live.watchers = make(map[chan struct{}]map[string]bool)
	}
	live.watchers[ch] = ids
	live.mu.Unlock()
	return ch, func() {
		live.mu.Lock()
		delete(live.watchers, ch)
		live.mu.Unlock()
	}
}

func (live *organizeLive) changed(id string) {
	live.mu.Lock()
	defer live.mu.Unlock()
	for ch, ids := range live.watchers {
		if ids[id] {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

type organizeFrame struct {
	Stage     string `json:"stage"`
	Current   string `json:"current,omitempty"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Percent   *int   `json:"percent,omitempty"`
}

func (a *App) streamOrganize(w http.ResponseWriter, r *http.Request) {
	var in manualOrganizeRequest
	if err := decodeBody(w, r, &in); err != nil {
		a.writeError(w, r, err)
		return
	}
	if !in.Interactive || in.Preview || in.Background {
		a.writeError(w, r, bad("INVALID_ORGANIZE", "实时进度适用于立即整理"))
		return
	}
	if a.paused() {
		a.writeError(w, r, &APIError{"AUTOMATION_PAUSED", "自动化已暂停，请先恢复", 409})
		return
	}
	ctx := r.Context()
	in.audit, _ = ctx.Value(organizeAuditKey{}).(*organizeRequestAudit)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(event string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := send("progress", organizeFrame{Stage: "preparing"}); err != nil {
		return
	}
	out, err := a.queueSelectedManualFiles(ctx, in)
	if err == nil {
		out, err = a.followOrganizeSelection(ctx, out, send)
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		code, message := "ORGANIZE_FAILED", a.clean(err.Error())
		var api *APIError
		if errors.As(err, &api) {
			code, message = api.Code, api.Message
		}
		_ = send("error", map[string]string{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")})
		return
	}
	_ = send("complete", out)
}

func (a *App) followOrganizeSelection(ctx context.Context, out manualOrganizeOutput, send func(string, any) error) (manualOrganizeOutput, error) {
	ids := map[string]bool{}
	var ordered []string
	sources := map[string]string{}
	for _, item := range out.Items {
		if item.JobID != "" && !ids[item.JobID] {
			ids[item.JobID] = true
			ordered = append(ordered, item.JobID)
			sources[item.JobID] = item.Source
		}
	}
	if len(ordered) == 0 {
		return out, nil
	}
	wake, stop := a.organizeLive.subscribe(ids)
	defer stop()
	terminal := map[string][]manualOrganizeItem{}
	frame := organizeFrame{Stage: "queued", Total: len(ordered)}
	if err := send("progress", frame); err != nil {
		return out, err
	}

	stateRefresh := time.NewTicker(15 * time.Second) // recover cancellations made outside a worker; keep idle connections alive
	defer stateRefresh.Stop()
	copyRefresh := time.NewTicker(500 * time.Millisecond) // live byte counters only; no database reads
	defer copyRefresh.Stop()

	for len(terminal) < len(ordered) {
		active := make([]string, 0, len(ordered)-len(terminal))
		for _, id := range ordered {
			if _, done := terminal[id]; !done {
				active = append(active, id)
			}
		}
		encoded, _ := json.Marshal(active)
		rows, err := a.Store.reader().QueryContext(ctx, `SELECT id,kind,state,error_code,error_message FROM jobs WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
		if err != nil {
			return out, err
		}
		states := map[string]Job{}
		for rows.Next() {
			var job Job
			if err = rows.Scan(&job.ID, &job.Kind, &job.State, &job.ErrorCode, &job.ErrorMessage); err != nil {
				rows.Close()
				return out, err
			}
			states[job.ID] = job
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}

		frame = organizeFrame{Stage: "queued", Total: len(ordered)}
		for _, id := range active {
			j, ok := states[id]
			if !ok {
				j, err = a.pendingOrganizeJob(ctx, id)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return out, err
				}
				// Delivery can commit and retire the receipt between the two reads.
				if j.ID == "" {
					continue
				}
			}
			if jobTerminal(j.State) {
				items, err := a.terminalOrganizeItems(ctx, j, sources[id])
				if err != nil {
					return out, err
				}
				terminal[id] = items
			} else if j.State == "running" {
				if frame.Stage != "transferring" {
					frame.Stage, frame.Current = "recognizing", sources[id]
					if j.Kind == "import" {
						frame.Stage = "transferring"
					}
				}
			}
		}
		frame.Completed = len(terminal)
		if err = send("progress", frame); err != nil {
			return out, err
		}
		if frame.Completed == frame.Total {
			break
		}
		lastPercent := -1
	wait:
		for {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-wake:
				break wait
			case <-stateRefresh.C:
				break wait
			case <-copyRefresh.C:
				for _, id := range active {
					v, ok := a.organizeLive.copies.Load(id)
					if !ok {
						continue
					}
					c := v.(organizeLiveCopy)
					n := c.bytes.Load()
					if c.size <= 0 || n <= 0 {
						continue
					}
					pct := int(min(99, float64(n)*100/float64(c.size)))
					if pct != lastPercent {
						f := frame
						f.Stage, f.Current, f.Percent = "transferring", c.source, &pct
						if err = send("progress", f); err != nil {
							return out, err
						}
						lastPercent = pct
					}
					break
				}
			}
		}
	}
	result := manualOrganizeOutput{Items: []manualOrganizeItem{}, Submitted: out.Submitted}
	for _, item := range out.Items {
		if projected, ok := terminal[item.JobID]; ok {
			result.Items = append(result.Items, projected...)
			delete(terminal, item.JobID)
		} else if item.JobID == "" {
			result.Items = append(result.Items, item)
		}
	}
	result.Total = len(result.Items)
	for _, item := range result.Items {
		if item.Status == "failed" || item.Status == "blocked" {
			result.Failed++
		}
	}
	return result, nil
}

func (a *App) terminalOrganizeItems(ctx context.Context, j Job, source string) ([]manualOrganizeItem, error) {
	item := manualOrganizeItem{Source: source, JobID: j.ID, Status: "success"}
	if j.State == "cancelled" && j.ErrorCode == "ORGANIZE_FILE_SKIPPED" {
		item.Status, item.Error = "skipped", j.ErrorMessage
		return []manualOrganizeItem{item}, nil
	}
	if j.Kind == "import" {
		if err := a.Store.reader().QueryRowContext(ctx, "SELECT payload FROM jobs WHERE id=?", j.ID).Scan(&j.Payload); err != nil {
			return nil, err
		}
		out, err := a.originalTransferJobResult(ctx, j)
		if err != nil {
			return nil, err
		}
		if out != nil && len(out.Items) > 0 {
			if j.State != "succeeded" && out.Failed == 0 {
				// A final bookkeeping/cancellation failure can follow physical publication.
				item.Status, item.Error = "failed", j.ErrorMessage
				if item.Error == "" {
					item.Error = "整理任务未完成"
				}
				return append(out.Items, item), nil
			}
			return out.Items, nil
		}
	}
	if j.State != "succeeded" {
		item.Status, item.Error = "failed", j.ErrorMessage
		if item.Error == "" {
			item.Error = "整理任务未完成 " + filepath.Base(source)
		}
	}
	return []manualOrganizeItem{item}, nil
}
