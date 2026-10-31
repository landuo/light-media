package app

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"light-media/internal/plugins"
)

func originalSharedSchedulerCapability(method string) bool {
	return method == plugins.SharedSchedulerRemovePluginJob || method == plugins.SharedSchedulerUpdatePluginJob
}

func (a *App) originalSharedScheduler(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedSchedulerCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件调度未绑定当前调用")
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "调度标识必须是有效Unicode字符串")
	}
	var input struct {
		PluginID *string         `json:"plugin_id"`
		JobID    json.RawMessage `json:"job_id,omitempty"`
	}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.PluginID == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件标识缺失")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if method == plugins.SharedSchedulerUpdatePluginJob {
		if len(input.JobID) != 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "更新服务不接受job_id")
		}
		if *input.PluginID != "" {
			if err := a.updateOriginalPluginJobsScoped(ctx, *input.PluginID, &scope); err != nil {
				return nil, err
			}
		}
	} else {
		jobID := ""
		if len(input.JobID) != 0 && string(input.JobID) != "null" {
			if err := json.Unmarshal(input.JobID, &jobID); err != nil {
				return nil, bad("INVALID_PLUGIN_VALUE", "job_id必须是字符串或null")
			}
		}
		a.removeOriginalPluginJobsSelected(*input.PluginID, jobID)
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.RawMessage(`null`), nil
}

// Caller owns jobs.mu. A truthy exact global ID includes default services and
// ignores pid. Empty ID matches only ordinary registrations' explicit pid;
// the common scheduler has no pid and is not the empty-string plugin.
func removeOriginalJobsLocked(state *originalDirectJobs, instance, jobID string) []*originalDirectJob {
	removed := []*originalDirectJob{}
	for key, job := range state.jobs {
		match := jobID != "" && originalServiceID(job) == jobID || jobID == "" && !job.commonModule && job.object != nil && job.object.installation.InstanceID == instance
		if match {
			delete(state.jobs, key)
			removed = append(removed, job)
		}
	}
	return removed
}

func releaseOriginalJobOwners(jobs []*originalDirectJob) {
	for _, job := range jobs {
		if job.object != nil && job.retained != nil {
			job.object.releaseOriginalRetainedOwner(job.retained)
		}
	}
}

func lookupOriginalJobIDLocked(state *originalDirectJobs, jobID string) *originalDirectJob {
	for _, job := range state.jobs {
		if originalServiceID(job) == jobID {
			return job
		}
	}
	return nil
}

func (a *App) removeOriginalPluginJobsSelected(instance, jobID string) {
	state := &a.pluginOriginalDirectJobs
	state.mu.Lock()
	removed := removeOriginalJobsLocked(state, instance, jobID)
	state.mu.Unlock()
	// Removing a registry entry removes its future clock. Already admitted
	// calls retain their own exact operation/call references until completion.
	releaseOriginalJobOwners(removed)
}

func (a *App) callOriginalBoundService(ctx context.Context, job *originalDirectJob, operation *originalRetainedLease, input json.RawMessage) (plugins.RunResult, error) {
	call, ok := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
	if !ok || call == nil || call.job != job || job.object == nil {
		return plugins.RunResult{}, bad("PLUGIN_CAPABILITY_DENIED", "服务调用未绑定实际注册")
	}
	ctx, err := job.object.originalRetainedParentContext(ctx, operation)
	if err != nil {
		return plugins.RunResult{}, err
	}
	unlock := job.object.lockOriginalMethod()
	defer unlock()
	return job.object.callSession(ctx, plugins.Runner{Timeout: 30 * time.Second}, job.definition.Handler, input, nil)
}
