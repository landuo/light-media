package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// The source chat interface exposes Subscribe.id as a decimal number. Keep
// that number separate from the native ID used by subscriptions and jobs.
// Existing records are backfilled once. New aliases are allocated in the
// same transaction as insertion of the subscription, and are never removed.
const originalSubscriptionAliasSchema = `
CREATE TABLE IF NOT EXISTS original_subscription_aliases (
    number INTEGER PRIMARY KEY AUTOINCREMENT CHECK(number > 0),
    subscription_id TEXT NOT NULL UNIQUE CHECK(subscription_id <> '')
);
INSERT INTO original_subscription_aliases(subscription_id)
SELECT r.id FROM records r
WHERE r.kind = 'subscription' AND NOT EXISTS (
    SELECT 1 FROM original_subscription_aliases a WHERE a.subscription_id = r.id
)
ORDER BY COALESCE(CASE WHEN json_valid(r.payload) THEN json_extract(r.payload, '$.created_at') END, r.updated_at), r.id;
CREATE TRIGGER IF NOT EXISTS original_subscription_alias_on_insert
AFTER INSERT ON records WHEN NEW.kind = 'subscription'
BEGIN
    INSERT INTO original_subscription_aliases(subscription_id)
    SELECT NEW.id WHERE NOT EXISTS (
        SELECT 1 FROM original_subscription_aliases WHERE subscription_id = NEW.id
    );
END;
`

type originalSubscriptionAlias struct {
	Number       int64
	Subscription Subscription
}

// The source lists current subscriptions, including paused ones. Completed
// and cancelled native records are kept for reconciliation but cannot be
// resolved as current chat subscriptions.
const originalCurrentSubscriptionAliases = `
SELECT a.number, r.id, r.payload
FROM original_subscription_aliases a
JOIN records r ON r.kind = 'subscription' AND r.id = a.subscription_id
WHERE json_extract(r.payload, '$.state') IN ('active', 'paused')`

func (s *Store) originalSubscriptionAliases(ctx context.Context) ([]originalSubscriptionAlias, error) {
	rows, err := s.DB.QueryContext(ctx, originalCurrentSubscriptionAliases+` ORDER BY a.number`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aliases := []originalSubscriptionAlias{}
	for rows.Next() {
		alias, err := scanOriginalSubscriptionAlias(rows)
		if err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

func (s *Store) originalSubscriptionByAlias(ctx context.Context, number int64) (Subscription, error) {
	if number <= 0 {
		return Subscription{}, sql.ErrNoRows
	}
	alias, err := scanOriginalSubscriptionAlias(s.DB.QueryRowContext(ctx, originalCurrentSubscriptionAliases+` AND a.number = ?`, number))
	return alias.Subscription, err
}

func scanOriginalSubscriptionAlias(row interface{ Scan(...any) error }) (originalSubscriptionAlias, error) {
	var alias originalSubscriptionAlias
	var id string
	var payload []byte
	if err := row.Scan(&alias.Number, &id, &payload); err != nil {
		return originalSubscriptionAlias{}, err
	}
	if err := json.Unmarshal(payload, &alias.Subscription); err != nil {
		return originalSubscriptionAlias{}, err
	}
	if alias.Subscription.ID != id {
		return originalSubscriptionAlias{}, bad("SUBSCRIPTION_ALIAS_IDENTITY", "订阅编号对应的记录身份不一致")
	}
	return alias, nil
}
