package app

import (
	"bytes"
	"context"

	"encoding/json"
	"fmt"
	"time"

	"light-media/internal/notice"
	bf "light-media/internal/plugins/brushflow"
)

type brushFlowNoticeProof struct {
	Version                                                                                           int
	Run, InputSHA, PayloadSHA, CreatedAt, CurrentTime, Timezone, HistoryID, HistoryDigest, InitialSHA string
	Ordinal                                                                                           int
	Payload, Envelope                                                                                 json.RawMessage
}

func (o *pluginBrushFlowOwner) Message(ctx context.Context, n bf.Notice) error {
	if n.System {
		return o.brushFlowSystemMessage(ctx, n)
	}
	payload := map[string]any{"mtype": "站点", "title": n.Title, "text": n.Text}
	c := o.call(ctx)
	if c == nil {
		return brushFlowEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return e
	}
	raw := mustCleanSeedJSON(payload)
	var input notice.Object
	if json.Unmarshal(raw, &input) != nil {
		return brushFlowEvidenceError()
	}
	if input["mtype"] == "SiteMessage" {
		input["mtype"] = "站点"
	}
	env, e := notice.NewEnvelope(input)
	if e != nil {
		return e
	}
	envelope := mustCleanSeedJSON(env.Dump())
	if _, e = o.a.pluginStore.noticePeriods(ctx); e != nil {
		return e
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = c.guardTx(ctx, tx); e != nil {
		return e
	}
	s, b, e := o.a.pluginStore.brushFlowRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	r := s.Runs[c.run.ID]
	if r.Phase != "started" {
		return brushFlowEvidenceError()
	}
	ordinal := len(r.Effects)
	zoneName, e := autoBackupHostTimezone()
	if e != nil {
		return e
	}
	zone, e := time.LoadLocation(zoneName)
	if e != nil {
		return e
	}
	at := o.Now()
	p := o.a.pluginStore
	token := p.noticeID("brush-flow-source", o.instance, o.generation, c.run.ID, ordinal)
	proof := brushFlowNoticeProof{Version: 1, Run: c.run.ID, InputSHA: deferredInputSHA(c.run.Input), PayloadSHA: tokenHash(string(raw)), Payload: raw, Envelope: envelope, Ordinal: ordinal, CreatedAt: at.UTC().Format(time.RFC3339Nano), CurrentTime: at.In(zone).Format("2006-01-02 15:04:05"), Timezone: zoneName}
	if env.Get("save_history") == true {
		title, _ := env.Get("title").(string)
		text, _ := env.Get("text").(string)
		mtype, _ := env.Get("mtype").(string)
		link, _ := env.Get("link").(string)
		proof.HistoryID = "notice_" + p.noticeID("brush-flow-history", token)
		n := Notification{ID: proof.HistoryID, Title: cleanWithSettings(title, o.a.Settings()), Message: cleanWithSettings(text, o.a.Settings()), MType: mtype, Link: link, CreatedAt: proof.CreatedAt}
		proof.HistoryDigest = p.noticeID("brush-flow-history-value", n)
		if e = putCleanupNoticeTx(ctx, tx, n); e != nil {
			return e
		}
	}
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", o.instance, o.generation, token), Kind: "brush_flow", Instance: o.instance, Generation: o.generation, Token: token, EffectID: fmt.Sprintf("%s:%d", c.run.ID, ordinal), AcceptedSHA: tokenHash(string(raw)), Envelope: envelope, CreatedAt: proof.CreatedAt, CurrentTime: proof.CurrentTime, HistoryID: proof.HistoryID, HistoryDigest: []byte(proof.HistoryDigest)}
	proof.InitialSHA = noticeInitialSHA(route)
	r.Effects = append(r.Effects, brushFlowEffect{Kind: "notice", Phase: "confirmed", Args: mustCleanSeedJSON(proof), Result: mustCleanSeedJSON(map[string]string{"route_id": route.ID, "initial_sha": proof.InitialSHA})})
	s.Runs[c.run.ID] = r
	if e = p.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	sealed, e := p.noticeSeal("route", route.ID, route)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		c.uncertain.Store(true)
		return e
	}
	e = o.a.runNoticeRoute(ctx, route.ID)
	if e != nil {
		state, _, readErr := p.readNoticeRoute(context.WithoutCancel(ctx), o.a.Store.DB, route.ID)
		if readErr != nil || !state.Complete {
			c.uncertain.Store(true)
		}
	}
	return e
}
func (p *PluginStore) validateBrushFlowNotice(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) error {
	s, _, e := p.brushFlowRead(ctx, q, route.Instance, route.Generation)
	if e != nil {
		return e
	}
	for _, r := range s.Runs {
		for ordinal, effect := range r.Effects {
			if effect.Kind != "notice" {
				continue
			}
			token := p.noticeID("brush-flow-source", route.Instance, route.Generation, r.RunID, ordinal)
			if token != route.Token {
				continue
			}
			var proof brushFlowNoticeProof
			if effect.Phase != "confirmed" || cleanupJournalStrict(effect.Args, &proof) != nil || proof.Version != 1 || proof.Run != r.RunID || proof.InputSHA != r.InputSHA || proof.Ordinal != ordinal || proof.PayloadSHA != tokenHash(string(proof.Payload)) {
				return brushFlowEvidenceError()
			}
			var input notice.Object
			if json.Unmarshal(proof.Payload, &input) != nil {
				return brushFlowEvidenceError()
			}
			if input["mtype"] == "SiteMessage" {
				input["mtype"] = "站点"
			}
			env, e := notice.NewEnvelope(input)
			if e != nil || !bytes.Equal(mustCleanSeedJSON(env.Dump()), proof.Envelope) {
				return brushFlowEvidenceError()
			}
			at, e := time.Parse(time.RFC3339Nano, proof.CreatedAt)
			zone, ze := time.LoadLocation(proof.Timezone)
			if e != nil || ze != nil || proof.CurrentTime != at.In(zone).Format("2006-01-02 15:04:05") {
				return brushFlowEvidenceError()
			}
			if route.EffectID != fmt.Sprintf("%s:%d", r.RunID, ordinal) || route.AcceptedSHA != proof.PayloadSHA || !bytes.Equal(route.Envelope, proof.Envelope) || route.CreatedAt != proof.CreatedAt || route.CurrentTime != proof.CurrentTime || route.HistoryID != proof.HistoryID || !bytes.Equal(route.HistoryDigest, []byte(proof.HistoryDigest)) || noticeInitialSHA(route) != proof.InitialSHA {
				return brushFlowEvidenceError()
			}
			var result struct {
				ID  string `json:"route_id"`
				SHA string `json:"initial_sha"`
			}
			if cleanupJournalStrict(effect.Result, &result) != nil || result.ID != route.ID || result.SHA != proof.InitialSHA {
				return brushFlowEvidenceError()
			}
			if proof.HistoryID != "" {
				var raw []byte
				if e = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, proof.HistoryID).Scan(&raw); e != nil {
					return e
				}
				var n Notification
				if json.Unmarshal(raw, &n) != nil {
					return brushFlowEvidenceError()
				}
				n.Read = false
				if p.noticeID("brush-flow-history-value", n) != proof.HistoryDigest {
					return brushFlowEvidenceError()
				}
			}
			return nil
		}
	}
	return brushFlowEvidenceError()
}

func (o *pluginBrushFlowOwner) brushFlowSystemMessage(ctx context.Context, n bf.Notice) error {
	c := o.call(ctx)
	if c == nil {
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
	ordinal := len(r.Effects)
	id := "notice_" + p.noticeID("brush-flow-system", o.instance, o.generation, c.run.ID, ordinal)
	notice := Notification{ID: id, Title: cleanWithSettings(n.Title, o.a.Settings()), Message: cleanWithSettings(n.Text, o.a.Settings()), MType: "站点", CreatedAt: o.Now().UTC().Format(time.RFC3339Nano)}
	if e = putCleanupNoticeTx(ctx, tx, notice); e != nil {
		return e
	}
	r.Effects = append(r.Effects, brushFlowEffect{Kind: "system_message", Phase: "confirmed", Args: mustCleanSeedJSON(notice), Result: mustCleanSeedJSON(map[string]string{"history_id": id, "digest": p.noticeID("brush-flow-system-value", notice)})})
	s.Runs[c.run.ID] = r
	if e = p.brushFlowSave(ctx, tx, s, b); e != nil {
		return e
	}
	return tx.Commit()
}
