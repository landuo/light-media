package app

import (
	"context"
	"database/sql"
	"encoding/json"
	bf "light-media/internal/plugins/brushflow"
	"strings"
	"time"
)

const brushFlowEventSchema = `CREATE TABLE IF NOT EXISTS plugin_trigger_sources(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,run_id TEXT NOT NULL,ordinal INTEGER NOT NULL);`

type brushFlowEventProof struct {
	Version    int
	ID         string
	Event      DomainEvent
	AddOrdinal int
	Payload    json.RawMessage
}

func (o *pluginBrushFlowOwner) Event(ctx context.Context, event bf.Event) error {
	c := o.active
	if event.Name != "brushflow_download_added" || event.Hash == "" || event.Downloader == "" {
		return brushFlowEvidenceError()
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	p := o.a.pluginStore
	s, b, e := p.brushFlowRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	r := s.Runs[c.run.ID]
	if r.Phase != "started" {
		return brushFlowEvidenceError()
	}
	add := -1
	for i, f := range r.Effects {
		if f.Kind != "add_torrent" || f.Phase != "confirmed" {
			continue
		}
		var args struct {
			Hash       string `json:"hash"`
			Record     bf.Row `json:"record"`
			Downloader string `json:"downloader_name"`
		}
		var result struct {
			NotDispatched bool `json:"not_dispatched"`
			SourceTruthy  bool `json:"source_truthy"`
		}
		if brushFlowDecodeNumbers(f.Args, &args) == nil && json.Unmarshal(f.Result, &result) == nil && strings.EqualFold(args.Hash, event.Hash) && args.Downloader == event.Downloader && brushFlowSameJSON(args.Record, event.Data) && !result.NotDispatched && result.SourceTruthy {
			add = i
		}
	}
	if add < 0 {
		return brushFlowEvidenceError()
	}
	id := p.noticeID("brush-flow-trigger", o.instance, o.generation, c.run.ID, add)
	for _, f := range r.Effects {
		if f.Kind == "plugin_triggered" {
			var proof brushFlowEventProof
			if cleanupJournalStrict(f.Args, &proof) != nil {
				return brushFlowEvidenceError()
			}
			if proof.ID == id {
				return p.validateBrushFlowTriggered(ctx, tx, proof.Event)
			}
		}
	}
	payload := mustCleanSeedJSON(map[string]any{"plugin_id": bf.PluginID, "event_name": event.Name, "hash": event.Hash, "data": event.Data, "downloader": event.Downloader})
	fact := DomainEvent{SchemaVersion: 1, EventID: domainEventID("plugin.triggered", id), Type: "plugin.triggered", OccurredAt: o.Now().UTC().Format(time.RFC3339Nano), PluginTriggerID: id, SubjectID: id, SubjectKind: "plugin_trigger", Origin: "plugin", Purpose: "plugin_trigger"}
	proof := brushFlowEventProof{Version: 1, ID: id, Event: fact, AddOrdinal: add, Payload: payload}
	ordinal := len(r.Effects)
	r.Effects = append(r.Effects, brushFlowEffect{Kind: "plugin_triggered", Phase: "confirmed", Args: mustCleanSeedJSON(proof), Result: mustCleanSeedJSON(map[string]string{"event_id": fact.EventID})})
	s.Runs[c.run.ID] = r
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_trigger_sources VALUES(?,?,?,?,?)`, id, o.instance, o.generation, c.run.ID, ordinal); e != nil {
		return e
	}
	if e = p.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	if e = recordDomainEventTx(ctx, tx, fact); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return c.unknown(e)
	}
	return nil
}
func validatePluginTriggeredDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) { return in, bad("INVALID_DOMAIN_EVENT", "插件触发事件契约无效") }
	if in.SchemaVersion != 1 || in.Sequence != 0 || !pluginStorageDigest.MatchString(in.PluginTriggerID) || in.EventID != domainEventID("plugin.triggered", in.PluginTriggerID) || in.Type != "plugin.triggered" || in.SubjectID != in.PluginTriggerID || in.SubjectKind != "plugin_trigger" || in.Origin != "plugin" || in.Purpose != "plugin_trigger" {
		return invalid()
	}
	copy := in
	copy.OccurredAt = ""
	copy.EventID = ""
	copy.Type = ""
	copy.SchemaVersion = 0
	copy.PluginTriggerID = ""
	copy.SubjectID = ""
	copy.SubjectKind = ""
	copy.Origin = ""
	copy.Purpose = ""
	if copy != (DomainEvent{}) {
		return invalid()
	}
	at, e := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if e != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}
func (p *PluginStore) validateBrushFlowTriggered(ctx context.Context, q pluginRemoveLinkJournalReader, event DomainEvent) error {
	// Sequence is the trusted outbox cursor, not part of the source fact.
	event.Sequence = 0
	if db, ok := q.(*sql.DB); ok {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return e
		}
		defer tx.Rollback()
		return p.validateBrushFlowTriggered(ctx, tx, event)
	}
	var instance, run string
	var generation int64
	var ordinal int
	if e := q.QueryRowContext(ctx, `SELECT instance_id,generation,run_id,ordinal FROM plugin_trigger_sources WHERE id=?`, event.PluginTriggerID).Scan(&instance, &generation, &run, &ordinal); e != nil {
		return brushFlowEvidenceError()
	}
	s, _, e := p.brushFlowRead(ctx, q, instance, generation)
	if e != nil {
		return e
	}
	r, ok := s.Runs[run]
	if !ok || ordinal < 0 || ordinal >= len(r.Effects) {
		return brushFlowEvidenceError()
	}
	effect := r.Effects[ordinal]
	var proof brushFlowEventProof
	if effect.Kind != "plugin_triggered" || effect.Phase != "confirmed" || cleanupJournalStrict(effect.Args, &proof) != nil || proof.Version != 1 || proof.ID != event.PluginTriggerID || !brushFlowSameJSON(proof.Event, event) || proof.AddOrdinal < 0 || proof.AddOrdinal >= ordinal {
		return brushFlowEvidenceError()
	}
	add := r.Effects[proof.AddOrdinal]
	if add.Kind != "add_torrent" || add.Phase != "confirmed" || proof.ID != p.noticeID("brush-flow-trigger", instance, generation, run, proof.AddOrdinal) {
		return brushFlowEvidenceError()
	}
	var args struct {
		Hash       string `json:"hash"`
		Record     bf.Row `json:"record"`
		Downloader string `json:"downloader_name"`
	}
	var result struct {
		NotDispatched bool `json:"not_dispatched"`
		SourceTruthy  bool `json:"source_truthy"`
	}
	if brushFlowDecodeNumbers(add.Args, &args) != nil || json.Unmarshal(add.Result, &result) != nil || result.NotDispatched || !result.SourceTruthy {
		return brushFlowEvidenceError()
	}
	want := map[string]any{"plugin_id": bf.PluginID, "event_name": "brushflow_download_added", "hash": args.Hash, "data": args.Record, "downloader": args.Downloader}
	if !brushFlowSameJSON(want, json.RawMessage(proof.Payload)) || !brushFlowSameJSON(effect.Result, map[string]string{"event_id": event.EventID}) {
		return brushFlowEvidenceError()
	}
	return nil
}

// This resolver is only for the real registered PluginTriggered receiver host.
// Producing a fact does not register or invoke a made-up consumer.
func (p *PluginStore) brushFlowTriggeredPayload(ctx context.Context, q pluginRemoveLinkJournalReader, event DomainEvent) (json.RawMessage, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		return p.brushFlowTriggeredPayload(ctx, tx, event)
	}
	if e := p.validateBrushFlowTriggered(ctx, q, event); e != nil {
		return nil, e
	}
	var instance, run string
	var generation int64
	var ordinal int
	if e := q.QueryRowContext(ctx, `SELECT instance_id,generation,run_id,ordinal FROM plugin_trigger_sources WHERE id=?`, event.PluginTriggerID).Scan(&instance, &generation, &run, &ordinal); e != nil {
		return nil, e
	}
	s, _, e := p.brushFlowRead(ctx, q, instance, generation)
	if e != nil {
		return nil, e
	}
	var proof brushFlowEventProof
	json.Unmarshal(s.Runs[run].Effects[ordinal].Args, &proof)
	return proof.Payload, nil
}
