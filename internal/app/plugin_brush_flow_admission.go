package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
)

type brushFlowLocalCommitKey struct{}
type brushFlowLocalTransaction struct {
	c  *brushFlowCall
	tx *sql.Tx
	s  *brushFlowState
}

func brushFlowLocalState(ctx context.Context, p *PluginStore, tx *sql.Tx, instance string) (*brushFlowState, bool) {
	v, ok := ctx.Value(brushFlowLocalCommitKey{}).(*brushFlowLocalTransaction)
	if !ok || v == nil || v.tx != tx || v.c == nil || v.s == nil || v.c.o.a.pluginStore != p || v.c.o.instance != instance || v.s.Instance != instance || v.s.Generation != v.c.o.generation {
		return nil, false
	}
	return v.s, true
}

var errBrushFlowUnknownAdmission = errors.New("private BrushFlow unconfirmed admission")

func (p *PluginStore) brushFlowQueueAdmissionTx(ctx context.Context, tx *sql.Tx, instance, plugin, action string, input json.RawMessage) error {
	if plugin != bf.PluginID {
		return nil
	}
	if action == "api" {
		var q plugins.HTTPRequest
		if json.Unmarshal(input, &q) == nil && q.RequestID != "" {
			return nil
		}
	}
	if s, ok := brushFlowLocalState(ctx, p, tx, instance); ok {
		return brushFlowAdmissionState(ctx, p, *s)
	}
	var gen int64
	if e := tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&gen); e != nil {
		return e
	}
	s, _, e := p.brushFlowRead(ctx, tx, instance, gen)
	if e != nil {
		return e
	}
	return brushFlowAdmissionState(ctx, p, s)
}
func brushFlowAdmissionState(ctx context.Context, p *PluginStore, s brushFlowState) error {
	if local, ok := ctx.Value(brushFlowLocalCommitKey{}).(*brushFlowLocalTransaction); ok && local != nil && local.c != nil && local.c.o.a.pluginStore == p && local.s != nil && local.s.Instance == s.Instance && local.s.Generation == s.Generation {
		c := local.c
		r, ok := s.Runs[c.run.ID]
		if !ok || (r.Phase != "started" && r.Phase != "confirmed" && r.Phase != "failed") || !r.Entered || r.InputSHA != deferredInputSHA(c.run.Input) || r.Revision != c.run.ConfigRevision {
			return brushFlowEvidenceError()
		}
		copyRuns := map[string]brushFlowAction{}
		for id, v := range s.Runs {
			if id != c.run.ID {
				copyRuns[id] = v
			}
		}
		s.Runs = copyRuns
	}
	if e := brushFlowNoUnknown(p.brushFlowWithLiveState(s)); e != nil {
		return fmt.Errorf("%w: %w", errBrushFlowUnknownAdmission, e)
	}
	return nil
}
func (p *PluginStore) brushFlowConfigAdmissionTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) error {
	if s, ok := brushFlowLocalState(ctx, p, tx, instance); ok {
		return brushFlowAdmissionState(ctx, p, *s)
	}
	var gen int64
	if e := tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&gen); e != nil {
		return e
	}
	s, _, e := p.brushFlowRead(ctx, tx, instance, gen)
	if e != nil {
		return e
	}
	if _, e = brushFlowConfig(raw); e != nil {
		return e
	}
	return brushFlowAdmissionState(ctx, p, s)
}
