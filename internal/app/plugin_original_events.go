package app

import (
	"context"
	"encoding/json"
)

// Reload is an original broadcast. Central scheduler refresh and subscribed
// plugin methods are independent tasks; no native-plugin special case exists.
func (a *App) emitOriginalPluginReload(instance string) {
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.updateOriginalPluginJobs(context.Background(), instance) }()
	raw, _ := json.Marshal(map[string]string{"plugin_id": instance})
	a.broadcastOriginalPluginEvent(context.Background(), "plugin_reload", raw)
}
