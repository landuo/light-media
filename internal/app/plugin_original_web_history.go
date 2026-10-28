package app

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"time"

	"light-media/internal/notice"
)

// Original MessageOper persists source columns, including nullable title/text,
// independently of notification display projections. Retain those columns in
// the existing encrypted content, without a second queue or public metadata.
type originalMessageHistoryPayload struct {
	Notification
	SourceMessage json.RawMessage `json:"source_message"`
	UTCOffset     int             `json:"source_message_utc_offset"`
}

func originalMessageHistoryColumns(fields notice.Object, stamp string) (json.RawMessage, error) {
	values := map[string]any{}
	for _, key := range []string{"source", "title", "text", "image", "link", "userid"} {
		values[key] = fields[key]
	}
	values["channel"], values["mtype"] = "", ""
	for _, key := range []string{"channel", "mtype"} {
		if fields[key] != nil {
			values[key] = fields[key]
		}
	}
	if user, ok := values["userid"].(json.Number); ok {
		values["userid"] = user.String()
	}
	if user, ok := values["userid"].(*big.Int); ok && user != nil {
		values["userid"] = user.String()
	}
	values["action"] = 1
	if value, present := fields["action"]; present {
		values["action"] = value
	}
	values["note"] = map[string]any{}
	if value := fields["note"]; sourceSubscriptionTruth(value) {
		values["note"] = value
	}
	values["reg_time"] = stamp
	return json.Marshal(values)
}

func (a *App) originalMessageHistoryRecord(n Notification, fields notice.Object) (notificationRecord, error) {
	stamp := originalMessageTime(n.CreatedAt)
	raw, err := originalMessageHistoryColumns(fields, stamp)
	if err != nil {
		return notificationRecord{}, err
	}
	offset := 0
	if at, err := time.Parse(time.RFC3339Nano, n.CreatedAt); err == nil {
		_, offset = at.Local().Zone()
	}
	payload := originalMessageHistoryPayload{n, raw, offset}
	sealed, err := a.Store.Seal(payload)
	if err != nil {
		return notificationRecord{}, err
	}
	return notificationRecord{Notification: n, ProtectedContent: sealed, SourceRegisteredAt: stamp}, nil
}

func originalMessageTime(stamp string) string {
	if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		return at.Local().Format("2006-01-02 15:04:05")
	}
	return stamp
}

func (a *App) saveOriginalIncomingWebMessage(ctx context.Context, user, text string) error {
	n := Notification{ID: id("message_"), Source: user, Channel: "Web", Message: text, UserID: &user, Action: json.RawMessage(`0`), Note: json.RawMessage(`{}`), CreatedAt: now(), Read: true}
	fields := notice.Object{"channel": "Web", "source": user, "userid": user, "text": text, "action": 0}
	record, err := a.originalMessageHistoryRecord(n, fields)
	if err != nil {
		return err
	}
	// Incoming rows belong to MessageOper's history, not the sent-notification
	// center. Generic record rowid supplies one stable native ordering identity.
	record.Notification = Notification{ID: n.ID, CreatedAt: n.CreatedAt, Read: true}
	return a.Store.Put(ctx, "message", n.ID, record)
}

func originalMessagePageInteger(r *http.Request, key string, fallback int64) (*big.Int, error) {
	values, present := r.URL.Query()[key]
	if !present {
		return big.NewInt(fallback), nil
	}
	raw := ""
	if len(values) > 0 {
		raw = values[len(values)-1]
	}
	value, ok := notice.CoerceSourceInteger(raw)
	if !ok {
		return nil, &APIError{"INVALID_MESSAGE_PAGE", "消息页码或条数必须是整数", 422}
	}
	parsed, ok := new(big.Int).SetString(value.String(), 10)
	if !ok {
		return nil, &APIError{"INVALID_MESSAGE_PAGE", "消息页码或条数必须是整数", 422}
	}
	return parsed, nil
}

func (a *App) originalWebMessageHistory(r *http.Request) (any, error) {
	page, err := originalMessagePageInteger(r, "page", 1)
	if err != nil {
		return nil, err
	}
	count, err := originalMessagePageInteger(r, "count", 20)
	if err != nil {
		return nil, err
	}
	offset := new(big.Int).Mul(new(big.Int).Sub(page, big.NewInt(1)), count)
	if !count.IsInt64() || !offset.IsInt64() {
		return nil, &APIError{"MESSAGE_PAGE_UNAVAILABLE", "消息分页超出本地存储范围", 500}
	}
	// The fixed API accepts count, while its frontend sends ignored size=20.
	// No channel filter: source MessageOper lists all message rows.
	rows, err := a.Store.DB.QueryContext(r.Context(), `SELECT rowid,payload FROM records WHERE kind IN ('notification','message') ORDER BY coalesce(json_extract(payload,'$.source_registered_at'),substr(json_extract(payload,'$.created_at'),1,19)) DESC,rowid DESC LIMIT ? OFFSET ?`, count.Int64(), offset.Int64())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var key int64
		var raw []byte
		if err = rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var record notificationRecord
		if json.Unmarshal(raw, &record) != nil {
			return nil, bad("MESSAGE_HISTORY_INVALID", "消息历史不可读")
		}
		n, err := a.openNotification(record)
		if err != nil {
			return nil, err
		}
		var source struct {
			SourceMessage json.RawMessage `json:"source_message"`
		}
		if len(record.ProtectedContent) > 0 {
			if err = a.Store.Unseal(record.ProtectedContent, &source); err != nil {
				return nil, err
			}
		}
		var item map[string]any
		if len(source.SourceMessage) > 0 {
			if json.Unmarshal(source.SourceMessage, &item) != nil || item == nil {
				return nil, bad("MESSAGE_HISTORY_INVALID", "消息历史不可读")
			}
		} else {
			optional := func(text string) any {
				if text == "" {
					return nil
				}
				return text
			}
			item = map[string]any{"channel": n.Channel, "source": optional(n.Source), "mtype": n.MType, "title": optional(n.Title), "text": optional(n.Message), "image": optional(n.Image), "link": optional(n.Link), "userid": n.UserID, "reg_time": originalMessageTime(n.CreatedAt), "action": 1, "note": map[string]any{}}
			if len(n.Action) > 0 {
				var action any
				if json.Unmarshal(n.Action, &action) == nil {
					item["action"] = action
				}
			}
			if len(n.Note) > 0 {
				var note any
				if json.Unmarshal(n.Note, &note) == nil && note != nil {
					item["note"] = note
				}
			}
		}
		item["id"] = key
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// User-role messages are intentionally not put into systemMessages: the fixed
// MessageHelper.put rejects role=user, and get(role) shares system/plugin FIFO.
// Ordinary command history must not invent the source's missing user stream.
