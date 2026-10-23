package app

import (
	"context"
	"database/sql"
	"encoding/json"

	"light-media/internal/catalog"
)

// Freeze only the media identity available at creation. A later plugin cannot
// silently reinterpret the event using a different subscription or a newly
// changed media identity. Public outbox events remain references only.
type subscriptionAddedSnapshot struct {
	Username       json.RawMessage `json:"username,omitempty"`
	SubscriptionID string          `json:"subscription_id"`
	Revision       int             `json:"revision"`
	Media          catalog.Media   `json:"media"`
}

func (a *App) recordSubscriptionAddedTx(ctx context.Context, tx *sql.Tx, subscription Subscription) error {
	event := DomainEvent{EventID: domainEventID("subscription.added", subscription.ID), Type: "subscription.added", MediaID: mediaKey(subscription.Media), SubscriptionID: subscription.ID, Purpose: "subscription", OccurredAt: subscription.CreatedAt}
	if _, err := validateDomainEvent(event); err != nil {
		return err
	}
	raw, err := json.Marshal(subscriptionAddedSnapshot{SubscriptionID: subscription.ID, Revision: subscription.Revision, Media: subscription.Media})
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("subscription_added", "system", event.EventID, raw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('subscription_added',?,?,?)`, event.EventID, sealed, now()); err != nil {
		return err
	}
	return recordDomainEventTx(ctx, tx, event)
}
func (a *App) subscriptionAddedSnapshot(ctx context.Context, event DomainEvent) (subscriptionAddedSnapshot, error) {
	return a.pluginStore.subscriptionAddedSnapshotFrom(ctx, a.Store.DB, event)
}

func (p *PluginStore) subscriptionAddedSnapshotFrom(ctx context.Context, q PluginProofReader, event DomainEvent) (subscriptionAddedSnapshot, error) {
	var snapshot subscriptionAddedSnapshot
	input := event
	input.Sequence = 0 // Persisted pages carry the host-assigned outbox cursor.
	valid, err := validateDomainEvent(input)
	if err != nil || event.Sequence < 0 || valid.Type != "subscription.added" {
		return snapshot, bad("INVALID_SUBSCRIPTION_EVENT", "订阅事件引用无法核对")
	}
	var sealed []byte
	if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription_added' AND id=?`, event.EventID).Scan(&sealed); err != nil {
		return snapshot, err
	}
	raw, err := p.unseal("subscription_added", "system", event.EventID, sealed)
	if err != nil {
		return snapshot, err
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.SubscriptionID != event.SubscriptionID || mediaKey(snapshot.Media) != event.MediaID || snapshot.Revision < 1 {
		return snapshot, bad("INVALID_SUBSCRIPTION_EVENT", "订阅事件与创建身份不一致")
	}
	return snapshot, nil
}
