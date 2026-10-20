package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"light-media/internal/plugins/schedule"
	sr "light-media/internal/plugins/seedrescue"
)

func (c *seedRescueCall) guardTx(ctx context.Context, tx *sql.Tx) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := c.o.a.pluginStore.requireSiteReferenceProtocolTx(ctx, tx, c.o.instance); e != nil {
		return e
	}
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	e := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, c.o.instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision)
	if e != nil || plugin != sr.PluginID || version != sr.Version || gen != c.o.generation || !installed || !enabled || revision != c.revision {
		return bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种安装或配置已经变化")
	}
	r, e := c.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, c.run.ID))
	if e != nil || r.State != "running" || r.InstanceID != c.run.InstanceID || r.Version != c.run.Version || r.Generation != c.run.Generation || r.ConfigRevision != c.run.ConfigRevision || r.Action != c.run.Action || !bytes.Equal(r.Input, c.run.Input) {
		return bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种执行身份已经变化")
	}
	if c.run.Action == "api" {
		_, e = c.o.a.validatePluginHTTPRunTx(ctx, tx, c.run)
		if e != nil {
			return e
		}
	}
	return nil
}
func (c *seedRescueCall) Guard(ctx context.Context) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return c.guardTx(ctx, tx)
}
func (c *seedRescueCall) ioGuard(ctx context.Context) error {
	if c.uncertain.Load() {
		return bad("SEED_RESCUE_UNCERTAIN", "稀缺保种外部操作结果尚未核对")
	}
	return c.Guard(ctx)
}
func (o *pluginSeedRescueOwner) Guard(ctx context.Context) error {
	if o.active == nil {
		return seedRescueEvidenceError()
	}
	return o.active.Guard(ctx)
}
func (o *pluginSeedRescueOwner) Now() time.Time {
	if o.a.pluginSeedRescue.now != nil {
		return o.a.pluginSeedRescue.now()
	}
	return time.Now()
}
func (o *pluginSeedRescueOwner) Log(ctx context.Context, level, text string) {
	if o.active != nil {
		o.a.pluginExecutionLog(PluginInstallation{PluginID: sr.PluginID, InstanceID: o.instance}, o.active.run, strings.ToUpper(level), "seed_rescue_source", cleanWithSettings(text, o.a.Settings()), "")
	}
}
func (o *pluginSeedRescueOwner) ValidateCron(cron string) error {
	zone, e := autoBackupHostTimezone()
	if e != nil {
		return e
	}
	_, e = schedule.ParseCrontab(cron, zone)
	return e
}
func seedRescueDataKey(key string) bool {
	return key == sr.JobsKey || key == sr.ScanStatusKey || key == sr.RSSSnapshotsKey
}
func (o *pluginSeedRescueOwner) LoadData(ctx context.Context, key string) (json.RawMessage, error) {
	if !seedRescueDataKey(key) {
		return nil, seedRescueEvidenceError()
	}
	c := o.active
	if c == nil {
		return nil, seedRescueEvidenceError()
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return nil, e
	}
	s, _, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return nil, e
	}
	if _, ok := s.Values[key]; !ok {
		return nil, nil
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, o.instance, key).Scan(&sealed); e != nil {
		return nil, e
	}
	return o.a.pluginStore.unseal("data", o.instance, key, sealed)
}
func (o *pluginSeedRescueOwner) SaveData(ctx context.Context, key string, raw json.RawMessage) error {
	if !seedRescueDataKey(key) || !json.Valid(raw) {
		return seedRescueEvidenceError()
	}
	c := o.active
	if c == nil {
		return seedRescueEvidenceError()
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	s, b, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	revision := int64(0)
	if old, ok := s.Values[key]; ok {
		revision = old.Revision
	}
	value, e := o.a.pluginStore.saveValueTx(ctx, tx, o.instance, "data", key, revision, raw)
	if e != nil {
		return e
	}
	s.Values[key] = seedRescueValue{value.Revision, tokenHash(string(raw))}
	if e = o.a.pluginStore.seedRescueSave(ctx, tx, s, b); e != nil {
		return e
	}
	return tx.Commit()
}
func (o *pluginSeedRescueOwner) UpdateConfig(ctx context.Context, value sr.Object) error {
	c := o.active
	if c == nil || c.warming {
		return seedRescueEvidenceError()
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		c.staged = value
		return nil
	}
	// The only source write outside init clears future risk confirmations. Bind
	// the whole resulting config, retaining token/RSS and every other setting.
	want := sr.ParseConfig(c.config).Current()
	want["confirm_download_risk"] = false
	if !bytes.Equal(mustCleanSeedJSON(want), mustCleanSeedJSON(value)) {
		return bad("SEED_RESCUE_SCOPE_DENIED", "稀缺保种配置写入不符合原操作")
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	s, b, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	v, e := o.a.pluginStore.saveValueTx(ctx, tx, o.instance, "config", "main", c.revision, mustCleanSeedJSON(value))
	if e != nil {
		return e
	}
	r := s.Runs[c.run.ID]
	r.EffectiveRevision = v.Revision
	r.Effects = append(r.Effects, seedRescueEffect{Kind: "own_config", Phase: "confirmed", Args: mustCleanSeedJSON(map[string]any{"from_revision": c.revision, "to_revision": v.Revision, "config_sha": tokenHash(string(v.Value))}), Result: json.RawMessage(`null`)})
	s.Runs[c.run.ID] = r
	s.Config = v.Value
	s.AppliedRevision = v.Revision
	s.AppliedConfigSHA = tokenHash(string(v.Value))
	if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND group_key='seedrescue' AND config_revision=?`, v.Revision, o.instance, o.generation, c.revision); e != nil {
		return e
	}
	if e = o.a.pluginStore.seedRescueSave(ctx, tx, s, b); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		c.uncertain.Store(true)
		return e
	}
	c.revision = v.Revision
	c.config = value
	return nil
}
func (a *App) seedRescueHTTPRevisionTx(ctx context.Context, tx *sql.Tx, r PluginRun) error {
	var rev int64
	if e := tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, r.InstanceID).Scan(&rev); e != nil {
		return e
	}
	if rev == r.ConfigRevision {
		return nil
	}
	s, _, e := a.pluginStore.seedRescueRead(ctx, tx, r.InstanceID, r.Generation)
	if e != nil {
		return e
	}
	action, ok := s.Runs[r.ID]
	if !ok || action.Revision != r.ConfigRevision || action.EffectiveRevision != rev || action.InputSHA != deferredInputSHA(r.Input) || action.Action != r.Action {
		return bad("PLUGIN_API_SCOPE_DENIED", "稀缺保种API配置证据已失效")
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, r.InstanceID).Scan(&sealed); e != nil {
		return e
	}
	raw, e := a.pluginStore.unseal("config", r.InstanceID, "main", sealed)
	if e != nil {
		return e
	}
	if s.AppliedRevision != rev || !bytes.Equal(raw, s.Config) {
		return seedRescueEvidenceError()
	}
	previous := r.ConfigRevision
	for _, f := range action.Effects {
		if f.Kind != "own_config" {
			continue
		}
		var proof struct {
			From int64  `json:"from_revision"`
			To   int64  `json:"to_revision"`
			SHA  string `json:"config_sha"`
		}
		if f.Phase != "confirmed" || cleanupJournalStrict(f.Args, &proof) != nil || proof.From != previous || proof.To != previous+1 || proof.SHA != tokenHash(string(raw)) {
			return seedRescueEvidenceError()
		}
		previous = proof.To
	}
	if previous != rev {
		return seedRescueEvidenceError()
	}
	return nil
}
func (a *App) seedRescueSidebar(ctx context.Context, in PluginInstallation) ([]map[string]any, error) {
	empty := []map[string]any{}
	if in.PluginID != sr.PluginID || in.Version != sr.Version || !in.Installed || !in.DesiredEnabled {
		return empty, nil
	}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	s, _, e := a.pluginStore.seedRescueRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		return nil, e
	}
	var revision int64
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID).Scan(&revision, &sealed); errors.Is(e, sql.ErrNoRows) {
		return empty, nil
	} else if e != nil {
		return nil, e
	}
	raw, e := a.pluginStore.unseal("config", in.InstanceID, "main", sealed)
	if e != nil {
		return nil, e
	}
	if s.AppliedRevision != revision || len(s.Config) == 0 || seedRescueCanonicalSHA(raw) != s.AppliedConfigSHA {
		return empty, nil
	}
	cfg, e := seedRescueConfig(s.Config)
	if e != nil {
		return nil, e
	}
	if !sr.ParseConfig(cfg).Enabled {
		return empty, nil
	}
	return []map[string]any{{"nav_key": "main", "title": "稀缺保种", "icon": "mdi-seed-outline", "section": "organize", "permission": "manage", "order": 46}}, nil
}

func seedRescueCanonicalSHA(raw []byte) string {
	cfg, e := seedRescueConfig(raw)
	if e != nil {
		return ""
	}
	return tokenHash(string(mustCleanSeedJSON(cfg)))
}
