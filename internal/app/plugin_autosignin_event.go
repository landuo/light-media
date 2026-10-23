package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	durable "light-media/internal/plugins/autosignin/durable"
	"regexp"
	"time"
)

// This is EventType.PluginAction priority-queue acceptance, NOT handler
// completion. It is intentionally not published as BrushFlow plugin.triggered
// or the unrelated App site_refresh statistics job. A future consumer must
// claim these immutable queue facts through its own durable dispatch receipts.
type autoSignInSourceEvent struct {
	EventID   string
	Version   int
	Identity  durable.SourceIdentity
	EventType string
	Priority  int
	Payload   json.RawMessage
	CreatedAt string
}

func (s *pluginAutoSignInSourcePort) acceptEventTx(ctx context.Context, tx *sql.Tx, req durable.SourceRequest) error {
	data, err := autoSignInEventPayload(req.Payload)
	if err != nil {
		return err
	}
	p := s.b.owner.a.pluginStore
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	eventID := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	box := autoSignInSourceAcceptance{EventID: eventID, Version: 1, Request: req, Token: p.autoSignInSourceToken(req.Identity), CreatedAt: p.noticeNow().UTC().Format(time.RFC3339Nano)}
	sealed, err := p.autoSignInSourceSeal("auto_signin_acceptance", box.Token, box)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_source_acceptances VALUES(?,?,?,?)`, box.Token, req.Identity.Scope.Instance, req.Identity.Scope.Generation, sealed); err != nil {
		return err
	}
	event := autoSignInSourceEvent{EventID: eventID, Version: 1, Identity: req.Identity, EventType: "EventType.PluginAction", Priority: 10, Payload: data, CreatedAt: box.CreatedAt}
	sealed, err = p.autoSignInSourceSeal("auto_signin_event", box.Token, event)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_signin_source_events(token,sealed) VALUES(?,?)`, box.Token, sealed); err != nil {
		return err
	}
	return p.insertAutoSignInProofTx(ctx, tx, box, autoSignInSourceProof{Result: durable.SourceResult{Identity: req.Identity, Outcome: durable.SourceNormal, ProofID: box.Token}, AcceptanceSHA: tokenHash(string(mustCleanSeedJSON(box)))})
}

func (p *PluginStore) validateAutoSignInEventTx(ctx context.Context, q pluginRemoveLinkJournalReader, box autoSignInSourceAcceptance) error {
	data, err := autoSignInEventPayload(box.Request.Payload)
	if err != nil {
		return err
	}
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_signin_source_events WHERE token=?`, box.Token).Scan(&sealed); err != nil {
		return err
	}
	var event autoSignInSourceEvent
	if err := p.noticeUnseal("auto_signin_event", box.Token, sealed, &event); err != nil {
		return err
	}
	if !autoSignInEventUUID.MatchString(event.EventID) || event.EventID != box.EventID || event.Version != 1 || event.Identity != box.Request.Identity || event.EventType != "EventType.PluginAction" || event.Priority != 10 || !bytes.Equal(event.Payload, data) || event.CreatedAt != box.CreatedAt {
		return durable.ErrStore
	}
	return nil
}

func autoSignInEventPayload(raw json.RawMessage) (json.RawMessage, error) {
	var wrapper struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if cleanupJournalStrict(raw, &wrapper) != nil || wrapper.Event != "EventType.PluginAction" {
		return nil, durable.ErrConflict
	}
	var payload map[string]json.RawMessage
	if cleanupJournalStrict(wrapper.Data, &payload) != nil {
		return nil, durable.ErrConflict
	}
	var action string
	if json.Unmarshal(payload["action"], &action) != nil {
		return nil, durable.ErrConflict
	}
	switch action {
	case "cloudflare_speedtest":
		if len(payload) != 1 {
			return nil, durable.ErrConflict
		}
	case "site_refresh":
		if len(payload) != 2 || payload["site_id"] == nil {
			return nil, durable.ErrConflict
		}
		var id any
		if cleanupJournalStrict(payload["site_id"], &id) != nil {
			return nil, durable.ErrConflict
		}
		switch id.(type) {
		case string, json.Number, float64:
		default:
			return nil, durable.ErrConflict
		}
	default:
		return nil, durable.ErrConflict
	}
	return wrapper.Data, nil
}

var autoSignInEventUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
