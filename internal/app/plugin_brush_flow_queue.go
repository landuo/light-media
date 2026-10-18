package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
)

type brushFlowScanSource struct {
	Token, OriginRun, Run, InputSHA, TaskID, Operation string
	Generation, Revision                               int64
	TaskSnapshot                                       json.RawMessage
}
type brushFlowQueued struct {
	TaskID, Operation string
	Snapshot          json.RawMessage
}

func (o *pluginBrushFlowOwner) Queue(ctx context.Context, task, operation string) error {
	c := o.call(ctx)
	if c == nil {
		return brushFlowEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return e
	}
	switch operation {
	case "brush", "check", "cleanup_tags", "cleanup_task_tag":
	default:
		return brushFlowEvidenceError()
	}
	if c.run.Action == "api" {
		q, e := brushFlowRequest(c.run)
		if e != nil {
			return e
		}
		if q.Params["task_id"] != task {
			return brushFlowEvidenceError()
		}
		if q.Key == "run" && operation != "brush" || q.Key == "check" && operation != "check" || q.Key == "delete" && operation != "cleanup_task_tag" || q.Key != "run" && q.Key != "check" && q.Key != "delete" {
			return brushFlowEvidenceError()
		}
	} else if c.run.Action != "init" && c.run.Action != "reload" {
		return brushFlowEvidenceError()
	}
	cfg := c.config
	if c.staged != nil {
		cfg = c.staged
	}
	snapshot := json.RawMessage(nil)
	for _, v := range append(brushFlowTasks(cfg), brushFlowTasks(c.config)...) {
		if v.ID() == task {
			snapshot = mustCleanSeedJSON(v.Row)
			break
		}
	}
	if task != "" && snapshot == nil {
		return brushFlowEvidenceError()
	}
	if task == "" && operation != "cleanup_tags" {
		return brushFlowEvidenceError()
	}
	c.queued = append(c.queued, brushFlowQueued{task, operation, snapshot})
	return nil
}
func brushFlowTasks(raw bf.Row) []bf.Task {
	cfg, e := bf.LoadApplied(raw)
	if e != nil {
		return nil
	}
	return cfg.Tasks
}
func (c *brushFlowCall) publishQueuedTx(ctx context.Context, tx *sql.Tx, s *brushFlowState) error {
	for _, q := range c.queued {
		token := id("bq_")
		input := mustCleanSeedJSON(map[string]string{"event_id": token})
		run, e := c.o.a.pluginStore.enqueueRunTx(ctx, tx, c.o.instance, "task", input)
		if e != nil {
			return e
		}
		source := brushFlowScanSource{Token: token, OriginRun: c.run.ID, Run: run.ID, InputSHA: deferredInputSHA(run.Input), TaskID: q.TaskID, Operation: q.Operation, Generation: run.Generation, Revision: run.ConfigRevision, TaskSnapshot: q.Snapshot}
		s.Sources[token] = source
		r := s.Runs[c.run.ID]
		r.Effects = append(r.Effects, brushFlowEffect{Kind: "queue", Phase: "confirmed", Args: mustCleanSeedJSON(source), Result: json.RawMessage(`null`)})
		s.Runs[c.run.ID] = r
	}
	return nil
}
func (p *PluginStore) brushFlowValidateSources(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState) error {
	if s.Sources == nil {
		return brushFlowEvidenceError()
	}
	for token, source := range s.Sources {
		if token != source.Token || !pluginStorageID.MatchString(token) || source.Generation != s.Generation || !pluginStorageID.MatchString(source.OriginRun) {
			return brushFlowEvidenceError()
		}
		origin, ok := s.Runs[source.OriginRun]
		if !ok || (origin.Phase != "confirmed" && !(origin.Phase == "failed" && origin.ErrorCode == "BRUSH_FLOW_SOURCE_FAILED")) || (origin.Method != bf.APICapability && origin.Method != bf.ConfigureCapability) {
			return brushFlowEvidenceError()
		}
		found := false
		for _, effect := range origin.Effects {
			if effect.Kind == "queue" && effect.Phase == "confirmed" && bytes.Equal(effect.Args, mustCleanSeedJSON(source)) && string(effect.Result) == "null" {
				found = true
			}
		}
		if !found {
			return brushFlowEvidenceError()
		}
		run, e := p.scanPluginRun(q.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, source.Run))
		if e != nil {
			return brushFlowEvidenceError()
		}
		var inv plugins.Invocation
		var tokenInput struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &tokenInput) != nil || tokenInput.ID != token || run.Action != "task" || inv.Action != "task" || run.InstanceID != s.Instance || run.Generation != source.Generation || run.ConfigRevision != source.Revision || deferredInputSHA(run.Input) != source.InputSHA {
			return brushFlowEvidenceError()
		}
	}
	return nil
}
func brushFlowHasPendingSource(ctx context.Context, q pluginRemoveLinkJournalReader, s brushFlowState, except string) (bool, error) {
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
func (c *brushFlowCall) sourceOperation(ctx context.Context) (brushFlowQueued, error) {
	var inv plugins.Invocation
	var token struct {
		ID string `json:"event_id"`
	}
	if cleanupJournalStrict(c.run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	tx, e := c.o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return brushFlowQueued{}, e
	}
	defer tx.Rollback()
	s, _, e := c.o.a.pluginStore.brushFlowRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return brushFlowQueued{}, e
	}
	source, ok := s.Sources[token.ID]
	if !ok || source.Run != c.run.ID || source.InputSHA != deferredInputSHA(c.run.Input) || source.Revision != c.run.ConfigRevision {
		return brushFlowQueued{}, brushFlowEvidenceError()
	}
	return brushFlowQueued{source.TaskID, source.Operation, source.TaskSnapshot}, nil
}
