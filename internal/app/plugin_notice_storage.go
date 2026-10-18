package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

const pluginNoticeSchema = `
CREATE TABLE IF NOT EXISTS plugin_notice_routes(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,source_token TEXT NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_notice_route_steps(route_id TEXT NOT NULL,ordinal INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(route_id,ordinal));
CREATE TABLE IF NOT EXISTS plugin_notice_publications(id TEXT PRIMARY KEY,route_id TEXT NOT NULL,ordinal INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_message_queue(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,route_id TEXT NOT NULL,ordinal INTEGER NOT NULL,state TEXT NOT NULL,sealed BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS plugin_message_queue_pending ON plugin_message_queue(state,seq);
CREATE TABLE IF NOT EXISTS plugin_notice_preferences(kind TEXT NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(kind,token));`

const pluginNoticeStepLimit = 4096
const pluginNoticePendingLimit = 1000

type pluginNoticeGate struct {
	gate chan struct{}
	refs int
}
type pluginNoticeRuntime struct {
	windowMu    sync.Mutex
	windowReady bool
	window      []notice.Period

	mu                       sync.Mutex
	gates                    map[string]*pluginNoticeGate
	drain                    sync.Mutex
	routeAfter, routeThrough int64
	queueAfter               int64
	now                      func() time.Time
	// A native, owned seam for actual module hosts and isolated protocol tests;
	// no SDK/event/config value can install a callback or provide authority.
	send func(context.Context, pluginNoticeQueueEntry) (int, error)
	// Native fault injection runs after an actual commit; SDK data cannot set it.
	afterCommit func(string) error
}

func (p *PluginStore) commitNoticeTx(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	if p.notices.afterCommit != nil {
		return p.notices.afterCommit(operation)
	}
	return nil
}

