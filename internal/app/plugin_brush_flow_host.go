package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	bf "light-media/internal/plugins/brushflow"
	"strings"
	"time"
)

func (c *brushFlowCall) guardTx(ctx context.Context, tx *sql.Tx) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	var configSealed []byte
	e := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, c.o.instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision, &configSealed)
	if e != nil || plugin != bf.PluginID || version != bf.Version || gen != c.o.generation || !installed || !enabled {
		return bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流安装或配置已经变化")
	}
	r, e := c.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, c.run.ID))
	if e != nil || r.State != "running" || r.InstanceID != c.run.InstanceID || r.Version != c.run.Version || r.Generation != c.run.Generation || r.ConfigRevision != c.run.ConfigRevision || r.Action != c.run.Action || !bytes.Equal(r.Input, c.run.Input) {
		return bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流执行身份已经变化")
	}
	s, _, e := c.o.a.pluginStore.brushFlowReadCurrent(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	accepted, ok := s.lookupRun(c.run.ID)
	if ok {
		if e := c.o.a.pluginStore.brushFlowValidateAction(ctx, tx, s, accepted); e != nil {
			return e
		}
	}
	if !ok || accepted.Phase != "started" || accepted.InputSHA != deferredInputSHA(c.run.Input) || accepted.Revision != c.run.ConfigRevision || accepted.EffectiveRevision != revision || accepted.Method != c.method || (c.run.Action != "init" && c.run.Action != "reload" && s.AppliedRevision != revision) {
		return bad("BRUSH_FLOW_SCOPE_DENIED", "站点刷流接受配置证据已经变化")
	}
	if s.AppliedRevision == revision {
		actual, err := c.o.a.pluginStore.unseal("config", c.o.instance, "main", configSealed)
		if err != nil || !bytes.Equal(actual, s.Config) {
			return brushFlowEvidenceError()
		}
	}
	if e = brushFlowAdmissionState(ctx, c.o.a.pluginStore, s); e != nil {
		return e
	}
	if revision != c.revision {
		cfg, err := brushFlowConfig(s.Config)
		if err != nil {
			return err
		}
		c.revision, c.config = revision, cfg
	}

	if c.run.Action == "api" {
		_, e = c.o.a.validatePluginHTTPRunTx(ctx, tx, c.run)
	}
	return e
}
func (c *brushFlowCall) Guard(ctx context.Context) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return c.guardTx(ctx, tx)
}

type brushFlowBoundaryError struct{ cause error }

