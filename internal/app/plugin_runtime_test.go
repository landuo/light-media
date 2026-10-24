package app

import (
	"context"
	"testing"
)

func TestPluginLogsRetainAndFilterExecutionIdentity(t *testing.T) {
	a := newTestApp(t)
	for _, instance := range []string{"CategoryEditor", "CategoryEditor.music"} {
		a.writeLog(map[string]any{"id": id("log_"), "time": now(), "level": "INFO", "module": "plugins", "event": "execution_completed", "message": "插件执行已完成", "plugin_id": "CategoryEditor", "plugin_version": "1.3", "plugin_instance_id": instance, "plugin_run_id": "pr_" + instance, "request_id": "", "error_code": "", "app_version": appVersion})
	}
	flushTestLogs(t, a)
	page, err := a.queryLogs(context.Background(), map[string]string{"plugin_id": "CategoryEditor", "plugin_instance_id": "CategoryEditor.music"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0]["plugin_run_id"] != "pr_CategoryEditor.music" || page.Entries[0]["plugin_version"] != "1.3" {
		t.Fatal("plugin log identity or independent clone filter missing", page.Entries, err)
	}
}