func (p *PluginStore) noticeLock(ctx context.Context, id string) (func(), error) {
	p.notices.mu.Lock()
	if p.notices.gates == nil {
		p.notices.gates = map[string]*pluginNoticeGate{}
	}
	g := p.notices.gates[id]
	if g == nil {
		g = &pluginNoticeGate{gate: make(chan struct{}, 1)}
		p.notices.gates[id] = g
	}
	g.refs++
	p.notices.mu.Unlock()
	drop := func() {
		p.notices.mu.Lock()
		g.refs--
		if g.refs == 0 {
			delete(p.notices.gates, id)
		}
		p.notices.mu.Unlock()
	}
	select {
	case g.gate <- struct{}{}:
		return func() { <-g.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
func (p *PluginStore) noticeNow() time.Time {
	if p.notices.now != nil {
		return p.notices.now()
	}
	return time.Now()
}
func (p *PluginStore) noticeID(kind string, v ...any) string {
	b := mustCleanSeedJSON(v)
	m := hmac.New(sha256.New, p.store.key)
	m.Write([]byte("plugin-notice/v1/" + kind + "\x00"))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}

type pluginNoticeRoute struct {
	Version                                                                  int
	ID, Instance, Token, Kind, EffectID, AcceptedSHA, CreatedAt, CurrentTime string
	Generation                                                               int64
	Envelope                                                                 json.RawMessage
	HistoryID                                                                string
	HistoryDigest                                                            []byte
	Count                                                                    int
	LastSHA                                                                  string
	Complete                                                                 bool
	SourceError                                                              string
}
type pluginNoticeStep struct {
	Version                              int
	RouteID, Kind, InputSHA, PreviousSHA string
	Ordinal                              int
	Output                               json.RawMessage
}
type pluginNoticePublication struct {
	Version     int
	ID, RouteID string
	Ordinal     int
	Data        json.RawMessage
}
type pluginNoticeQueueEntry struct {
	Version                          int
	ID, RouteID, State               string
	Ordinal                          int
	Direct                           bool
	Call                             notice.Call
	CreatedAt, AttemptAt, FinishedAt string
	Modules                          int
	ErrorCode                        string
}

func noticeInitialSHA(r pluginNoticeRoute) string {
	r.Count = 0
	r.LastSHA = ""
	r.Complete = false
	r.SourceError = ""
	return tokenHash(string(mustCleanSeedJSON(r)))
}
func (p *PluginStore) noticeSeal(kind, id string, v any) ([]byte, error) {
	b, err := cleanupJournalJSON(v)
	if err != nil || len(b) > pluginCleanInvalidSeedJournalLimit {
		return nil, seedhost.ErrUnsafe
	}
	return p.seal("notice_journal", id, kind, b)
}
func (p *PluginStore) noticeUnseal(kind, id string, b []byte, v any) error {
	raw, err := p.unseal("notice_journal", id, kind, b)
	if err != nil {
		return err
	}
	if cleanupJournalStrict(raw, v) != nil {
		return seedhost.ErrUnsafe
	}
	return nil
}
func (p *PluginStore) initCleanNoticeRouteTx(ctx context.Context, tx *sql.Tx, j *pluginCleanInvalidSeedJournal, box pluginCleanInvalidSeedPrivateNotice, accepted []byte) (string, string, error) {
	token := j.token("outbox", box.Notice.ID, -1)
	r := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", j.instance, j.generation, token), Kind: "clean_seed", Instance: j.instance, Generation: j.generation, Token: token, EffectID: box.Notice.ID, AcceptedSHA: tokenHash(string(accepted)), Envelope: box.Envelope, CreatedAt: box.CreatedAt, CurrentTime: box.CurrentTime, HistoryID: box.HistoryID, HistoryDigest: box.HistoryDigest}
	sealed, err := p.noticeSeal("route", r.ID, r)
	if err != nil {
		return "", "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, r.ID, r.Instance, r.Generation, r.Token, sealed); err != nil {
		return "", "", err
	}
	return r.ID, noticeInitialSHA(r), nil
}
func (p *PluginStore) readNoticeRoute(ctx context.Context, q pluginRemoveLinkJournalReader, id string) (pluginNoticeRoute, []pluginNoticeStep, error) {
	var r pluginNoticeRoute
	if !pluginStorageDigest.MatchString(id) {
		return r, nil, seedhost.ErrUnsafe
	}
	var instance, token string
	var generation int64
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT instance_id,generation,source_token,sealed FROM plugin_notice_routes WHERE id=?`, id).Scan(&instance, &generation, &token, &sealed); err != nil {
		return r, nil, err
	}
	if err := p.noticeUnseal("route", id, sealed, &r); err != nil {
		return r, nil, err
	}
	if r.Version != 1 || r.ID != id || r.Instance != instance || r.Generation != generation || r.Token != token || (r.Kind != "clean_seed" && r.Kind != "auto_backup") || r.Count < 0 || r.Count > pluginNoticeStepLimit || !pluginStorageID.MatchString(instance) || generation < 1 || id != p.noticeID("route", instance, generation, token) {
		return r, nil, seedhost.ErrUnsafe
	}
	if r.Kind == "auto_backup" {
		if err := p.validateAutoBackupNotice(ctx, q, r); err != nil {
			return r, nil, err
		}
	} else {
		j := &pluginCleanInvalidSeedJournal{p: p, instance: instance, generation: generation}
		box, err := readCleanSeedPrivateNotice(ctx, q, j, token)
		if err != nil {
			return r, nil, err
		}
		raw := mustCleanSeedJSON(box)
		if r.EffectID != box.Notice.ID || r.AcceptedSHA != tokenHash(string(raw)) || !bytes.Equal(r.Envelope, box.Envelope) || r.HistoryID != box.HistoryID || !hmac.Equal(r.HistoryDigest, box.HistoryDigest) || r.CreatedAt != box.CreatedAt || r.CurrentTime != box.CurrentTime {
			return r, nil, seedhost.ErrUnsafe
		}
		receipt, err := cleanSeedReadReceipt(ctx, q, j, box.Notice.ID)
		var anchor pluginCleanInvalidSeedNoticeReceipt
		if err != nil || cleanupJournalStrict(receipt.Payload, &anchor) != nil || anchor.RouteID != id || anchor.RouteInitialSHA != noticeInitialSHA(r) {
			return r, nil, seedhost.ErrUnsafe
		}

	}
	rows, err := q.QueryContext(ctx, `SELECT ordinal,sealed FROM plugin_notice_route_steps WHERE route_id=? ORDER BY ordinal`, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	steps := []pluginNoticeStep{}
	previous := ""
	for rows.Next() {
		var ordinal int
		var b []byte
		if err = rows.Scan(&ordinal, &b); err != nil {
			return r, nil, err
		}
		var s pluginNoticeStep
		if err = p.noticeUnseal(fmt.Sprintf("step/%d", ordinal), id, b, &s); err != nil {
			return r, nil, err
		}
		if s.Version != 1 || s.RouteID != id || s.Ordinal != len(steps) || s.Ordinal != ordinal || s.PreviousSHA != previous || !pluginStorageDigest.MatchString(s.InputSHA) || len(s.Output) == 0 || len(steps) >= r.Count {
			return r, nil, seedhost.ErrUnsafe
		}
		previous = tokenHash(string(mustCleanSeedJSON(s)))
		steps = append(steps, s)
	}
	if err = rows.Err(); err != nil {
		return r, nil, err
	}
	if len(steps) != r.Count || previous != r.LastSHA || r.SourceError != "" && !r.Complete {
		return r, nil, seedhost.ErrUnsafe
	}
	return r, steps, nil
}
func (p *PluginStore) saveNoticeRouteTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute) error {
	sealed, err := p.noticeSeal("route", r.ID, r)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_notice_routes SET sealed=? WHERE id=? AND instance_id=? AND generation=? AND source_token=?`, sealed, r.ID, r.Instance, r.Generation, r.Token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return seedhost.ErrUnsafe
	}
	return nil
}

// Native settings readers preserve null versus an existing empty user mapping.
// These methods are host-only; importing real settings still requires its own
// authenticated source/reference migration receipt and management UI.
func (p *PluginStore) putNoticePreferenceTx(ctx context.Context, tx *sql.Tx, kind, key string, value any) error {
	if kind != "switch" && kind != "user" && kind != "window" && kind != "superuser" {
		return seedhost.ErrUnsafe
	}
	token := p.noticeID("preference", kind, key)
	raw := struct {
		Kind, Key string
		Value     any
	}{kind, key, value}
	b, err := p.noticeSeal("preference", token, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_preferences VALUES(?,?,?) ON CONFLICT(kind,token) DO UPDATE SET sealed=excluded.sealed`, kind, token, b)
	return err
}
func (p *PluginStore) noticePreference(ctx context.Context, q pluginRemoveLinkJournalReader, kind, key string) (any, error) {
	token := p.noticeID("preference", kind, key)
	var b []byte
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_notice_preferences WHERE kind=? AND token=?`, kind, token).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		Kind, Key string
		Value     any
	}
	if err = p.noticeUnseal("preference", token, b, &out); err != nil {
		return nil, err
	}
	if out.Kind != kind || out.Key != key {
		return nil, seedhost.ErrUnsafe
	}
	return out.Value, nil
}

func (p *PluginStore) tryNoticeLock(id string) (func(), bool) {
	p.notices.mu.Lock()
	if p.notices.gates == nil {
		p.notices.gates = map[string]*pluginNoticeGate{}
	}
	g := p.notices.gates[id]
	if g == nil {
		g = &pluginNoticeGate{gate: make(chan struct{}, 1)}
		p.notices.gates[id] = g
	}
	g.refs++
	p.notices.mu.Unlock()
	drop := func() {
		p.notices.mu.Lock()
		g.refs--
		if g.refs == 0 {
			delete(p.notices.gates, id)
		}
		p.notices.mu.Unlock()
	}
	select {
	case g.gate <- struct{}{}:
		return func() { <-g.gate; drop() }, true
	default:
		drop()
		return nil, false
	}
}

func (p *PluginStore) noticePeriods(ctx context.Context) ([]notice.Period, error) {
	p.notices.windowMu.Lock()
	defer p.notices.windowMu.Unlock()
	if p.notices.windowReady {
		return p.notices.window, nil
	}
	raw, err := p.noticePreference(ctx, p.store.DB, "window", "")
	if err != nil {
		return nil, err
	}
	periods, err := notice.ParseSchedule(raw)
	if err != nil {
		return nil, err
	}
	p.notices.window, p.notices.windowReady = periods, true
	return periods, nil
}
func (p *PluginStore) reloadNoticePeriods(ctx context.Context) error {
	p.notices.windowMu.Lock()
	defer p.notices.windowMu.Unlock()
	raw, err := p.noticePreference(ctx, p.store.DB, "window", "")
	if err != nil {
		return err
	}
	periods, err := notice.ParseSchedule(raw)
	if err != nil {
		return err
	}
	p.notices.window, p.notices.windowReady = periods, true
	return nil
}
func (p *PluginStore) currentNoticePeriods() ([]notice.Period, error) {
	p.notices.windowMu.Lock()
	defer p.notices.windowMu.Unlock()
	if !p.notices.windowReady {
		return nil, notice.ErrDependency
	}
	return p.notices.window, nil
}
