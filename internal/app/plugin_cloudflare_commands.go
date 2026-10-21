package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"light-media/internal/plugins"
	cf "light-media/internal/plugins/cloudflarespeedtest"
)

const cloudflareCommandSchema = `CREATE TABLE IF NOT EXISTS plugin_cloudflare_commands(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);`

type cloudflareCommand struct {
	Version                       int
	ID, Instance, RunID, InputSHA string
	Generation, Revision          int64
	Authority                     pluginAPIAuthority
	Event                         cf.Object
}

// queueCloudflareCommand is private to the trusted incoming command dispatcher.
// Existing live session/key authority is checked in the same transaction that
// creates the durable fact and invocation. A worker cannot call this function.
func (a *App) queueCloudflareCommand(ctx context.Context, instance string, authority pluginAPIAuthority, channel, user string) (PluginRun, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return PluginRun{}, e
	}
	defer tx.Rollback()
	if authority.Kind != "key" && authority.Kind != "session" && authority.Kind != "command" {
		return PluginRun{}, bad("CLOUDFLARE_COMMAND_DENIED", "命令来源未经鉴权")
	}
	if authority.Kind == "command" {
		// A message command carries the source channel/user instead of an HTTP
		// credential. The sealed fact below is only writable through this
		// authenticated dispatch path, so the fact itself is the evidence.
		if strings.TrimSpace(channel) == "" || strings.TrimSpace(user) == "" {
			return PluginRun{}, bad("CLOUDFLARE_COMMAND_DENIED", "命令来源缺少渠道或用户")
		}
	} else if e = a.validatePluginHTTPAuthorityTx(ctx, tx, authority); e != nil {
		return PluginRun{}, e
	}
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	e = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision)
	if e != nil || plugin != cf.PluginID || version != cf.Version || !installed || !enabled {
		return PluginRun{}, bad("CLOUDFLARE_COMMAND_DENIED", "命令目标没有启用")
	}
	fact := cloudflareCommand{Version: 1, ID: id("cfc_"), Instance: instance, Generation: gen, Revision: revision, Authority: authority, Event: cf.Object{"action": cf.Action, "channel": channel, "user": user}}
	run, e := a.pluginStore.enqueueRunTx(ctx, tx, instance, "command", mustCleanSeedJSON(map[string]string{"event_id": fact.ID}))
	if e != nil {
		return PluginRun{}, e
	}
	fact.RunID = run.ID
	fact.InputSHA = deferredInputSHA(run.Input)
	sealed, e := a.pluginStore.seal("run_result", instance, "cloudflare-command:"+fact.ID, mustCleanSeedJSON(fact))
	if e != nil {
		return PluginRun{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_cloudflare_commands VALUES(?,?,?,?)`, fact.ID, instance, gen, sealed); e != nil {
		return PluginRun{}, e
	}
	if e = tx.Commit(); e != nil {
		return PluginRun{}, e
	}
	a.wakePlugins()
	return run, nil
}
func (c *cloudflareCall) commandEvent(ctx context.Context) (cf.Object, error) {
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(c.run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return nil, bad("CLOUDFLARE_COMMAND_DENIED", "命令输入无效")
	}
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_cloudflare_commands WHERE id=? AND instance_id=? AND generation=?`, token.ID, c.o.instance, c.o.generation).Scan(&sealed); e != nil {
		return nil, bad("CLOUDFLARE_COMMAND_DENIED", "命令事实不存在")
	}
	raw, e := c.o.a.pluginStore.unseal("run_result", c.o.instance, "cloudflare-command:"+token.ID, sealed)
	if e != nil {
		return nil, e
	}
	var fact cloudflareCommand
	if cleanupJournalStrict(raw, &fact) != nil || fact.Version != 1 || fact.ID != token.ID || fact.Instance != c.o.instance || fact.Generation != c.o.generation || fact.Revision != c.run.ConfigRevision || fact.RunID != c.run.ID || fact.InputSHA != deferredInputSHA(c.run.Input) || fact.Event["action"] != cf.Action {
		return nil, bad("CLOUDFLARE_COMMAND_DENIED", "命令事实与执行不匹配")
	}
	if fact.Authority.Kind == "command" {
		if larkSourceText(fact.Event["channel"]) == "" || larkSourceText(fact.Event["user"]) == "" {
			return nil, bad("CLOUDFLARE_COMMAND_DENIED", "命令事实缺少渠道或用户")
		}
	} else if e = c.o.a.validatePluginHTTPAuthorityTx(ctx, tx, fact.Authority); e != nil {
		return nil, e
	}
	return fact.Event, nil
}
func (c *cloudflareCall) validateCommand(ctx context.Context) error {
	_, e := c.commandEvent(ctx)
	return e
}

// A source-owned config write advances the public value revision while the
// API's original immutable run identity stays fixed. Only the authenticated
// sequential own_config receipts establish this narrow continuation authority.
func (p *PluginStore) cloudflareAPIRevisionTx(ctx context.Context, tx *sql.Tx, run PluginRun) (int64, error) {
	s, _, e := p.cloudflareRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return 0, e
	}
	r, ok := s.Runs[run.ID]
	if !ok || r.Action != "api" || r.InputSHA != deferredInputSHA(run.Input) || r.Revision != run.ConfigRevision {
		return 0, cloudflareEvidenceError()
	}
	revision := run.ConfigRevision
	var final json.RawMessage
	for _, effect := range r.Effects {
		if effect.Kind != "own_config" {
			continue
		}
		var result struct {
			Revision int64 `json:"revision"`
		}
		if effect.Phase != "confirmed" || cleanupJournalStrict(effect.Result, &result) != nil || result.Revision != revision+1 {
			return 0, cloudflareEvidenceError()
		}
		revision = result.Revision
		final = effect.Args
	}
	if len(final) == 0 {
		return 0, errors.New("no Cloudflare published API revision")
	}
	var current int64
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&current, &sealed)
	if e != nil || current != revision {
		return 0, bad("PLUGIN_API_SCOPE_DENIED", "Cloudflare API配置已变化")
	}
	raw, e := p.unseal("config", run.InstanceID, "main", sealed)
	if e != nil || !bytes.Equal(raw, final) {
		return 0, cloudflareEvidenceError()
	}
	return revision, nil
}
