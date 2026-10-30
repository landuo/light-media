package app

import (
	"context"
	"encoding/json"
	"time"

	"light-media/internal/plugins"
)

// Ordinary callback exceptions retain their source diagnostic after the same
// bounded secret redactor used by plugin logs. Transport/host failures stay
// opaque. No Python traceback is invented at this Go process boundary.
func (a *App) reportOriginalServiceExecutionError(ctx context.Context, job *originalDirectJob, err error) string {
	fields := map[string]any{"type": "scheduler", "scheduler_id": originalServiceID(job), "scheduler_name": job.definition.Name}
	title := job.definition.Name + " 执行失败"
	detail, present := plugins.CallbackErrorDetail(err)
	if !present {
		a.reportOriginalPluginExecutionError(ctx, "system", title, fields, err)
		return "插件定时方法执行失败"
	}
	safe := sanitizeLogEntry(map[string]any{"message": detail}, a.Settings())["message"].(string)
	title = a.clean(title)
	a.systemMessages.put("system", &title, safe, nil, time.Now())
	fields["error"], fields["error_code"], fields["traceback"], fields["diagnostic_source"] = safe, "plugin_operation_failed", "", "go-sdk-callback-detail/v1"
	raw, marshalErr := json.Marshal(fields)
	if marshalErr == nil {
		a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), "system.error", raw)
	}
	return safe
}
