package app

import (
	"context"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// Native watcher/timer callbacks belong to this installation even when no SDK
// worker is alive. A real invocation retains its RunID; background observations
// are instance logs and never borrow a completed worker's identity.
func (o *pluginScrapeCleanOwner) logSink(captured Settings) func(context.Context, scrapehost.Binding, scrapehost.Log) {
	return func(ctx context.Context, b scrapehost.Binding, event scrapehost.Log) {
		if b.InstanceID != o.instance || b.Generation != o.generation {
			return
		}
		run := PluginRun{InstanceID: o.instance, Version: "1.0.7", Generation: b.Generation, ConfigRevision: b.Revision}
		if call, ok := ctx.Value(pluginScrapeCleanOwnerCallKey{}).(pluginScrapeCleanOwnerCall); ok && call.owner == o && call.run != nil {
			run = *call.run
		}
		message := iyuuSourceSecretFields.ReplaceAllString(event.Message, "${1}[REDACTED]")
		message = cleanWithSettings(cleanWithSettings(message, captured), o.a.Settings())
		o.a.pluginExecutionLog(PluginInstallation{InstanceID: o.instance, PluginID: "ScrapeFileClean", Version: "1.0.7"}, run, event.Level, "scrape_clean."+event.Event, message, "")
	}
}
