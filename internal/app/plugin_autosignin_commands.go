package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"light-media/internal/plugins"
	source "light-media/internal/plugins/autosignin"
	durable "light-media/internal/plugins/autosignin/durable"
)

const autoSignInCommandSchema = `CREATE TABLE IF NOT EXISTS plugin_signin_commands(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);`

type autoSignInCommand struct {
	Version                       int
	ID, Instance, RunID, InputSHA string
	Generation, Revision          int64
	Authority                     pluginAPIAuthority
	Event                         source.Object
}

// queueAutoSignInCommand is private to the trusted incoming command dispatcher.
// Existing live session/key authority is checked in the same transaction that
// creates the durable fact and invocation. A worker cannot call this function.
func (a *App) queueAutoSignInCommand(ctx context.Context, instance string, authority pluginAPIAuthority, channel, user string) (PluginRun, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return PluginRun{}, e
	}
	defer tx.Rollback()
	if authority.Kind != "key" && authority.Kind != "session" && authority.Kind != "command" {
		return PluginRun{}, bad("SIGNIN_COMMAND_DENIED", "命令来源未经鉴权")
	}
	if authority.Kind == "command" {
		// A message command carries the source channel/user instead of an HTTP
		// credential. The sealed fact below is only writable through this
		// authenticated dispatch path, so the fact itself is the evidence.
		if strings.TrimSpace(channel) == "" || strings.TrimSpace(user) == "" {
			return PluginRun{}, bad("SIGNIN_COMMAND_DENIED", "命令来源缺少渠道或用户")
		}
	} else if e = a.validatePluginHTTPAuthorityTx(ctx, tx, authority); e != nil {
		return PluginRun{}, e
	}
	if _, e = tx.ExecContext(ctx, autoSignInCommandSchema); e != nil {
		return PluginRun{}, e
	}
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	e = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision)
	if e != nil || plugin != "AutoSignIn" || version != source.Version || !installed || !enabled {
		return PluginRun{}, bad("SIGNIN_COMMAND_DENIED", "命令目标没有启用")
	}
	fact := autoSignInCommand{Version: 1, ID: id("sic_"), Instance: instance, Generation: gen, Revision: revision, Authority: authority, Event: source.Object{"action": "site_signin", "channel": channel, "user": user}}
	run, e := a.pluginStore.enqueueRunTx(ctx, tx, instance, "event", mustCleanSeedJSON(map[string]string{"event_id": fact.ID}))
	if e != nil {
		return PluginRun{}, e
	}
	fact.RunID = run.ID
	fact.InputSHA = deferredInputSHA(run.Input)
	sealed, e := a.pluginStore.seal("run_result", instance, "signin-command:"+fact.ID, mustCleanSeedJSON(fact))
	if e != nil {
		return PluginRun{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_signin_commands VALUES(?,?,?,?)`, fact.ID, instance, gen, sealed); e != nil {
		return PluginRun{}, e
	}
	if e = tx.Commit(); e != nil {
		return PluginRun{}, e
	}
	a.wakePlugins()
	return run, nil
}
func (a *App) readAutoSignInCommand(ctx context.Context, run PluginRun) (source.Object, error) {
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return nil, bad("SIGNIN_COMMAND_DENIED", "命令输入无效")
	}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_commands WHERE id=? AND instance_id=? AND generation=?`, token.ID, run.InstanceID, run.Generation).Scan(&sealed); e != nil {
		return nil, bad("SIGNIN_COMMAND_DENIED", "命令事实不存在")
	}
	raw, e := a.pluginStore.unseal("run_result", run.InstanceID, "signin-command:"+token.ID, sealed)
	if e != nil {
		return nil, e
	}
	var fact autoSignInCommand
	if cleanupJournalStrict(raw, &fact) != nil || fact.Version != 1 || fact.ID != token.ID || fact.Instance != run.InstanceID || fact.Generation != run.Generation || fact.Revision != run.ConfigRevision || fact.RunID != run.ID || fact.InputSHA != deferredInputSHA(run.Input) || fact.Event["action"] != "site_signin" {
		return nil, bad("SIGNIN_COMMAND_DENIED", "命令事实与执行不匹配")
	}
	if fact.Authority.Kind == "command" {
		if larkSourceText(fact.Event["channel"]) == "" || larkSourceText(fact.Event["user"]) == "" {
			return nil, bad("SIGNIN_COMMAND_DENIED", "命令事实缺少渠道或用户")
		}
	} else if e = a.validatePluginHTTPAuthorityTx(ctx, tx, fact.Authority); e != nil {
		return nil, e
	}
	return fact.Event, nil
}

// Only native producer facts select source event metadata. SDK input is never authority.
func (a *App) autoSignInTrustedCommand(ctx context.Context, run PluginRun, inv plugins.Invocation) (durable.Command, error) {
	switch run.Action {
	case "service":
		return a.autoSignInTrustedService(ctx, run, inv)
	case "site_deleted":
		return a.autoSignInTrustedSiteDeleted(ctx, run, inv)
	case "event":
		var eventInput map[string]json.RawMessage
		if json.Unmarshal(inv.Input, &eventInput) == nil && len(eventInput["batch_id"]) > 0 {
			return a.autoSignInTrustedSiteDeleted(ctx, run, inv)
		}
		event, err := a.readAutoSignInCommand(ctx, run)
		if err != nil {
			return durable.Command{}, err
		}
		return durable.Command{Action: "event", Event: &source.EventEnvelope{Data: event}}, nil
	default:
		return durable.Command{}, bad("SIGNIN_SOURCE_PENDING", "该签到入口尚无已核验的原始事件事实")
	}
}
