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
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

type scrapeNoticeProof struct {
	Version                                               int
	RouteID, InitialSHA, CreatedAt, CurrentTime, Timezone string
	Payload, Envelope                                     json.RawMessage
}

func (a *App) initScrapeNoticeTx(ctx context.Context, tx *sql.Tx, e *pluginScrapeCleanEffects, x scrapehost.Effect, token string, box pluginScrapeCleanPrivateOutbox, n Notification) (*scrapeNoticeProof, error) {
	var src struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if scrapeJournalStrict(x.Payload, &src) != nil {
		return nil, scrapeJournalCorrupt()
	}
	env, err := notice.NewEnvelope(notice.Object{"title": src.Title, "text": src.Text, "mtype": "站点"})
	if err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339Nano, box.CreatedAt)
	if err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(box.Timezone)
	if err != nil {
		return nil, err
	}
	proof := &scrapeNoticeProof{Version: 1, Payload: append(json.RawMessage{}, x.Payload...), Envelope: mustCleanSeedJSON(env.Dump()), CreatedAt: box.CreatedAt, CurrentTime: at.In(zone).Format("2006-01-02 15:04:05"), Timezone: box.Timezone}
	p := a.pluginStore
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", e.j.instance, e.j.generation, token), Kind: "scrape_clean", Instance: e.j.instance, Generation: e.j.generation, Token: token, EffectID: x.ID, AcceptedSHA: tokenHash(string(x.Payload)), Envelope: proof.Envelope, CreatedAt: box.CreatedAt, CurrentTime: proof.CurrentTime, HistoryID: n.ID, HistoryDigest: scrapeNoticeDigest(e, n)}
	proof.RouteID, proof.InitialSHA = route.ID, noticeInitialSHA(route)
	sealed, err := p.noticeSeal("route", route.ID, route)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed)
	return proof, err
}
func (p *PluginStore) validateScrapeNotice(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	j := &pluginScrapeCleanJournal{p: p, instance: r.Instance, generation: r.Generation}
	s, _, _, err := j.read(ctx, q)
	if err != nil {
		return err
	}
	x, ok := s.Effects[r.EffectID]
	if !ok || x.Kind != "notification" || x.ID != r.EffectID || x.Binding.InstanceID != r.Instance || x.Binding.Generation != r.Generation || x.State != "confirmed" || !x.Result {
		return scrapeJournalCorrupt()
	}
	e := &pluginScrapeCleanEffects{j: j}
	token := e.token(x.ID)
	if token != r.Token {
		return scrapeJournalCorrupt()
	}
	if receipt, err := e.receipt(ctx, q, x); err != nil || !receipt.Result {
		if err != nil {
			return err
		}
		return scrapeJournalCorrupt()
	}
	var bs, hs []byte
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_private_outbox WHERE instance_id=? AND generation=? AND token=?`, r.Instance, r.Generation, token).Scan(&bs); err != nil {
		return err
	}
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, r.Instance, r.Generation, token).Scan(&hs); err != nil {
		return err
	}
	raw, err := p.unseal("scrape_journal", r.Instance, e.aad("outbox", token), bs)
	if err != nil {
		return err
	}
	var box pluginScrapeCleanPrivateOutbox
	if scrapeJournalStrict(raw, &box) != nil || box.Version != 1 || box.State != "history_saved" {
		return scrapeJournalCorrupt()
	}
	raw, err = p.unseal("scrape_journal", r.Instance, e.aad("handoff", token), hs)
	if err != nil {
		return err
	}
	var h pluginScrapeHandoff
	identity, original, err := e.identity(x)
	if err != nil || scrapeJournalStrict(raw, &h) != nil || h.Version != 1 || h.Kind != x.Kind || h.Identity != identity || !bytes.Equal(h.Effect, original) || !bytes.Equal(box.Effect, original) || h.Stage != box.State || h.Notice == nil {
		return scrapeJournalCorrupt()
	}
	proof := h.Notice
	if proof.Version != 1 || proof.RouteID != r.ID || proof.InitialSHA != noticeInitialSHA(r) || !bytes.Equal(proof.Payload, x.Payload) || !bytes.Equal(proof.Envelope, r.Envelope) || proof.CreatedAt != box.CreatedAt || proof.Timezone != box.Timezone || proof.CreatedAt != r.CreatedAt || proof.CurrentTime != r.CurrentTime || r.AcceptedSHA != tokenHash(string(x.Payload)) || r.HistoryID != h.Reference || !hmac.Equal(r.HistoryDigest, h.PublicDigest) {
		return scrapeJournalCorrupt()
	}
	var src struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if scrapeJournalStrict(x.Payload, &src) != nil {
		return scrapeJournalCorrupt()
	}
	env, err := notice.NewEnvelope(notice.Object{"title": src.Title, "text": src.Text, "mtype": "站点"})
	if err != nil || !bytes.Equal(mustCleanSeedJSON(env.Dump()), proof.Envelope) {
		return scrapeJournalCorrupt()
	}
	at, err := time.Parse(time.RFC3339Nano, proof.CreatedAt)
	zone, ze := time.LoadLocation(proof.Timezone)
	if err != nil || ze != nil || proof.CurrentTime != at.In(zone).Format("2006-01-02 15:04:05") {
		return scrapeJournalCorrupt()
	}
	wantID := "scrape_notice_" + hex.EncodeToString(j.mac("notice-history", []byte(token)))
	var nr []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, wantID).Scan(&nr); err != nil {
		return err
	}
	var n Notification
	if scrapeJournalStrict(nr, &n) != nil || n.ID != wantID || r.HistoryID != wantID || !hmac.Equal(scrapeNoticeDigest(e, n), r.HistoryDigest) {
		return scrapeJournalCorrupt()
	}
	return nil
}
