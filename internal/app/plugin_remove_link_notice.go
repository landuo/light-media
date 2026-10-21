package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"light-media/internal/notice"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// Only Commit creates this acceptance. Neither SDK JSON nor a generic envelope
// can authorize a RemoveLink route. The receipt binds all immutable fields.
type removeLinkNoticeAcceptance struct {
	AppDomain                        *string
	Envelope                         json.RawMessage
	CreatedAt, CurrentTime, Timezone string
	TimezoneOffset                   int
	HistoryTitle, HistoryText        string
}
type removeLinkNoticeProof struct {
	Version             int
	RouteID, InitialSHA string
}

func removeLinkNoticeEnvelope(box pluginRemoveLinkPrivateOutbox, x cleanuphost.Effect) (*notice.Envelope, error) {
	if box.Version != 2 || box.Notice == nil || box.Notice.AppDomain == nil || x.Kind != "notification" {
		return nil, cleanuphost.ErrUnsafe
	}
	var payload struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if cleanupJournalStrict(x.Payload, &payload) != nil {
		return nil, cleanuphost.ErrUnsafe
	}
	link, err := sourcePluginDefaultLink(*box.Notice.AppDomain, "RemoveLink", "")
	if err != nil {
		return nil, err
	}
	return notice.NewEnvelope(notice.Object{"mtype": "站点", "title": payload.Title, "text": payload.Text, "link": link})
}

func (e *pluginRemoveLinkEffects) acceptRemoveLinkNotice(x cleanuphost.Effect, original []byte) (pluginRemoveLinkPrivateOutbox, error) {
	settings := e.a.Settings()
	domain := settings.AppDomain
	loc := time.Local
	at := e.j.p.noticeNow()
	n := &removeLinkNoticeAcceptance{AppDomain: &domain, CreatedAt: at.UTC().Format(time.RFC3339Nano), Timezone: loc.String(), CurrentTime: at.In(loc).Format("2006-01-02 15:04:05")}
	_, n.TimezoneOffset = at.In(loc).Zone()
	box := pluginRemoveLinkPrivateOutbox{Version: 2, Effect: original, State: "pending", Notice: n}
	env, err := removeLinkNoticeEnvelope(box, x)
	if err != nil {
		return box, err
	}
	n.Envelope, err = json.Marshal(env.Dump())
	title, _ := env.Get("title").(string)
	body, _ := env.Get("text").(string)
	n.HistoryTitle, n.HistoryText = cleanWithSettings(title, settings), cleanWithSettings(body, settings)
	return box, err
}

// State is a mutable handoff checkpoint, never part of the accepted source.
func removeLinkNoticeAcceptanceSHA(box pluginRemoveLinkPrivateOutbox, x cleanuphost.Effect) (string, error) {
	if box.Version == 1 && box.Notice == nil {
		return "", nil
	}
	env, err := removeLinkNoticeEnvelope(box, x)
	if err != nil {
		return "", err
	}
	n := box.Notice
	raw, err := json.Marshal(env.Dump())
	if err != nil || !bytes.Equal(raw, n.Envelope) {
		return "", cleanuphost.ErrUnsafe
	}
	at, err := time.Parse(time.RFC3339Nano, n.CreatedAt)
	if err != nil || n.Timezone == "" || n.TimezoneOffset < -86400 || n.TimezoneOffset > 86400 || n.CurrentTime != at.In(time.FixedZone(n.Timezone, n.TimezoneOffset)).Format("2006-01-02 15:04:05") {
		return "", cleanuphost.ErrUnsafe
	}
	box.State = ""
	raw, err = json.Marshal(box)
	if err != nil {
		return "", err
	}
	return tokenHash(string(raw)), nil
}

func removeLinkAcceptedHistory(e *pluginRemoveLinkEffects, token string, box pluginRemoveLinkPrivateOutbox, x cleanuphost.Effect) (Notification, error) {
	env, err := removeLinkNoticeEnvelope(box, x)
	if err != nil {
		return Notification{}, err
	}
	link, _ := env.Get("link").(string)
	n := box.Notice
	return Notification{ID: "cleanup_notice_" + hex.EncodeToString(e.j.mac("notice-history", []byte(token))), Title: n.HistoryTitle, Message: n.HistoryText, MType: "站点", Link: link, CreatedAt: n.CreatedAt}, nil
}

