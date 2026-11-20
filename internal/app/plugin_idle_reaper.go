package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The idle reaper retires plugin worker processes that have served no call for
// the whole TTL window. The loaded object, its registrations, its events and
// its scheduler jobs all stay in the host; only the resident worker (its Go
// runtime and heap) is returned. The next call transparently rebuilds the
// session and replays the initializer, so scheduling, the UI and events keep
// working unchanged.
const defaultPluginIdleTTL = 30 * time.Minute

// pluginIdleRetireAllowlist is the audited set: replaying each initializer
// after a rebuild is a no-op (self-resetting configuration latch) or an
// idempotent same-value apply. Plugins with resident in-process loops
// (ScrapeFileClean, RemoveLink watchers), event-driven standbys
// (MediaServerRefresh answers transfer.complete) or heavy scan state are
// deliberately absent and stay resident. Extend only after auditing the
// initializer.
var pluginIdleRetireAllowlist = map[string]bool{
	"MergeSiteSwitch":     true,
	"CustomHosts":         true,
	"PluginUnInstall":     true,
	"RemoteIdentifiers":   true,
	"AutoBackup":          true,
	"CloudflareSpeedTest": true,
	"CategoryEditor":      true,
}

var errPluginIdleBusy = errors.New("plugin session busy")
var errPluginIdleAbsent = errors.New("plugin session absent")

// pluginIdleTTL resolves the retire window. Precedence: the environment, a
// data-directory file (for containers whose environment cannot change without
// a rebuild), then the default. "off" and non-positive values disable the
// reaper; an unparsable value disables it with a logged error rather than
// guessing a window the operator did not ask for.
func pluginIdleTTL(lookup func(string) (string, bool), dataDir string) (time.Duration, error) {
	if raw, ok := lookup("LIGHT_MEDIA_PLUGIN_IDLE_TTL"); ok && strings.TrimSpace(raw) != "" {
		return parsePluginIdleTTL(raw)
	}
	if dataDir != "" {
		if raw, err := os.ReadFile(filepath.Join(dataDir, "plugin-idle-ttl")); err == nil {
			if value := strings.TrimSpace(string(raw)); value != "" {
				return parsePluginIdleTTL(value)
			}
		}
	}
	return defaultPluginIdleTTL, nil
}

func parsePluginIdleTTL(raw string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "disabled":
		return 0, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid plugin idle TTL %q: %w", raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("invalid plugin idle TTL %q: must be positive", raw)
	}
	return value, nil
}

// pluginIdleExtraAllowlist adds operator-audited classes on top of the built-in
// list without a rebuild; a name may also be prefixed with '-' to subtract it.
func pluginIdleExtraAllowlist(lookup func(string) (string, bool)) (add, remove []string) {
	raw, ok := lookup("LIGHT_MEDIA_PLUGIN_IDLE_EXTRA")
	if !ok {
		return nil, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "-") {
			if name := strings.TrimSpace(strings.TrimPrefix(entry, "-")); name != "" {
				remove = append(remove, name)
			}
			continue
		}
		add = append(add, entry)
	}
	return add, remove
}

func pluginIdleEligible(p *originalPluginInstance, add, remove []string) bool {
	if !p.external || !pluginIdleRetireAllowlist[p.installation.PluginID] {
		if !p.external {
			return false
		}
		var extra bool
		for _, name := range add {
			if name == p.installation.PluginID {
				extra = true
				break
			}
		}
		if !extra {
			return false
		}
	}
	for _, name := range remove {
		if name == p.installation.PluginID {
			return false
		}
	}
	if p.stopping.Load() {
		return false
	}
	// A compiled-in plugin holds no worker process: retiring its session
	// would only force a pointless initializer replay on the next call.
	if p.builtin.Load() {
		return false
	}
	// A stopped-for-good object (explicit stop, not idle retirement) has no
	// worker; a retired one is already reaped. Both are out of scope here.
	if !p.sessionStarted || p.sessionRetired.Load() {
		return false
	}
	if idle := time.Since(time.Unix(0, p.lastUsed.Load())); idle < 0 {
		return false
	}
	return true
}

