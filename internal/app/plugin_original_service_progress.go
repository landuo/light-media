package app

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"light-media/internal/plugins"
)

type originalServiceCallKey struct{}
type originalServiceCall struct{ job *originalDirectJob }
type originalServiceProgress struct {
	enabled  bool
	value    float64
	text     string
	data     map[string]json.RawMessage
	updated  time.Time
	accessed uint64
}

func originalServiceID(job *originalDirectJob) string {
	if job.commonModule {
		return "scheduler_job"
	}
	return job.object.installation.InstanceID + "_" + job.definition.ID
}
func originalServiceProvider(job *originalDirectJob) string {
	if job.commonModule {
		return "[系统]"
	}
	if job.provider != "" {
		return job.provider
	}
	return job.object.manifest.Name
}
func originalServiceEncode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// The source progress region is TLRU: reads change recency, while only writes
// renew the 24 hour lifetime. All callers hold the direct-jobs mutex.
func (a *App) originalServiceProgressGetLocked(jobID string, at time.Time) *originalServiceProgress {
	state := &a.pluginOriginalDirectJobs
	progress := state.progress[jobID]
	if progress != nil && !at.Before(progress.updated.Add(24*time.Hour)) {
		delete(state.progress, jobID)
		return nil
	}
	if progress != nil {
		state.progressAccess++
		progress.accessed = state.progressAccess
	}
	return progress
}