func (p *PluginStore) initRemoveLinkNoticeTx(ctx context.Context, tx *sql.Tx, e *pluginRemoveLinkEffects, x cleanuphost.Effect, token string, box pluginRemoveLinkPrivateOutbox, n Notification) (*removeLinkNoticeProof, error) {
	sha, err := removeLinkNoticeAcceptanceSHA(box, x)
	if err != nil || sha == "" {
		return nil, cleanuphost.ErrUnsafe
	}
	r := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", e.j.instance, e.j.generation, token), Kind: "remove_link", Instance: e.j.instance, Generation: e.j.generation, Token: token, EffectID: x.ID, AcceptedSHA: sha, Envelope: box.Notice.Envelope, CreatedAt: box.Notice.CreatedAt, CurrentTime: box.Notice.CurrentTime, HistoryID: n.ID, HistoryDigest: cleanupNoticeDigest(e, n)}
	sealed, err := p.noticeSeal("route", r.ID, r)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, r.ID, r.Instance, r.Generation, r.Token, sealed); err != nil {
		return nil, err
	}
	return &removeLinkNoticeProof{Version: 1, RouteID: r.ID, InitialSHA: noticeInitialSHA(r)}, nil
}

// Authenticate using the caller's reader, including an uncommitted handoff
// transaction. No live owner/config or filesystem authority is consulted.
func (p *PluginStore) validateRemoveLinkNotice(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	row := pluginCleanupOutboxRow{Instance: r.Instance, Generation: r.Generation, Token: r.Token}
	if err := q.QueryRowContext(ctx, `SELECT sealed,created_at FROM plugin_cleanup_private_outbox WHERE instance_id=? AND generation=? AND token=?`, row.Instance, row.Generation, row.Token).Scan(&row.Sealed, &row.Created); err != nil {
		return err
	}
	e, x, box, err := p.committedCleanupOutbox(ctx, q, row)
	if err != nil {
		return err
	}
	if box.Version != 2 || box.State != "history_saved" || x.Kind != "notification" || x.ID != r.EffectID {
		return cleanuphost.ErrUnsafe
	}
	sha, err := removeLinkNoticeAcceptanceSHA(box, x)
	if err != nil {
		return err
	}
	var sealed []byte
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cleanup_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, row.Instance, row.Generation, row.Token).Scan(&sealed); err != nil {
		return err
	}
	raw, err := p.unseal("cleanup_journal", row.Instance, e.aad("handoff", row.Token), sealed)
	if err != nil {
		return err
	}
	var h pluginCleanupHandoff
	identity, original, err := e.identity(x)
	if err != nil || cleanupJournalStrict(raw, &h) != nil || h.Version != 1 || h.Identity != identity || h.Kind != x.Kind || h.Stage != box.State || !bytes.Equal(h.Effect, original) || h.Notice == nil {
		return cleanuphost.ErrUnsafe
	}
	proof := h.Notice
	n, err := removeLinkAcceptedHistory(e, row.Token, box, x)
	if err != nil {
		return err
	}
	digest := cleanupNoticeDigest(e, n)
	if proof.Version != 1 || proof.RouteID != r.ID || proof.InitialSHA != noticeInitialSHA(r) || r.AcceptedSHA != sha || !bytes.Equal(r.Envelope, box.Notice.Envelope) || r.CreatedAt != box.Notice.CreatedAt || r.CurrentTime != box.Notice.CurrentTime || h.Reference != n.ID || r.HistoryID != n.ID || !hmac.Equal(h.PublicDigest, digest) || !hmac.Equal(r.HistoryDigest, digest) {
		return cleanuphost.ErrUnsafe
	}
	var body []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&body); err != nil {
		return err
	}
	var actual Notification
	if cleanupJournalStrict(body, &actual) != nil {
		return cleanuphost.ErrUnsafe
	}
	actual.Read = false
	if actual != n {
		return cleanuphost.ErrUnsafe
	}
	return nil
}
