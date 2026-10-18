package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/notice"
	ab "light-media/internal/plugins/autobackup"
)

const autoBackupNoticeSchema = `CREATE TABLE IF NOT EXISTS plugin_auto_backup_notices(token TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);`

type autoBackupNotice struct {
	Version                                                                            int
	Instance, RunID, InputSHA, Token, CreatedAt, CurrentTime, HistoryID, HistoryDigest string
	Generation, Revision                                                               int64
	Ordinal                                                                            int
	Payload, Envelope                                                                  json.RawMessage
	Timezone                                                                           string
}

func autoBackupEnvelope(raw json.RawMessage) (*notice.Envelope, error) {
	var input notice.Object
	if cleanupJournalStrict(raw, &input) != nil || input == nil {
		return nil, errors.New("invalid backup notice")
	}
	if input["mtype"] == "SiteMessage" {
		input["mtype"] = "站点"
	}
	env, err := notice.NewEnvelope(input)
	if err != nil {
		return nil, &ab.SourceError{Type: "ValidationError", Operation: "Notification"}
	}
	return env, nil
}
func (c *autoBackupCall) notice(ctx context.Context, payload ab.Object) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env, err := autoBackupEnvelope(raw)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(env.Dump())
	if err != nil {
		return err
	}
	if _, err = c.o.a.pluginStore.noticePeriods(ctx); err != nil {
		return err
	}
	if _, err = c.o.a.Store.DB.ExecContext(ctx, autoBackupNoticeSchema); err != nil {
		return err
	}
	value, err := c.effect(ctx, "notice", json.RawMessage(raw), func() (any, error) {
		tx, err := c.o.a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		s, _, err := c.o.a.pluginStore.autoBackupRead(ctx, tx, c.o.instance, c.o.generation)
		if err != nil {
			return nil, err
		}
		r := s.Runs[c.run.ID]
		ordinal := len(r.Effects) - 1
		if ordinal < 0 || r.Effects[ordinal].Kind != "notice" || !bytes.Equal(r.Effects[ordinal].Args, raw) {
			return nil, errors.New("backup notice effect mismatch")
		}
		at := c.now().UTC()
		zone, err := time.LoadLocation(c.o.timezone)
		if err != nil {
			return nil, err
		}
		p := c.o.a.pluginStore
		token := p.noticeID("auto-backup-source", c.o.instance, c.o.generation, c.run.ID, ordinal)
		box := autoBackupNotice{Version: 2, Timezone: c.o.timezone, Instance: c.o.instance, Generation: c.o.generation, RunID: c.run.ID, Revision: c.run.ConfigRevision, InputSHA: deferredInputSHA(c.run.Input), Token: token, Ordinal: ordinal, Payload: raw, Envelope: envelope, CreatedAt: at.Format(time.RFC3339Nano), CurrentTime: at.In(zone).Format("2006-01-02 15:04:05")}
		if env.Get("save_history") == true {
			settings := c.o.a.Settings()
			title, _ := env.Get("title").(string)
			text, _ := env.Get("text").(string)
			mtype, _ := env.Get("mtype").(string)
			link, _ := env.Get("link").(string)
			box.HistoryID = "notice_" + p.noticeID("auto-backup-history", token)
			n := Notification{ID: box.HistoryID, Title: cleanWithSettings(title, settings), Message: cleanWithSettings(text, settings), MType: mtype, Link: link, CreatedAt: box.CreatedAt}
			box.HistoryDigest = p.noticeID("auto-backup-history-value", n)
			if err = putCleanupNoticeTx(ctx, tx, n); err != nil {
				return nil, err
			}
		}
		b, err := json.Marshal(box)
		if err != nil {
			return nil, err
		}
		sealed, err := p.seal("run_result", box.Instance, "auto-backup-notice:"+token, b)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_auto_backup_notices VALUES(?,?,?,?)`, token, box.Instance, box.Generation, sealed); err != nil {
			return nil, err
		}
		route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", box.Instance, box.Generation, token), Kind: "auto_backup", Instance: box.Instance, Generation: box.Generation, Token: token, EffectID: c.run.ID + ":" + token, AcceptedSHA: tokenHash(string(b)), Envelope: box.Envelope, CreatedAt: box.CreatedAt, CurrentTime: box.CurrentTime, HistoryID: box.HistoryID, HistoryDigest: []byte(box.HistoryDigest)}
		sealed, err = p.noticeSeal("route", route.ID, route)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return map[string]string{"route_id": route.ID, "initial_sha": noticeInitialSHA(route)}, nil
	})
	if err != nil {
		return err
	}
	route := value.(map[string]string)["route_id"]
	err = c.o.a.runNoticeRoute(ctx, route)
	if err != nil {
		r, _, e := c.o.a.pluginStore.readNoticeRoute(context.WithoutCancel(ctx), c.o.a.Store.DB, route)
		if e != nil || !r.Complete {
			c.uncertain = true
		}
	}
	return err
}
func (p *PluginStore) validateAutoBackupNotice(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) error {
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_auto_backup_notices WHERE token=? AND instance_id=? AND generation=?`, route.Token, route.Instance, route.Generation).Scan(&sealed); err != nil {
		return err
	}
	raw, err := p.unseal("run_result", route.Instance, "auto-backup-notice:"+route.Token, sealed)
	if err != nil {
		return err
	}
	var box autoBackupNotice
	if cleanupJournalStrict(raw, &box) != nil || box.Version != 2 || box.Instance != route.Instance || box.Generation != route.Generation || box.Token != route.Token || box.Ordinal < 0 || box.Token != p.noticeID("auto-backup-source", box.Instance, box.Generation, box.RunID, box.Ordinal) {
		return errors.New("backup notice identity mismatch")
	}
	env, err := autoBackupEnvelope(box.Payload)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(env.Dump())
	if err != nil || !bytes.Equal(envelope, box.Envelope) {
		return errors.New("backup notice source envelope mismatch")
	}
	at, err := time.Parse(time.RFC3339Nano, box.CreatedAt)
	zone, ze := time.LoadLocation(box.Timezone)
	if err != nil || ze != nil || box.CurrentTime != at.In(zone).Format("2006-01-02 15:04:05") {
		return errors.New("backup notice source time mismatch")
	}
	if route.EffectID != box.RunID+":"+box.Token || route.AcceptedSHA != tokenHash(string(raw)) || !bytes.Equal(route.Envelope, box.Envelope) || route.CreatedAt != box.CreatedAt || route.CurrentTime != box.CurrentTime || route.HistoryID != box.HistoryID || !bytes.Equal(route.HistoryDigest, []byte(box.HistoryDigest)) {
		return errors.New("backup notice route mismatch")
	}
	s, _, err := p.autoBackupRead(ctx, q, box.Instance, box.Generation)
	if err != nil {
		return err
	}
	r, ok := s.Runs[box.RunID]
	if !ok || r.Revision != box.Revision || r.InputSHA != box.InputSHA || box.Ordinal >= len(r.Effects) {
		return errors.New("backup notice original action missing")
	}
	effect := r.Effects[box.Ordinal]
	var proof struct {
		RouteID    string `json:"route_id"`
		InitialSHA string `json:"initial_sha"`
	}
	if effect.Kind != "notice" || effect.Phase != "confirmed" || !bytes.Equal(effect.Args, box.Payload) || cleanupJournalStrict(effect.Result, &proof) != nil || proof.RouteID != route.ID || proof.InitialSHA != noticeInitialSHA(route) {
		return errors.New("backup notice effect receipt mismatch")
	}
	if box.HistoryID != "" {
		var body []byte
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, box.HistoryID).Scan(&body); err != nil {
			return err
		}
		var n Notification
		if json.Unmarshal(body, &n) != nil {
			return errors.New("backup notice history corrupt")
		}
		n.Read = false
		if p.noticeID("auto-backup-history-value", n) != box.HistoryDigest {
			return errors.New("backup notice history mismatch")
		}
	}
	return nil
}