func (a *App) prepareOriginalServiceLocked(job *originalDirectJob) {
	state := &a.pluginOriginalDirectJobs
	if state.progress == nil {
		state.progress = map[string]*originalServiceProgress{}
	}
	at := time.Now()
	for key, value := range state.progress {
		if at.Sub(value.updated) >= 24*time.Hour {
			delete(state.progress, key)
		}
	}
	if len(state.progress) >= 1024 && state.progress[originalServiceID(job)] == nil {
		oldest := ""
		var oldestAccess uint64
		for key, value := range state.progress {
			if oldest == "" || value.accessed < oldestAccess {
				oldest, oldestAccess = key, value.accessed
			}
		}
		delete(state.progress, oldest)
	}
	job.running = true
	job.lastErrorSet = false
	job.lastStarted, job.lastFinished, job.lastError = at.Format("2006-01-02 15:04:05"), "", ""
	state.progress[originalServiceID(job)] = &originalServiceProgress{enabled: true, text: job.definition.Name + " 开始执行 ...", updated: at, data: map[string]json.RawMessage{
		"id": originalServiceEncode(originalServiceID(job)), "name": originalServiceEncode(job.definition.Name), "provider": originalServiceEncode(originalServiceProvider(job)), "status": originalServiceEncode("running"), "success": json.RawMessage(`null`), "started_at": originalServiceEncode(job.lastStarted), "finished_at": json.RawMessage(`null`), "error": json.RawMessage(`null`),
	}}
	a.originalServiceProgressGetLocked(originalServiceID(job), at)
}
func originalServiceResultError(raw json.RawMessage) string {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 1 {
		return ""
	}
	var tuple []json.RawMessage
	if json.Unmarshal(envelope[plugins.ServiceTupleKey], &tuple) != nil || len(tuple) == 0 || !bytes.Equal(bytes.TrimSpace(tuple[0]), []byte("false")) {
		return ""
	}
	if len(tuple) < 2 || !originalViewJSONTruth(tuple[1]) {
		return "定时任务返回失败"
	}
	var text string
	if json.Unmarshal(tuple[1], &text) == nil {
		return text
	}
	// JSON values crossing this Go contract have no arbitrary Python __str__.
	// Booleans retain Python spelling; compound values retain their JSON form.
	if bytes.Equal(tuple[1], []byte("true")) {
		return "True"
	}
	return string(tuple[1])
}
func (a *App) executeOriginalService(ctx context.Context, job *originalDirectJob, operation *originalRetainedLease) {
	if operation != nil {
		defer job.object.releaseOriginalRetainedOwner(operation)
	}
	ctx = context.WithValue(ctx, originalServiceCallKey{}, &originalServiceCall{job: job})
	var result plugins.RunResult
	input, err := originalServiceArguments(job.definition.Input)
	if job.commonModule {
		// ChainBase.scheduler_job deliberately returns None even when a
		// provider returns a terminal value; Scheduler.start sees success.
		_, err = a.invokeOriginalPluginModules(ctx, "scheduler_job", nil, nil, nil)
	} else if err == nil {
		result, err = a.callOriginalBoundService(ctx, job, operation, input)
	}
	errorText := originalServiceResultError(result.Result)
	failed := err != nil || errorText != ""
	if err != nil {
		errorText = a.reportOriginalServiceExecutionError(ctx, job, err)
		a.log("ERROR", "plugins", "service_failed", errorText, "", "")
	}
	errorText = a.clean(errorText)
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	// The fixed scheduler finishes by logical ID, not by the captured method
	// dictionary. An older asynchronous completion can therefore finish a
	// newly registered same-ID job and its shared progress; preserve that quirk.
	jobID := originalServiceID(job)
	current := lookupOriginalJobIDLocked(state, originalServiceID(job))
	finishedAt := time.Now().Format("2006-01-02 15:04:05")
	name := jobID
	var provider any
	if current != nil {
		current.running = false
		current.lastFinished, current.lastError = finishedAt, errorText
		current.lastErrorSet = failed
		name, provider = current.definition.Name, originalServiceProvider(current)
	}
	progress := a.originalServiceProgressGetLocked(originalServiceID(job), time.Now())
	if progress == nil {
		return
	}
	progress.enabled = false
	progress.updated = time.Now()
	status := "success"
	progress.text = name + " 执行完成"
	if !failed {
		progress.value = 100
	} else {
		status = "failed"
		progress.text = name + " 执行失败"
	}
	progress.data["status"] = originalServiceEncode(status)
	progress.data["success"] = originalServiceEncode(!failed)
	progress.data["finished_at"] = originalServiceEncode(finishedAt)
	progress.data["id"] = originalServiceEncode(jobID)
	progress.data["name"] = originalServiceEncode(name)
	progress.data["provider"] = originalServiceEncode(provider)
	progress.data["error"] = json.RawMessage(`null`)
	if failed {
		progress.data["error"] = originalServiceEncode(errorText)
	}
}
func (a *App) originalServiceProgressResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	call, ok := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
	if !ok || call == nil || call.job == nil || call.job.object != scope.object || call.job.definition.Handler != scope.phase {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "进度仅属于当前定时方法调用")
	}
	var input plugins.ServiceProgressUpdate
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.Value != nil && (math.IsNaN(*input.Value) || math.IsInf(*input.Value, 0)) {
		return nil, bad("INVALID_PLUGIN_VALUE", "进度必须为有限数值")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	progress := a.originalServiceProgressGetLocked(originalServiceID(call.job), time.Now())
	if !call.job.running || progress == nil || !progress.enabled {
		return json.RawMessage(`null`), nil
	}
	if input.Value != nil {
		progress.value = max(0, min(*input.Value, 100))
	}
	if input.Text != nil {
		progress.text = a.clean(*input.Text)
	}
	progress.updated = time.Now()
	for key, value := range map[string]any{"id": originalServiceID(call.job), "name": call.job.definition.Name, "provider": originalServiceProvider(call.job), "status": "running", "success": nil} {
		progress.data[key] = originalServiceEncode(value)
	}
	for key, value := range input.Data {
		progress.data[key] = append(json.RawMessage(nil), value...)
	}
	return json.RawMessage(`null`), nil
}
func (a *App) originalServiceProgressSnapshot(jobID string) map[string]any {
	if jobID == "" {
		return nil
	}
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	defer state.mu.Unlock()
	return a.originalServiceProgressSnapshotLocked(jobID)
}
func (a *App) originalServiceProgressSnapshotLocked(jobID string) map[string]any {
	state := &a.pluginOriginalDirectJobs
	var job *originalDirectJob
	for _, candidate := range state.jobs {
		if originalServiceID(candidate) == jobID {
			job = candidate
			break
		}
	}
	progress := a.originalServiceProgressGetLocked(jobID, time.Now())
	if job == nil && progress == nil {
		return nil
	}
	name, status := jobID, "waiting"
	var provider any
	running := false
	if job != nil {
		name, provider = job.definition.Name, originalServiceProvider(job)
		running = job.running
		if running {
			status = "running"
		}
	}
	out := map[string]any{"id": jobID, "name": name, "provider": provider, "enable": running, "value": 0, "text": nil, "status": status, "success": nil, "started_at": nil, "finished_at": nil, "error": nil, "data": map[string]any{}}
	if job != nil {
		for key, value := range map[string]string{"started_at": job.lastStarted, "finished_at": job.lastFinished, "error": job.lastError} {
			if value != "" {
				out[key] = value
			}
		}
	}
	if job != nil && job.lastErrorSet {
		// Source distinguishes a present empty exception from None.
		out["error"] = job.lastError
	}
	if progress != nil {
		data := map[string]json.RawMessage{}
		for key, value := range progress.data {
			data[key] = append(json.RawMessage(nil), value...)
		}
		out["enable"], out["value"], out["text"], out["data"] = progress.enabled, progress.value, progress.text, data
		for _, key := range []string{"name", "provider", "status", "success", "started_at", "finished_at", "error"} {
			if value, exists := data[key]; exists && (key == "success" || originalViewJSONTruth(value)) {
				out[key] = value
			}
		}
	}
	if job != nil && job.clockError != "" {
		// Additive clock state does not override callback status/success/error.
		// Neither prepare nor completion owns or clears this field.
		out["clock_error"] = job.clockError
	}
	return out
}

func originalServiceProgressPath(path string) (string, bool) {
	if strings.HasPrefix(path, "dashboard/schedule/") && strings.HasSuffix(path, "/progress") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "dashboard/schedule/"), "/progress")
		return id, id != "" && !strings.Contains(id, "/")
	}
	return "", false
}
