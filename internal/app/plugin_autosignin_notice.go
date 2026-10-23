package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/notice"
	durable "light-media/internal/plugins/autosignin/durable"
)

type autoSignInSourceContextKey struct{}

func autoSignInNoticeEnvelope(payload json.RawMessage, domain string) (*notice.Envelope, error) {
	var fields notice.Object
	if cleanupJournalStrict(payload, &fields) != nil || fields == nil {
		return nil, durable.ErrConflict
	}
	// The source's enum is represented symbolically by the Go plugin; the host
	// supplies the installed enum's value, never a caller-selected default link.
	if fields["mtype"] == "NotificationType.SiteMessage" {
		fields["mtype"] = "站点"
	}
	link, err := sourcePluginDefaultLink(domain, "AutoSignIn", "")
	if err != nil {
		return nil, err
	}
	fields["link"] = link
	return notice.NewEnvelope(fields)
}
func (s *pluginAutoSignInSourcePort) acceptNoticeTx(ctx context.Context, tx *sql.Tx, req durable.SourceRequest) (box autoSignInSourceAcceptance, err error) {
	a := s.b.owner.a
	p := a.pluginStore
	at := p.noticeNow()
	domain := a.Settings().AppDomain
	env, err := autoSignInNoticeEnvelope(req.Payload, domain)
	if err != nil {
		var validation *notice.ValidationError
		if errors.As(err, &validation) {
			return s.acceptNoticeConstructionExceptionTx(ctx, tx, req, domain, at)
		}
		return box, err
	}
	raw, err := json.Marshal(env.Dump())
	if err != nil {
		return box, err
	}
	_, offset := at.Local().Zone()
	box = autoSignInSourceAcceptance{Version: 1, Request: req, Token: p.autoSignInSourceToken(req.Identity), Domain: domain, Envelope: raw, CreatedAt: at.UTC().Format(time.RFC3339Nano), CurrentTime: at.Local().Format("2006-01-02 15:04:05"), Timezone: time.Local.String(), TimezoneOffset: offset}
	if env.Get("save_history") == true {
		box.HistoryID = "notice_" + p.noticeID("auto-signin-history", box.Token)
	}
	id := req.Identity
	route := pluginNoticeRoute{Version: 1, ID: p.noticeID("route", id.Scope.Instance, int64(id.Scope.Generation), box.Token), Kind: "auto_signin", Instance: id.Scope.Instance, Generation: int64(id.Scope.Generation), Token: box.Token, EffectID: box.Token, Envelope: raw, CreatedAt: box.CreatedAt, CurrentTime: box.CurrentTime, HistoryID: box.HistoryID, HistoryDigest: []byte(box.HistoryDigest)}
	route.AcceptedSHA = tokenHash(string(mustCleanSeedJSON(box)))
	box.RouteID, box.RouteInitialSHA = route.ID, noticeInitialSHA(route)
	sealed, err := p.autoSignInSourceSeal("auto_signin_acceptance", box.Token, box)
	if err != nil {
		return box, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_source_acceptances VALUES(?,?,?,?)`, box.Token, id.Scope.Instance, id.Scope.Generation, sealed); err != nil {
		return box, err
	}
	sealed, err = p.noticeSeal("route", route.ID, route)
	if err != nil {
		return box, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, route.ID, route.Instance, route.Generation, route.Token, sealed)
	return box, err
}
func (p *PluginStore) autoSignInRouteAcceptance(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) (box autoSignInSourceAcceptance, err error) {
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_source_acceptances WHERE token=? AND instance_id=? AND generation=?`, r.Token, r.Instance, r.Generation).Scan(&sealed)
	if err != nil {
		return
	}
	err = p.noticeUnseal("auto_signin_acceptance", r.Token, sealed, &box)
	return
}
func (p *PluginStore) validateAutoSignInNotice(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	box, err := p.autoSignInRouteAcceptance(ctx, q, r)
	if err != nil {
		return err
	}
	id := box.Request.Identity
	if box.Version != 1 || id.Version != 1 || id.Kind != "source.notify" || id.Scope.Instance != r.Instance || id.Scope.Generation != uint64(r.Generation) || box.Token != r.Token || box.Token != p.autoSignInSourceToken(id) || box.RouteID != r.ID || box.RouteInitialSHA != noticeInitialSHA(r) {
		return durable.ErrStore
	}
	env, err := autoSignInNoticeEnvelope(box.Request.Payload, box.Domain)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env.Dump())
	if err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, box.CreatedAt)
	if err != nil || box.Timezone == "" || box.TimezoneOffset < -86400 || box.TimezoneOffset > 86400 || at.In(time.FixedZone(box.Timezone, box.TimezoneOffset)).Format("2006-01-02 15:04:05") != box.CurrentTime {
		return durable.ErrStore
	}
	anchor := box
	anchor.RouteID, anchor.RouteInitialSHA = "", ""
	if !bytes.Equal(raw, box.Envelope) || !bytes.Equal(r.Envelope, box.Envelope) || r.AcceptedSHA != tokenHash(string(mustCleanSeedJSON(anchor))) || r.EffectID != box.Token || r.CreatedAt != box.CreatedAt || r.CurrentTime != box.CurrentTime || r.HistoryID != box.HistoryID || !bytes.Equal(r.HistoryDigest, []byte(box.HistoryDigest)) {
		return durable.ErrStore
	}
	return durable.ValidateSourceRequest(box.Request)
}