// retireSessionForIdle stops the resident worker while the loaded object and
// every registration stay intact. Any in-flight call or pending lease aborts
// the retirement; the reaper tries again later. The guards make the admission
// mutex redundant here: every reservation originates from an in-flight call
// (blocked by the inflight gate) or from an existing lease (blocked by the
// lease gate), and a post-flip caller wakes a fresh session instead. Holding
// the admission mutex across the worker stop would deadlock a stop handler
// that reserves its own final continuation.
func (p *originalPluginInstance) retireSessionForIdle(ctx context.Context) error {
	p.wakeMu.Lock()
	defer p.wakeMu.Unlock()
	p.submitted.mu.Lock()
	busy := submittedRetireBlocked(&p.submitted)
	p.submitted.mu.Unlock()
	if busy {
		return errPluginIdleBusy
	}
	p.sessionMu.Lock()
	client := p.session
	if client == nil || p.stopping.Load() || !p.sessionStarted || p.sessionRetired.Load() {
		p.sessionMu.Unlock()
		return errPluginIdleAbsent
	}
	if p.inflight.Load() != 0 {
		p.sessionMu.Unlock()
		return errPluginIdleBusy
	}
	p.session = nil
	p.sessionRetired.Store(true)
	p.sessionNeedsInit.Store(true)
	p.sessionMu.Unlock()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	stopCtx = context.WithValue(stopCtx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client})
	callback, closePhase := p.originalBaseSessionHost(stopCtx, "stop", nil)
	defer closePhase()
	return client.StopWithHost(stopCtx, callback)
}

// submittedRetireBlocked reports whether any asynchronous continuation still
// owns this object. The caller holds submitted.mu.
func submittedRetireBlocked(state *originalSubmittedState) bool {
	return len(state.leases) > 0 || state.sealed || state.privateClosing || state.publicClosing || state.publicRetiring
}

func (a *App) runPluginIdleReaper(ctx context.Context) {
	ttl, err := pluginIdleTTL(os.LookupEnv, a.Dir)
	if err != nil {
		a.log("ERROR", "plugins", "plugin_idle_disabled", "插件空闲退出配置无效："+err.Error(), "", "")
		return
	}
	if ttl == 0 {
		a.log("INFO", "plugins", "plugin_idle_disabled", "插件空闲退出已关闭", "", "")
		return
	}
	a.pluginIdleTicks.Store(0)
	add, remove := pluginIdleExtraAllowlist(os.LookupEnv)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		a.pluginIdleTicks.Add(1)
		now := time.Now()
		for _, object := range a.originalLoadedPluginSnapshot() {
			if ctx.Err() != nil {
				return
			}
			if object.inflight.Load() != 0 {
				continue
			}
			if idle := now.Sub(time.Unix(0, object.lastUsed.Load())); idle < ttl {
				continue
			}
			if !pluginIdleEligible(object, add, remove) {
				continue
			}
			switch err := object.retireSessionForIdle(ctx); {
			case err == nil:
				a.log("INFO", "plugins", "session_retired", "插件空闲会话已退出，等待下次调用重建："+object.installation.PluginID, "", "")
			case errors.Is(err, errPluginIdleBusy), errors.Is(err, errPluginIdleAbsent):
				// Someone is calling or the state already moved on.
			default:
				a.log("ERROR", "plugins", "session_retire_failed", "插件空闲会话退出失败："+object.installation.PluginID+" "+err.Error(), "", "")
			}
		}
	}
}

// pluginIdleProbe reports the reaper's own liveness and the idle age of every
// allowlisted object, bounded to diagnostic facts (no configuration payloads).
func (a *App) pluginIdleProbe() map[string]any {
	ttl, err := pluginIdleTTL(os.LookupEnv, a.Dir)
	if err != nil || ttl == 0 {
		return nil
	}
	add, remove := pluginIdleExtraAllowlist(os.LookupEnv)
	probe := map[string]any{"ttl_seconds": int64(ttl / time.Second), "ticks": a.pluginIdleTicks.Load()}
	objects := []map[string]any{}
	for _, object := range a.originalLoadedPluginSnapshot() {
		if !pluginIdleRetireAllowlist[object.installation.PluginID] || object.builtin.Load() {
			continue
		}
		objects = append(objects, map[string]any{
			"class":        object.installation.PluginID,
			"idle_seconds": int64(time.Since(time.Unix(0, object.lastUsed.Load())) / time.Second),
			"retired":      object.sessionRetired.Load(),
			"eligible":     pluginIdleEligible(object, add, remove),
			"inflight":     object.inflight.Load(),
		})
	}
	probe["objects"] = objects
	return probe
}

// pluginIdleTTLSeconds renders the effective window for settings surfaces.
func pluginIdleTTLSeconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}
