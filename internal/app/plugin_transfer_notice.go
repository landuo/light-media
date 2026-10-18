package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/notice"
	transfer "light-media/internal/plugins/torrenttransfer"
)

const transferNoticeSchema = `CREATE TABLE IF NOT EXISTS plugin_transfer_notices(token TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);`

type transferNotice struct {
	Version                                            int
	Identity                                           transfer.RunIdentity
	Token, Key                                         string
	Sequence                                           int
	Payload, Envelope                                  json.RawMessage
	CreatedAt, CurrentTime, Timezone                   string
	TimezoneOffset                                     int
	HistoryID, HistoryDigest, RouteID, RouteInitialSHA string
}

// Local is an actual process setting, not a replacement for a configured zone.
// Notices also persist the offset used at the source instant: cold validation
// must not reinterpret an old local wall time using a new process TZ.
func transferProcessTimezone() string {
	if zone := os.Getenv("TZ"); zone != "" {
		if _, e := time.LoadLocation(zone); e == nil {
			return zone
		}
	}
	return time.Local.String()
}
func transferScheduleTimezone() (string, error) {
	zone := transferProcessTimezone()
	if zone != "Local" {
		return zone, nil
	}
	// Persist a real IANA name for scheduling; the shared scheduler deliberately
	// rejects mutable "Local". Read only the host's timezone metadata.
	if path, e := filepath.EvalSymlinks("/etc/localtime"); e == nil {
		if n := strings.LastIndex(path, "/zoneinfo/"); n >= 0 {
			zone = path[n+10:]
			if _, e = time.LoadLocation(zone); e == nil {
				return zone, nil
			}
		}
	}
	if raw, e := os.ReadFile("/etc/timezone"); e == nil {
		zone = strings.TrimSpace(string(raw))
		if zone != "Local" {
			if _, e = time.LoadLocation(zone); e == nil {
				return zone, nil
			}
		}
	}
	return "", errors.New("Transfer cannot persist process timezone; configure the host TZ")
}
func transferNoticeEnvelope(raw []byte) (*notice.Envelope, error) {
	var payload map[string]string
	if cleanupJournalStrict(raw, &payload) != nil || len(payload) != 3 || payload["type"] != "SiteMessage" {
		return nil, transfer.ErrReplayDiverged
	}
	return notice.NewEnvelope(notice.Object{"mtype": "站点", "title": payload["title"], "text": payload["text"]})
}
func transferNoticeSequence(key string) (int, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "notice" {
		return 0, transfer.ErrReplayDiverged
	}
	run := strings.Split(parts[1], ":")
	if len(run) != 2 || !pluginStorageDigest.MatchString(run[0]) || !pluginStorageDigest.MatchString(run[1]) {
		return 0, transfer.ErrReplayDiverged
	}
	n, e := strconv.Atoi(parts[2])
	if e != nil || n < 1 || strconv.Itoa(n) != parts[2] {
		return 0, transfer.ErrReplayDiverged
	}
	return n, nil
}
func (a *App) acceptTransferNoticeTx(ctx context.Context, tx *sql.Tx, id transfer.RunIdentity, key string, value any) error {
	seq, e := transferNoticeSequence(key)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	env, e := transferNoticeEnvelope(raw)
	if e != nil {
		return e
	}
	envelope, e := json.Marshal(env.Dump())
	if e != nil {
		return e
	}
	p := a.pluginStore
	at := p.noticeNow()
	zone := transferProcessTimezone()
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return e
	}
	_, offset := at.In(loc).Zone()
	token := p.noticeID("transfer-source", id, key)
	box := transferNotice{Version: 2, Identity: id, Token: token, Key: key, Sequence: seq, Payload: raw, Envelope: envelope, CreatedAt: at.UTC().Format(time.RFC3339Nano), CurrentTime: at.In(loc).Format("2006-01-02 15:04:05"), Timezone: zone, TimezoneOffset: offset}
	if env.Get("save_history") == true {
		title, _ := env.Get("title").(string)
		text, _ := env.Get("text").(string)
		mtype, _ := env.Get("mtype").(string)
		link, _ := env.Get("link").(string)
		settings := a.Settings()
		box.HistoryID = "notice_" + p.noticeID("transfer-history", token)
		n := Notification{ID: box.HistoryID, Title: cleanWithSettings(title, settings), Message: cleanWithSettings(text, settings), MType: mtype, Link: link, CreatedAt: box.CreatedAt}
		box.HistoryDigest = p.noticeID("transfer-history-value", n)
		if e = putCleanupNoticeTx(ctx, tx, n); e != nil {
			return e
		}
	}
	// The source fact and JR local receipt share this caller-owned transaction.
	// The route's accepted SHA excludes only the self-referential route anchor.
	accepted, e := json.Marshal(box)
	if e != nil {
		return e
	}
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", id.Instance, int64(id.Generation), token), Kind: "transfer", Instance: id.Instance, Generation: int64(id.Generation), Token: token, EffectID: key, AcceptedSHA: tokenHash(string(accepted)), Envelope: envelope, CreatedAt: box.CreatedAt, CurrentTime: box.CurrentTime, HistoryID: box.HistoryID, HistoryDigest: []byte(box.HistoryDigest)}
	box.RouteID, box.RouteInitialSHA = route.ID, noticeInitialSHA(route)
	rawBox, e := json.Marshal(box)
	if e != nil {
		return e
	}
	sealed, e := p.seal("run_result", id.Instance, "transfer-notice:"+token, rawBox)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_transfer_notices VALUES(?,?,?,?)`, token, id.Instance, id.Generation, sealed); e != nil {
		return e
	}
	sealed, e = p.noticeSeal("route", route.ID, route)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed)
	return e
}
func (a *App) routeTransferNotice(ctx context.Context, id transfer.RunIdentity, key string) error {
	token := a.pluginStore.noticeID("transfer-source", id, key)
	return a.runNoticeRoute(ctx, a.pluginStore.noticeID("route", id.Instance, int64(id.Generation), token))
}

// Validating a historical producer grants no live Transfer/native permission. Both
// the Root sealed fact and the original authenticated JR local receipt are
// required; a public event, row count or matching string cannot create one.
func (p *PluginStore) validateTransferNotice(ctx context.Context, q pluginRemoveLinkJournalReader, route pluginNoticeRoute) error {
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_transfer_notices WHERE token=? AND instance_id=? AND generation=?`, route.Token, route.Instance, route.Generation).Scan(&sealed); e != nil {
		return e
	}
	raw, e := p.unseal("run_result", route.Instance, "transfer-notice:"+route.Token, sealed)
	if e != nil {
		return e
	}
	var box transferNotice
	if cleanupJournalStrict(raw, &box) != nil || box.Version != 2 || box.Identity.Instance != route.Instance || box.Identity.Generation != uint64(route.Generation) || box.Token != route.Token || box.Token != p.noticeID("transfer-source", box.Identity, box.Key) {
		return transfer.ErrJournalIntegrity
	}
	seq, e := transferNoticeSequence(box.Key)
	if e != nil || seq != box.Sequence {
		return transfer.ErrJournalIntegrity
	}
	env, e := transferNoticeEnvelope(box.Payload)
	if e != nil {
		return e
	}
	envelope, e := json.Marshal(env.Dump())
	if e != nil || !bytes.Equal(envelope, box.Envelope) {
		return transfer.ErrJournalIntegrity
	}
	at, e := time.Parse(time.RFC3339Nano, box.CreatedAt)
	if e != nil || box.Timezone == "" || box.TimezoneOffset < -86400 || box.TimezoneOffset > 86400 || box.CurrentTime != at.In(time.FixedZone(box.Timezone, box.TimezoneOffset)).Format("2006-01-02 15:04:05") {
		return transfer.ErrJournalIntegrity
	}
	anchor := box
	anchor.RouteID, anchor.RouteInitialSHA = "", ""
	accepted, e := json.Marshal(anchor)
	if e != nil {
		return e
	}
	if route.EffectID != box.Key || route.AcceptedSHA != tokenHash(string(accepted)) || !bytes.Equal(route.Envelope, box.Envelope) || route.CreatedAt != box.CreatedAt || route.CurrentTime != box.CurrentTime || route.HistoryID != box.HistoryID || !bytes.Equal(route.HistoryDigest, []byte(box.HistoryDigest)) || box.RouteID != route.ID || box.RouteInitialSHA != noticeInitialSHA(route) {
		return transfer.ErrJournalIntegrity
	}
	verifier, e := transfer.NewAnchoredJournalVerifier(p.store.DB, p.transferJournalKey(route.Instance, int64(box.Identity.Generation)), transferHistoricalRegistryAuthority{p, route.Instance, int64(box.Identity.Generation)})
	if e != nil {
		return e
	}
	var value map[string]string
	if cleanupJournalStrict(box.Payload, &value) != nil {
		return transfer.ErrJournalIntegrity
	}
	verify := func(tx *sql.Tx) error {
		return verifier.ValidateLocalReceipt(ctx, tx, box.Identity, box.Sequence, "source-notice", box.Key, value)
	}
	if tx, ok := q.(*sql.Tx); ok {
		e = verify(tx)
	} else if db, ok := q.(*sql.DB); ok {
		var tx *sql.Tx
		tx, e = db.BeginTx(ctx, nil)
		if e == nil {
			defer tx.Rollback()
			e = verify(tx)
			if e == nil {
				e = tx.Commit()
			}
		}
	} else {
		return errors.New("Transfer notice requires actual SQL reader")
	}
	if e != nil {
		return e
	}
	if box.HistoryID != "" {
		var body []byte
		if e = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, box.HistoryID).Scan(&body); e != nil {
			return e
		}
		var n Notification
		if cleanupJournalStrict(body, &n) != nil {
			return transfer.ErrJournalIntegrity
		}
		n.Read = false
		if p.noticeID("transfer-history-value", n) != box.HistoryDigest {
			return transfer.ErrJournalIntegrity
		}
	}
	return nil
}