// Only the currently owned synchronous invocation can advance these routes.
// Reading historical facts does not install execution authority in a context.
func (p *PluginStore) requireAutoSignInNoticeExecutionTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute) error {
	if r.Kind != "auto_signin" {
		return nil
	}
	s, ok := ctx.Value(autoSignInSourceContextKey{}).(*pluginAutoSignInSourcePort)
	if !ok || s == nil || s.b == nil || s.b.owner.a.pluginStore != p {
		return durable.ErrStale
	}
	loaded, err := s.b.owner.readTx(ctx, tx)
	if err != nil {
		return err
	}
	if err = s.b.guardTx(ctx, tx, s.b.owner.binding, loaded.State); err != nil {
		return err
	}
	box, err := p.autoSignInRouteAcceptance(ctx, tx, r)
	if err != nil {
		return err
	}
	return s.validateTx(ctx, tx, loaded.State, box.Request.Identity, true)
}
func (p *PluginStore) finishAutoSignInNoticeTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, cursor int, sourceErr error, failure *removeLinkSourceFailure) error {
	if r.Kind != "auto_signin" {
		return nil
	}
	class, known := knownRemoveLinkSourceClass(sourceErr)
	if !known || !r.Complete || r.Count != cursor || r.SourceError != class {
		return durable.ErrUncertain
	}
	if failure != nil && (failure.Ordinal != cursor || !validRemoveLinkSourceFailure(*failure)) {
		return durable.ErrStore
	}
	box, err := p.autoSignInRouteAcceptance(ctx, tx, r)
	if err != nil {
		return err
	}
	result := durable.SourceResult{Identity: box.Request.Identity, Outcome: durable.SourceNormal, ProofID: box.Token}
	if sourceErr != nil {
		result.Outcome = durable.SourceException
		result.ErrorClass = class
		result.Error = class
	}
	proof := autoSignInSourceProof{Failure: failure, Result: result, AcceptanceSHA: tokenHash(string(mustCleanSeedJSON(box))), RouteInitialSHA: noticeInitialSHA(r), Count: cursor, LastSHA: r.LastSHA}
	return p.insertAutoSignInProofTx(ctx, tx, box, proof)
}
func (p *PluginStore) validateAutoSignInSourceResult(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	if r.Kind != "auto_signin" {
		return nil
	}
	box, err := p.autoSignInRouteAcceptance(ctx, q, r)
	if err != nil {
		return err
	}
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_source_results WHERE token=?`, r.Token).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) && !r.Complete {
		return nil
	}
	if err != nil {
		return err
	}
	var proof autoSignInSourceProof
	if err = p.noticeUnseal("auto_signin_result", r.Token, sealed, &proof); err != nil {
		return err
	}
	if !r.Complete || proof.Result.Identity != box.Request.Identity || proof.Result.ProofID != box.Token || proof.AcceptanceSHA != tokenHash(string(mustCleanSeedJSON(box))) || proof.RouteInitialSHA != noticeInitialSHA(r) || proof.Count != r.Count || proof.LastSHA != r.LastSHA || proof.Result.ErrorClass != r.SourceError {
		return durable.ErrStore
	}
	if proof.Failure != nil && (proof.Result.Outcome != durable.SourceException || proof.Failure.Ordinal != r.Count || !validRemoveLinkSourceFailure(*proof.Failure)) {
		return durable.ErrStore
	}
	return durable.ValidateSourceResult(box.Request, proof.Result)
}

// Called exactly at Router.SaveHistory, after source rendering and its empty
// message checks. Acceptance deliberately performs no history side effect.
func (p *PluginStore) autoSignInNoticeHistoryTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, v notice.Object) (any, error) {
	if r.Kind != "auto_signin" || r.HistoryID == "" {
		return nil, durable.ErrStore
	}
	box, err := p.autoSignInRouteAcceptance(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	if box.HistoryID != r.HistoryID {
		return nil, durable.ErrStore
	}
	title, _ := v["title"].(string)
	text, _ := v["text"].(string)
	mtype, _ := v["mtype"].(string)
	link, _ := v["link"].(string)
	n := Notification{ID: box.HistoryID, Title: title, Message: text, MType: mtype, Link: link, CreatedAt: box.CreatedAt}
	if err = putCleanupNoticeTx(ctx, tx, n); err != nil {
		return nil, err
	}
	return map[string]any{"history_id": box.HistoryID, "history_digest": p.noticeID("auto-signin-history-value", n)}, nil
}

// The authenticated history step is the authority for source history return;
// empty-message/early-exception routes legitimately have no history step.
func (p *PluginStore) validateAutoSignInNoticeSteps(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute, steps []pluginNoticeStep) error {
	if r.Kind != "auto_signin" {
		return nil
	}
	histories := 0
	for _, step := range steps {
		if step.Kind != "history" {
			continue
		}
		histories++
		var proof struct {
			HistoryID     string `json:"history_id"`
			HistoryDigest string `json:"history_digest"`
		}
		if cleanupJournalStrict(step.Output, &proof) != nil || proof.HistoryID != r.HistoryID || proof.HistoryID == "" || !pluginStorageDigest.MatchString(proof.HistoryDigest) {
			return durable.ErrStore
		}
		var raw []byte
		if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, proof.HistoryID).Scan(&raw); err != nil {
			return err
		}
		var n Notification
		if cleanupJournalStrict(raw, &n) != nil {
			return durable.ErrStore
		}
		n.Read = false
		if n.ID != proof.HistoryID || n.CreatedAt != r.CreatedAt || proof.HistoryDigest != p.noticeID("auto-signin-history-value", n) {
			return durable.ErrStore
		}

	}
	if histories > 1 {
		return durable.ErrStore
	}
	return nil
}

func (s *pluginAutoSignInSourcePort) acceptNoticeConstructionExceptionTx(ctx context.Context, tx *sql.Tx, req durable.SourceRequest, domain string, at time.Time) (box autoSignInSourceAcceptance, err error) {
	p := s.b.owner.a.pluginStore
	box = autoSignInSourceAcceptance{Version: 1, Request: req, Token: p.autoSignInSourceToken(req.Identity), Domain: domain, CreatedAt: at.UTC().Format(time.RFC3339Nano), ConstructionErrorClass: "ValidationError"}
	sealed, err := p.autoSignInSourceSeal("auto_signin_acceptance", box.Token, box)
	if err != nil {
		return box, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_source_acceptances VALUES(?,?,?,?)`, box.Token, req.Identity.Scope.Instance, req.Identity.Scope.Generation, sealed); err != nil {
		return box, err
	}
	proof := autoSignInSourceProof{Result: durable.SourceResult{Identity: req.Identity, Outcome: durable.SourceException, ErrorClass: "ValidationError", Error: "ValidationError", ProofID: box.Token}, AcceptanceSHA: tokenHash(string(mustCleanSeedJSON(box)))}
	return box, p.insertAutoSignInProofTx(ctx, tx, box, proof)
}
func (p *PluginStore) validateAutoSignInConstructionProof(box autoSignInSourceAcceptance, proof autoSignInSourceProof) error {
	if box.ConstructionErrorClass != "ValidationError" || box.RouteID != "" || box.RouteInitialSHA != "" || box.HistoryID != "" || len(box.Envelope) != 0 && string(box.Envelope) != "null" || proof.Result.Outcome != durable.SourceException || proof.Result.ErrorClass != "ValidationError" || proof.Count != 0 || proof.LastSHA != "" || proof.RouteInitialSHA != "" || proof.Failure != nil {
		return durable.ErrStore
	}
	_, err := autoSignInNoticeEnvelope(box.Request.Payload, box.Domain)
	var validation *notice.ValidationError
	if !errors.As(err, &validation) {
		return durable.ErrStore
	}
	return nil
}
