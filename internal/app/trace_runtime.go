package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/logging"
)

type traceWriter struct {
	app                   *App
	Workflow, Job, Parent string
}

type jobTraceKey struct{}
type jobTrace struct{ Job, Request string }

func (w traceWriter) Write(b []byte) (int, error) {
	var entry map[string]any
	if json.Unmarshal(b, &entry) != nil {
		return 0, errors.New("invalid trace record")
	}
	event, _ := entry["event"].(string)
	if event == "run_started" || event == "http_started" || (event == "http_completed" && entry["status"] == "ok") {
		entry["level"] = "DEBUG"
	}
	entry["id"] = id("log_")
	entry["message"] = "外部接口请求"
	kind, _ := entry["target_kind"].(string)
	entry["module"] = "external." + kind
	if kind == "" {
		entry["module"] = "external"
	}
	if w.Workflow != "" {
		entry["workflow_id"] = w.Workflow
	}
	if w.Job != "" {
		entry["job_id"] = w.Job
	}
	if w.Parent != "" {
		entry["parent_request_id"] = w.Parent
	}
	w.app.writeLog(entry)
	return len(b), nil
}
func (a *App) traceContext(ctx context.Context, workflow, job, parent string) context.Context {
	ctx = context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: job, Request: parent})
	if workflow != "" {
		ctx = context.WithValue(ctx, workflowKey{}, workflow)
	}
	run, e := logging.NewRun(traceWriter{app: a, Workflow: workflow, Job: job, Parent: parent})
	if e != nil {
		return ctx
	}
	return run.Context(ctx)
}