func (e *brushFlowBoundaryError) Error() string             { return e.cause.Error() }
func (e *brushFlowBoundaryError) Unwrap() error             { return e.cause }
func (e *brushFlowBoundaryError) StopExternalEffects() bool { return true }
func (c *brushFlowCall) ioGuard(ctx context.Context) error {
	if c.uncertain.Load() {
		return &brushFlowBoundaryError{bad("BRUSH_FLOW_UNCERTAIN", "站点刷流外部操作结果尚未核对")}
	}
	if e := c.Guard(ctx); e != nil {
		return &brushFlowBoundaryError{e}
	}
	return nil
}
func (o *pluginBrushFlowOwner) Now() time.Time {
	at := time.Now()
	if o.a.pluginBrushFlow.now != nil {
		at = o.a.pluginBrushFlow.now()
	}
	if z, e := autoBackupHostTimezone(); e == nil {
		if loc, e := time.LoadLocation(z); e == nil {
			at = at.In(loc)
		}
	}
	return at.Truncate(time.Microsecond)
}
func (o *pluginBrushFlowOwner) NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return hex.EncodeToString(b)
}
func (o *pluginBrushFlowOwner) RandomTag() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, 10)
	for i := range out {
		b := []byte{0}
		for {
			_, _ = rand.Read(b)
			if int(b[0]) < 248 {
				out[i] = letters[int(b[0])%len(letters)]
				break
			}
		}
	}
	return string(out)
}
func (o *pluginBrushFlowOwner) Sleep(ctx context.Context, d time.Duration) error {
	if o.a.pluginBrushFlow.sleep != nil {
		return o.a.pluginBrushFlow.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginBrushFlowOwner) Log(ctx context.Context, level, s string) {
	if o.call(ctx) != nil {
		o.a.pluginExecutionLog(PluginInstallation{PluginID: bf.PluginID, InstanceID: o.instance}, o.call(ctx).run, strings.ToUpper(level), "brush_flow_source", cleanWithSettings(s, o.a.Settings()), "")
	}
}
func brushFlowTaskIDs(cfg bf.Row) []string {
	var v struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	json.Unmarshal(mustCleanSeedJSON(cfg), &v)
	ids := []string{}
	for _, t := range v.Tasks {
		ids = append(ids, t.ID)
	}
	return ids
}
func brushFlowDataKey(c *brushFlowCall, key string) bool {
	if key == "task_data_schema_version" {
		return true
	}
	if (c.run.Action == "init" || c.run.Action == "reload") && (key == "torrents" || key == "archived" || key == "unmanaged") {
		return true
	}
	ids := brushFlowTaskIDs(c.config)
	if c.staged != nil {
		ids = append(ids, brushFlowTaskIDs(c.staged)...)
	}
	var inv struct {
		Config bf.Row `json:"config"`
	}
	json.Unmarshal(c.run.Input, &inv)
	ids = append(ids, brushFlowTaskIDs(inv.Config)...)
	for _, id := range ids {
		for _, n := range bf.DataNames {
			if key == "task."+id+"."+n {
				return true
			}
		}
	}
	return false
}
func (o *pluginBrushFlowOwner) Get(ctx context.Context, key string) (any, error) {
	c := o.call(ctx)
	if c == nil {
		return nil, brushFlowEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	if !brushFlowDataKey(c, key) {
		return nil, brushFlowEvidenceError()
	}
	if value, ok := c.writes[key]; ok {
		if value == nil {
			return nil, nil
		}
		return brushFlowDecodeValue(value)
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return nil, e
	}
	s, _, e := o.a.pluginStore.brushFlowReadCurrent(ctx, tx, o.instance, o.generation)
	if e != nil {
		return nil, e
	}
	raw, exists, e := o.a.pluginStore.brushFlowValueProof(ctx, tx, s, key)
	if e != nil {
		return nil, e
	}
	if !exists {
		return nil, nil
	}
	return brushFlowDecodeValue(raw)
}
func brushFlowDecodeValue(raw json.RawMessage) (any, error) {
	if len(raw) > 0 && raw[0] == '{' {
		return append(json.RawMessage{}, raw...), nil
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e := d.Decode(&v)
	return v, e
}
func (o *pluginBrushFlowOwner) Put(ctx context.Context, key string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return o.write(ctx, key, raw)
}
func (o *pluginBrushFlowOwner) Del(ctx context.Context, key string) error {
	return o.write(ctx, key, nil)
}
func (o *pluginBrushFlowOwner) write(ctx context.Context, key string, raw json.RawMessage) error {
	c := o.call(ctx)
	if c == nil || len(raw) > pluginDataValueLimit || raw != nil && !json.Valid(raw) {
		return brushFlowEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return e
	}
	if !brushFlowDataKey(c, key) {
		return brushFlowEvidenceError()
	}
	if c.run.Action == "api" || c.run.Action == "init" || c.run.Action == "reload" {
		c.writes[key] = raw
		return nil
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	s, b, e := o.a.pluginStore.brushFlowReadCurrent(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	if e = c.writeValueTx(ctx, tx, &s, key, raw); e != nil {
		return e
	}
	if e = o.a.pluginStore.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	return tx.Commit()
}
func (c *brushFlowCall) writeValueTx(ctx context.Context, tx *sql.Tx, s *brushFlowState, key string, raw json.RawMessage) error {
	if _, _, e := c.o.a.pluginStore.brushFlowValueProof(ctx, tx, *s, key); e != nil {
		return e
	}
	if raw == nil {
		if _, e := tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, c.o.instance, key); e != nil {
			return e
		}
		s.deleteValue(key)
		return nil
	}
	revision := s.value(key).Revision
	v, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "data", key, revision, raw)
	if e != nil {
		return e
	}
	s.setValue(key, brushFlowValue{v.Revision, tokenHash(string(raw))})
	return nil
}
func (o *pluginBrushFlowOwner) SaveConfig(ctx context.Context, value bf.Row) error {
	c := o.call(ctx)
	if c == nil {
		return brushFlowEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return e
	}
	if c.run.Action != "init" && c.run.Action != "reload" && c.run.Action != "api" {
		return brushFlowEvidenceError()
	}
	c.staged = value
	return nil
}
func (o *pluginBrushFlowOwner) RefreshSchedules(ctx context.Context, services []bf.Service) error {
	if o.call(ctx) == nil {
		return brushFlowEvidenceError()
	}
	o.call(ctx).services = services
	return nil
}
func (a *App) brushFlowSidebar(ctx context.Context, in PluginInstallation) ([]map[string]any, error) {
	empty := []map[string]any{}
	if in.PluginID != bf.PluginID || in.Version != bf.Version || !in.Installed || !in.DesiredEnabled {
		return empty, nil
	}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	s, _, e := a.pluginStore.brushFlowReadCurrent(ctx, tx, in.InstanceID, in.Generation)
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
	if s.AppliedRevision != revision || len(s.Config) == 0 || brushFlowCanonicalSHA(raw) != s.AppliedConfigSHA {
		return empty, nil
	}
	var cfg bf.Row
	if json.Unmarshal(s.Config, &cfg) != nil {
		return nil, brushFlowEvidenceError()
	}
	value, e := bf.LoadApplied(cfg)
	if e != nil {
		return nil, e
	}
	if !value.Enabled() || cfg["show_sidebar_nav"] != true {
		return empty, nil
	}
	return []map[string]any{{"nav_key": "main", "title": "站点刷流", "icon": "mdi-sync", "section": "organize", "permission": "manage", "order": 45}}, nil
}
func brushFlowCanonicalSHA(raw []byte) string {
	v, e := brushFlowConfig(raw)
	if e != nil {
		return ""
	}
	return tokenHash(string(mustCleanSeedJSON(v)))
}

func (a *App) brushFlowHTTPRevisionTx(ctx context.Context, tx *sql.Tx, r PluginRun) error {
	var rev int64
	if e := tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, r.InstanceID).Scan(&rev); e != nil {
		return e
	}
	if rev == r.ConfigRevision {
		return nil
	}
	s, _, e := a.pluginStore.brushFlowReadCurrent(ctx, tx, r.InstanceID, r.Generation)
	if e != nil {
		return e
	}
	action, ok := s.lookupRun(r.ID)
	if !ok || action.Revision != r.ConfigRevision || action.EffectiveRevision != rev || action.InputSHA != deferredInputSHA(r.Input) || action.Action != r.Action {
		return bad("PLUGIN_API_SCOPE_DENIED", "站点刷流API配置证据已失效")
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
		return brushFlowEvidenceError()
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
			return brushFlowEvidenceError()
		}
		previous = proof.To
	}
	if previous != rev {
		return brushFlowEvidenceError()
	}
	return nil
}
