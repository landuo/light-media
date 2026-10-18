package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	cf "light-media/internal/plugins/cloudflarespeedtest"
	"light-media/internal/plugins/schedule"
)

func (c *cloudflareCall) guardTx(ctx context.Context, tx *sql.Tx) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	e := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, c.o.instance).Scan(&plugin, &version, &gen, &installed, &enabled, &revision)
	if e != nil || plugin != cf.PluginID || version != cf.Version || gen != c.o.generation || !installed || !enabled || revision != c.revision {
		return bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare安装或配置已变化")
	}
	r, e := c.o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, c.run.ID))
	if e != nil || r.State != "running" || r.Generation != c.run.Generation || r.ConfigRevision != c.run.ConfigRevision || r.Action != c.run.Action || !bytes.Equal(r.Input, c.run.Input) {
		return bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare执行身份已变化")
	}
	return nil
}
func (c *cloudflareCall) Guard(ctx context.Context) error {
	if c.uncertain {
		return bad("CLOUDFLARE_UNCERTAIN", "Cloudflare副作用结果待核对，已停止后续操作")
	}
	if c.run.Action == "api" {
		if e := c.o.a.validatePluginHTTPRun(ctx, c.run); e != nil {
			return e
		}
	}
	if c.run.Action == "event" || c.run.Action == "command" {
		if e := c.validateCommand(ctx); e != nil {
			return e
		}
	}
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return c.guardTx(ctx, tx)
}
func (o *pluginCloudflareOwner) Guard(ctx context.Context) error {
	if o.active == nil {
		return bad("CLOUDFLARE_SCOPE_DENIED", "Cloudflare没有活动宿主调用")
	}
	return o.active.Guard(ctx)
}
func (o *pluginCloudflareOwner) Now() time.Time {
	if o.a.pluginCloudflare.now != nil {
		return o.a.pluginCloudflare.now()
	}
	return time.Now()
}
func (c *cloudflareCall) Now() time.Time { return c.o.Now() }
func (c *cloudflareCall) now() time.Time { return c.Now() }
func (o *pluginCloudflareOwner) Log(level, text string) {
	if o.active != nil {
		o.a.pluginExecutionLog(PluginInstallation{PluginID: cf.PluginID, InstanceID: o.instance}, o.active.run, strings.ToUpper(level), "cloudflare_source", cleanWithSettings(text, o.a.Settings()), "")
	}
}
func (o *pluginCloudflareOwner) ScheduleOnce(_ context.Context, at time.Time) error {
	c := o.active
	if c.warming {
		return nil
	}
	c.defs = append(c.defs, pluginScheduleDefinition{ID: "CloudflareSpeedTest.once", Name: "Cloudflare优选", Handler: "cloudflare", Trigger: schedule.Spec{Kind: "date", RunAt: schedule.FormatTime(at), Timezone: o.timezone}, MisfireGraceMS: 1000})
	return nil
}
func (o *pluginCloudflareOwner) StartOnce(context.Context) error { return nil }
func (o *pluginCloudflareOwner) StopOnce(ctx context.Context) error {
	c := o.active
	if c.warming {
		return nil
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		c.defs = nil
		return nil
	}
	_, e := c.local(ctx, "stop_once", nil, func(tx *sql.Tx) (any, error) {
		_, e := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=? AND generation=? AND service_id='CloudflareSpeedTest.once'`, o.instance, o.generation)
		return true, e
	})
	return e
}
func (o *pluginCloudflareOwner) SaveConfig(ctx context.Context, value cf.Object) error {
	c := o.active
	if c.warming {
		return errors.New("Cloudflare cold restore attempted configuration write")
	}
	if c.run.Action == "init" || c.run.Action == "reload" {
		c.staged = value
		return nil
	}
	_, e := c.local(ctx, "own_config", value, func(tx *sql.Tx) (any, error) {
		v, e := o.a.pluginStore.saveValueTx(ctx, tx, o.instance, "config", "main", c.revision, mustCleanSeedJSON(value))
		if e != nil {
			return nil, e
		}
		old := c.revision
		c.revision = v.Revision
		_, e = tx.ExecContext(ctx, `UPDATE plugin_schedules SET config_revision=? WHERE instance_id=? AND generation=? AND group_key='cloudflare' AND config_revision=?`, v.Revision, o.instance, o.generation, old)
		return map[string]int64{"revision": v.Revision}, e
	})
	return e
}
func (o *pluginCloudflareOwner) CustomHosts(ctx context.Context) (cf.Object, error) {
	c := o.active
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	in, e := o.a.pluginStore.Installation(ctx, "CustomHosts")
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if in.PluginID != "CustomHosts" || !in.Installed || !in.DesiredEnabled {
		return nil, nil
	}
	v, e := o.a.pluginStore.Config(ctx, in.InstanceID)
	if e != nil {
		return nil, e
	}
	var value cf.Object
	if cleanupJournalStrict(v.Value, &value) != nil {
		return nil, bad("CLOUDFLARE_CUSTOM_HOSTS_INVALID", "CustomHosts配置无法读取")
	}
	c.customInstance = in
	c.customRevision = v.Revision
	return value, nil
}
func (c *cloudflareCall) customTargetTx(ctx context.Context, tx *sql.Tx) error {
	i := c.customInstance
	var plugin, version string
	var gen, revision int64
	var installed, enabled bool
	e := tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, i.InstanceID).Scan(&plugin, &version, &gen, &installed, &enabled, &revision)
	if e != nil || plugin != "CustomHosts" || version != i.Version || gen != i.Generation || !installed || !enabled || revision != c.customRevision {
		return bad("CLOUDFLARE_CUSTOM_HOSTS_CONFLICT", "CustomHosts已编辑、停用、重置或卸载，未覆盖新配置")
	}
	return nil
}
func (o *pluginCloudflareOwner) SaveCustomHosts(ctx context.Context, value cf.Object) error {
	c := o.active
	_, e := c.local(ctx, "custom_hosts", map[string]any{"instance": c.customInstance.InstanceID, "generation": c.customInstance.Generation, "revision": c.customRevision, "config": value}, func(tx *sql.Tx) (any, error) {
		if e := c.customTargetTx(ctx, tx); e != nil {
			return nil, e
		}
		v, e := o.a.pluginStore.saveValueTx(ctx, tx, c.customInstance.InstanceID, "config", "main", c.customRevision, mustCleanSeedJSON(value))
		if e != nil {
			return nil, e
		}
		c.customRevision = v.Revision
		return map[string]int64{"revision": v.Revision}, nil
	})
	return e
}
func (o *pluginCloudflareOwner) ReloadCustomHosts(ctx context.Context) error {
	c := o.active
	_, e := c.local(ctx, "custom_reload", map[string]any{"instance": c.customInstance.InstanceID, "generation": c.customInstance.Generation, "revision": c.customRevision}, func(tx *sql.Tx) (any, error) {
		if e := c.customTargetTx(ctx, tx); e != nil {
			return nil, e
		}
		run, e := o.a.pluginStore.enqueueRunTx(ctx, tx, c.customInstance.InstanceID, "reload", nil)
		if e != nil {
			return nil, e
		}
		return map[string]any{"run_id": run.ID, "instance_id": run.InstanceID, "generation": run.Generation, "revision": run.ConfigRevision}, nil
	})
	if e == nil {
		o.a.wakePlugins()
	}
	return e
}
func (o *pluginCloudflareOwner) PostMessage(ctx context.Context, m cf.Message) error {
	raw, e := json.Marshal(m)
	if e != nil {
		return e
	}
	var value cf.Object
	_ = json.Unmarshal(raw, &value)
	return o.active.notice(ctx, value)
}
func (o *pluginCloudflareOwner) SystemMessage(ctx context.Context, title, text string) error {
	c := o.active
	_, e := c.local(ctx, "system_notice", map[string]string{"title": title, "text": text}, func(tx *sql.Tx) (any, error) {
		n := Notification{ID: "notice_" + o.a.pluginStore.noticeID("cloudflare-system", o.instance, o.generation, c.run.ID), Title: title, Message: text, CreatedAt: c.Now().UTC().Format(time.RFC3339Nano)}
		return true, putCleanupNoticeTx(ctx, tx, n)
	})
	return e
}

func (c *cloudflareCall) ensureNative(ctx context.Context) error {
	if c.native != nil {
		return nil
	}
	_, e := c.effect(ctx, "runtime_prepare", nil, func() (any, error) {
		var e error
		if f := c.o.a.pluginCloudflare.nativeFactory; f != nil {
			c.native, e = f(c)
		} else {
			c.native, e = newCloudflareNative(ctx, c)
		}
		return true, e
	})
	return e
}
func (o *pluginCloudflareOwner) Platform() cf.Platform { return cloudflarePlatform() }
func (o *pluginCloudflareOwner) Exists(ctx context.Context, ref cf.FileRef) (bool, error) {
	c := o.active
	if e := c.ensureNative(ctx); e != nil {
		return false, e
	}
	if e := c.Guard(ctx); e != nil {
		return false, e
	}
	return c.native.Exists(ctx, ref)
}
func (o *pluginCloudflareOwner) Ensure(ctx context.Context) error {
	c := o.active
	if e := c.ensureNative(ctx); e != nil {
		return e
	}
	_, e := c.effect(ctx, "runtime_ensure", nil, func() (any, error) { return true, c.native.Ensure(ctx) })
	return e
}
func (o *pluginCloudflareOwner) Reset(ctx context.Context) error {
	c := o.active
	if e := c.ensureNative(ctx); e != nil {
		return e
	}
	_, e := c.effect(ctx, "runtime_reset", nil, func() (any, error) { return true, c.native.Reset(ctx) })
	return e
}
func (o *pluginCloudflareOwner) Latest(ctx context.Context, proxy bool) (cf.ReleaseResponse, error) {
	c := o.active
	if e := c.ensureNative(ctx); e != nil {
		return cf.ReleaseResponse{}, e
	}
	if e := c.Guard(ctx); e != nil {
		return cf.ReleaseResponse{}, e
	}
	return c.native.Latest(ctx, proxy)
}
func (o *pluginCloudflareOwner) Download(ctx context.Context, req cf.InstallRequest) error {
	c := o.active
	_, e := c.effect(ctx, "archive_download", req, func() (any, error) { return true, c.native.Download(ctx, req) })
	return e
}
func (o *pluginCloudflareOwner) Extract(ctx context.Context, req cf.InstallRequest) error {
	c := o.active
	_, e := c.effect(ctx, "archive_extract", req, func() (any, error) {
		e := c.native.Extract(ctx, req)
		if e != nil {
			return nil, e
		}
		if native, ok := c.native.(*cloudflareNative); ok {
			c.asset = native.proof
			return c.asset, nil
		}
		return true, nil
	})
	return e
}
func (o *pluginCloudflareOwner) Remove(ctx context.Context, name string) error {
	c := o.active
	_, e := c.effect(ctx, "archive_remove", name, func() (any, error) { return true, c.native.Remove(ctx, name) })
	return e
}
func (o *pluginCloudflareOwner) Run(ctx context.Context, spec cf.RunSpec) (cf.RunResult, error) {
	c := o.active
	v, e := c.effect(ctx, "cli", spec, func() (any, error) { return c.native.Run(ctx, spec) })
	if e != nil {
		return cf.RunResult{}, e
	}
	return v.(cf.RunResult), nil
}
func (o *pluginCloudflareOwner) ReadResult(ctx context.Context) ([]byte, error) {
	c := o.active
	if e := c.Guard(ctx); e != nil {
		return nil, e
	}
	return c.native.ReadResult(ctx)
}
