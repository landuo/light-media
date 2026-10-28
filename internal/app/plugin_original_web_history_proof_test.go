package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// New private source columns must retain the existing exact receipt checks;
// accepting notificationRecord is not permission to accept a resealed forgery.
func TestOriginalWebMessageCoreHistoryReceiptAuthenticatesColumnsAndTimezone(t *testing.T) {
	for _, mode := range []string{"cipher_tamper", "resealed_source_columns", "public_projection_tamper", "public_time_index_tamper", "restart_timezone"} {
		t.Run(mode, func(t *testing.T) {
			a, input, _ := uiSourceAddFixture(t)
			subscriptionStatsInstallSettings(t, a, "http://owned-unused.invalid", false)
			if _, err := a.createUISubscription(context.Background(), input, "owned-history-user"); err != nil {
				t.Fatal("actual core route did not finish", err)
			}
			route := noticeV2OnlyRoute(t, a)
			var raw []byte
			if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='notification' ORDER BY rowid LIMIT 1`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var record notificationRecord
			if json.Unmarshal(raw, &record) != nil || len(record.ProtectedContent) == 0 {
				t.Fatal("actual core history lacks private source columns")
			}
			if mode == "restart_timezone" {
				// Replay a previous process's stored local timezone, rather than
				// mutating Go's process-global time.Local while App workers run.
				var payload originalMessageHistoryPayload
				if err := a.Store.Unseal(record.ProtectedContent, &payload); err != nil {
					t.Fatal(err)
				}
				payload.UTCOffset = -11 * 3600
				stamp, err := time.Parse(time.RFC3339Nano, record.CreatedAt)
				if err != nil {
					t.Fatal(err)
				}
				record.SourceRegisteredAt = stamp.In(time.FixedZone("owned prior process", payload.UTCOffset)).Format("2006-01-02 15:04:05")
				var fields map[string]any
				if json.Unmarshal(payload.SourceMessage, &fields) != nil {
					t.Fatal("source columns unavailable")
				}
				fields["reg_time"] = record.SourceRegisteredAt
				payload.SourceMessage, _ = json.Marshal(fields)
				record.ProtectedContent, err = a.Store.Seal(payload)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Store.Put(context.Background(), "notification", record.ID, record); err != nil {
					t.Fatal(err)
				}
				if _, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route.ID); err != nil {
					t.Fatal("valid stored local source time depended on restart timezone", err)
				}
				return
			}
			switch mode {
			case "cipher_tamper":
				record.ProtectedContent[len(record.ProtectedContent)-1] ^= 1
			case "public_projection_tamper":
				record.Title = "owned forged public projection"
			case "public_time_index_tamper":
				record.SourceRegisteredAt = "2999-01-01 00:00:00"
			case "resealed_source_columns":
				var payload originalMessageHistoryPayload
				if err := a.Store.Unseal(record.ProtectedContent, &payload); err != nil {
					t.Fatal(err)
				}
				var columns map[string]any
				if json.Unmarshal(payload.SourceMessage, &columns) != nil {
					t.Fatal("source columns unavailable")
				}
				columns["text"] = "owned forged private columns"
				payload.SourceMessage, _ = json.Marshal(columns)
				var err error
				record.ProtectedContent, err = a.Store.Seal(payload)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Store.Put(context.Background(), "notification", record.ID, record); err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.pluginStore.readNoticeRoute(context.Background(), a.Store.DB, route.ID); err == nil {
				t.Fatal("changed history was accepted as original source output", mode)
			}
		})
	}
}
