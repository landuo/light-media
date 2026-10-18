package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"light-media/internal/notice"
	sr "light-media/internal/plugins/seedrescue"
)

type seedRescueNoticeProof struct {
	Version                                                                                           int
	Run, InputSHA, PayloadSHA, CreatedAt, CurrentTime, Timezone, HistoryID, HistoryDigest, InitialSHA string
	Ordinal                                                                                           int
	Payload, Envelope                                                                                 json.RawMessage
}

func (o *pluginSeedRescueOwner) PostMessage(ctx context.Context, payload sr.Object) error {
	c := o.active
	if c == nil {
		return seedRescueEvidenceError()
	}
	if e := c.Guard(ctx); e != nil {
		return e
	}
	raw := mustCleanSeedJSON(payload)
	var input notice.Object
	if json.Unmarshal(raw, &input) != nil {
		return seedRescueEvidenceError()
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
	s, b, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
	if e != nil {
		return e
	}
	r := s.Runs[c.run.ID]
	if r.Phase != "started" {
		return seedRescueEvidenceError()
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
	token := p.noticeID("seed-rescue-source", o.instance, o.generation, c.run.ID, ordinal)
	proof := seedRescueNoticeProof{Version: 1, Run: c.run.ID, InputSHA: deferredInputSHA(c.run.Input), PayloadSHA: tokenHash(string(raw)), Payload: raw, Envelope: envelope, Ordinal: ordinal, CreatedAt: at.UTC().Format(time.RFC3339Nano), CurrentTime: at.In(zone).Format("2006-01-02 15:04:05"), Timezone: zoneName}
	if env.Get("save_history") == true {
		title, _ := env.Get("title").(string)
		text, _ := env.Get("text").(string)
		mtype, _ := env.Get("mtype").(string)
		link, _ := env.Get("link").(string)
		proof.HistoryID = "notice_" + p.noticeID("seed-rescue-history", token)
		n := Notification{ID: proof.HistoryID, Title: cleanWithSettings(title, o.a.Settings()), Message: cleanWithSettings(text, o.a.Settings()), MType: mtype, Link: link, CreatedAt: proof.CreatedAt}
		proof.HistoryDigest = p.noticeID("seed-rescue-history-value", n)
		if e = putCleanupNoticeTx(ctx, tx, n); e != nil {
			return e
		}
	}
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", o.instance, o.generation, token), Kind: "seed_rescue", Instance: o.instance, Generation: o.generation, Token: token, EffectID: fmt.Sprintf("%s:%d", c.run.ID, ordinal), AcceptedSHA: tokenHash(string(raw)), Envelope: envelope, CreatedAt: proof.CreatedAt, CurrentTime: proof.CurrentTime, HistoryID: proof.HistoryID, HistoryDigest: []byte(proof.HistoryDigest)}
	proof.InitialSHA = noticeInitialSHA(route)
	r.Effects = append(r.Effects, seedRescueEffect{Kind: "notice", Phase: "confirmed", Args: mustCleanSeedJSON(proof), Result: mustCleanSeedJSON(map[string]string{"route_id": route.ID, "initial_sha": proof.InitialSHA})})
	s.Runs[c.run.ID] = r
	if e = p.seedRescueSave(ctx, tx, s, b); e != nil {
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
func (p *PluginStore) validateSeedRescueNotice(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) error {
	s, _, e := p.seedRescueRead(ctx, q, route.Instance, route.Generation)
	if e != nil {
		return e
	}
	for _, r := range s.Runs {
		for ordinal, effect := range r.Effects {
			if effect.Kind != "notice" {
				continue
			}
			token := p.noticeID("seed-rescue-source", route.Instance, route.Generation, r.RunID, ordinal)
			if token != route.Token {
				continue
			}
			var proof seedRescueNoticeProof
			if effect.Phase != "confirmed" || cleanupJournalStrict(effect.Args, &proof) != nil || proof.Version != 1 || proof.Run != r.RunID || proof.InputSHA != r.InputSHA || proof.Ordinal != ordinal || proof.PayloadSHA != tokenHash(string(proof.Payload)) {
				return seedRescueEvidenceError()
			}
			var input notice.Object
			if json.Unmarshal(proof.Payload, &input) != nil {
				return seedRescueEvidenceError()
			}
			if input["mtype"] == "SiteMessage" {
				input["mtype"] = "站点"
			}
			env, e := notice.NewEnvelope(input)
			if e != nil || !bytes.Equal(mustCleanSeedJSON(env.Dump()), proof.Envelope) {
				return seedRescueEvidenceError()
			}
			at, e := time.Parse(time.RFC3339Nano, proof.CreatedAt)
			zone, ze := time.LoadLocation(proof.Timezone)
			if e != nil || ze != nil || proof.CurrentTime != at.In(zone).Format("2006-01-02 15:04:05") {
				return seedRescueEvidenceError()
			}
			if route.EffectID != fmt.Sprintf("%s:%d", r.RunID, ordinal) || route.AcceptedSHA != proof.PayloadSHA || !bytes.Equal(route.Envelope, proof.Envelope) || route.CreatedAt != proof.CreatedAt || route.CurrentTime != proof.CurrentTime || route.HistoryID != proof.HistoryID || !bytes.Equal(route.HistoryDigest, []byte(proof.HistoryDigest)) || noticeInitialSHA(route) != proof.InitialSHA {
				return seedRescueEvidenceError()
			}
			var result struct {
				ID  string `json:"route_id"`
				SHA string `json:"initial_sha"`
			}
			if cleanupJournalStrict(effect.Result, &result) != nil || result.ID != route.ID || result.SHA != proof.InitialSHA {
				return seedRescueEvidenceError()
			}
			if proof.HistoryID != "" {
				var raw []byte
				if e = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, proof.HistoryID).Scan(&raw); e != nil {
					return e
				}
				var n Notification
				if json.Unmarshal(raw, &n) != nil {
					return seedRescueEvidenceError()
				}
				n.Read = false
				if p.noticeID("seed-rescue-history-value", n) != proof.HistoryDigest {
					return seedRescueEvidenceError()
				}
			}
			return nil
		}
	}
	return seedRescueEvidenceError()
}
