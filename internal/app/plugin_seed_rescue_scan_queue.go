package app

import (
	"context"
	"database/sql"
	"encoding/json"

	"light-media/internal/plugins"
	sr "light-media/internal/plugins/seedrescue"
)

type seedRescueScanSource struct {
	Token, OriginRun, Run, InputSHA string
	Generation, Revision            int64
}

func (c *seedRescueCall) queueScan(ctx context.Context, m *sr.Machine) (json.RawMessage, error) {
	valid, e := m.ValidConfig(ctx, false)
	if e != nil {
		return nil, e
	}
	if !valid {
		msg := "插件配置不完整或下载器不可用"
		return seedRescueHTTPResponse(sr.Response{Message: &msg}), nil
	}
	tx, e := c.o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return nil, e
	}
	s, b, e := c.o.a.pluginStore.seedRescueRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return nil, e
	}
	for _, source := range s.Sources {
		var state string
		if e = tx.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&state); e != nil {
			return nil, e
		}
		if state == "pending" || state == "running" {
			msg := "稀缺保种任务正在运行"
			return seedRescueHTTPResponse(sr.Response{Message: &msg}), nil
		}
	}
	token := id("ss_")
	raw := mustCleanSeedJSON(map[string]string{"event_id": token})
	run, e := c.o.a.pluginStore.enqueueRunTx(ctx, tx, c.o.instance, "scan", raw)
	if e != nil {
		return nil, e
	}
	s.Sources[token] = seedRescueScanSource{token, c.run.ID, run.ID, deferredInputSHA(run.Input), run.Generation, run.ConfigRevision}
	status := sr.Object{"state": "running", "stage": "preparing", "completed": 0, "total": 0, "started_at": c.o.Now().Format("2006-01-02T15:04:05-07:00"), "finished_at": nil, "errors": 0, "issues": []any{}, "message": "正在准备扫描"}
	value, e := c.o.a.pluginStore.saveValueTx(ctx, tx, c.o.instance, "data", sr.ScanStatusKey, s.Values[sr.ScanStatusKey].Revision, mustCleanSeedJSON(status))
	if e != nil {
		return nil, e
	}
	s.Values[sr.ScanStatusKey] = seedRescueValue{value.Revision, tokenHash(string(value.Value))}
	msg := "已提交后台扫描"
	out := seedRescueHTTPResponse(sr.Response{Success: true, Message: &msg, Data: sr.Object{"scan_status": status}})
	r := s.Runs[c.run.ID]
	r.Phase = "confirmed"
	r.Result = out
	s.Runs[c.run.ID] = r
	if e = c.o.a.pluginStore.seedRescueSave(ctx, tx, s, b); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		c.uncertain.Store(true)
		return nil, e
	}
	c.o.a.wakePlugins()
	return out, nil
}
func (c *seedRescueCall) validateScanSource(ctx context.Context) error {
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	s, _, e := c.o.a.pluginStore.seedRescueRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(c.run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return seedRescueEvidenceError()
	}
	source, ok := s.Sources[token.ID]
	if !ok || source.Run != c.run.ID || source.Generation != c.run.Generation || source.Revision != c.run.ConfigRevision || source.InputSHA != deferredInputSHA(c.run.Input) {
		return seedRescueEvidenceError()
	}
	return nil
}
func (p *PluginStore) seedRescueValidateSources(ctx context.Context, q pluginRemoveLinkJournalReader, s seedRescueState) error {
	if s.Sources == nil {
		return seedRescueEvidenceError()
	}
	for token, source := range s.Sources {
		if token != source.Token || !pluginStorageID.MatchString(token) || source.Generation != s.Generation || !pluginStorageID.MatchString(source.OriginRun) {
			return seedRescueEvidenceError()
		}
		origin, ok := s.Runs[source.OriginRun]
		if !ok || origin.Action != "api" || origin.Phase != "confirmed" || origin.Method != sr.APICapability {
			return seedRescueEvidenceError()
		}
		run, e := p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, source.Run))
		if e != nil {
			return seedRescueEvidenceError()
		}
		var inv plugins.Invocation
		var input struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &input) != nil || input.ID != token || run.Action != "scan" || inv.Action != "scan" || run.InstanceID != s.Instance || run.Generation != source.Generation || run.ConfigRevision != source.Revision || deferredInputSHA(run.Input) != source.InputSHA {
			return seedRescueEvidenceError()
		}
	}
	return nil
}

func seedRescueHasPendingSource(ctx context.Context, q pluginRemoveLinkJournalReader, s seedRescueState, except string) (bool, error) {
	for _, source := range s.Sources {
		if source.Run == except {
			continue
		}
		var state string
		if e := q.QueryRowContext(ctx, `SELECT state FROM plugin_runs WHERE id=?`, source.Run).Scan(&state); e != nil {
			return false, e
		}
		if state == "pending" || state == "running" {
			return true, nil
		}
	}
	return false, nil
}
