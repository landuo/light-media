package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"light-media/internal/media/words"
)

// This value is kept outside Subscription and every ordinary request/intent
// snapshot. Only the authenticated subscription editor receives the raw text.
func subscriptionWordsInput(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, nil // Old clients omit the field when editing other settings.
	}
	// encoding/json replaces invalid UTF-8 while decoding a string. Validate
	// the original token first so an invalid edit cannot silently change text.
	if !utf8.Valid(raw) {
		return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词不是有效文本")
	}
	text := ""
	if string(raw) != "null" && json.Unmarshal(raw, &text) != nil {
		return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词须为文本或空值")
	}
	if !utf8.ValidString(text) {
		return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词不是有效文本")
	}
	if _, err := words.New(strings.Split(text, "\n")); err != nil {
		return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词超过允许范围")
	}
	encoded, _ := json.Marshal(text)
	if len(encoded) > pluginDataValueLimit {
		return nil, bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词超过允许范围")
	}
	return &text, nil
}

func (a *App) saveSubscriptionWordsTx(ctx context.Context, tx *sql.Tx, key string, text *string) error {
	if text == nil {
		return nil
	}
	if *text == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM subscription_words WHERE subscription_id=?`, key)
		return err
	}
	raw, _ := json.Marshal(*text)
	sealed, err := a.pluginStore.seal("subscription_words", "system", key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subscription_words(subscription_id,sealed,updated_at) VALUES(?,?,?) ON CONFLICT(subscription_id) DO UPDATE SET sealed=excluded.sealed,updated_at=excluded.updated_at`, key, sealed, now())
	return err
}

func (a *App) subscriptionWords(ctx context.Context, key string) (string, error) {
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT sealed FROM subscription_words WHERE subscription_id=?`, key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	raw, err := a.pluginStore.unseal("subscription_words", "system", key, sealed)
	if err != nil {
		return "", err
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", bad("INVALID_SUBSCRIPTION_WORDS", "订阅识别词存储无法读取")
	}
	return text, nil
}

func (a *App) subscriptionEditorValue(ctx context.Context, sub Subscription) (map[string]any, error) {
	text, err := a.subscriptionWords(ctx, sub.ID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return nil, err
	}
	value := map[string]any{}
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value["custom_words"] = text
	return value, nil
}
