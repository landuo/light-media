package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestNotificationChannelActualEncryptedCASAndCold(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	first, e := p.NotificationChannels(ctx)
	if e != nil || first.Revision != 0 || len(first.Value) != 0 {
		t.Fatal(first, e)
	}
	want := []any{map[string]any{"name": "custom", "type": "larkmessager", "enabled": false, "switchs": []any{"站点消息"}, "config": map[string]any{"secret": "owned-secret-private", "uid": json.Number("9007199254740993")}, "future": map[string]any{"x": true}}}
	next, e := p.SaveNotificationChannels(ctx, 0, want)
	if e != nil || next.Revision != 1 {
		t.Fatal(next, e)
	}
	if _, e = p.SaveNotificationChannels(ctx, 0, []any{}); pluginErrorCodeInternal(e) != "NOTIFICATION_CHANNEL_REVISION_CONFLICT" {
		t.Fatal("stale CAS accepted", e)
	}
	var sealed []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM notification_channel_head`).Scan(&sealed); e != nil || bytes.Contains(sealed, []byte("owned-secret-private")) {
		t.Fatal("plaintext channel credentials", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e = NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	cold, e := p.NotificationChannels(ctx)
	if e != nil || cold.Revision != 1 || !bytes.Equal(mustCleanSeedJSON(cold.Value), mustCleanSeedJSON(want)) {
		t.Fatal("original list/unknown fields/numbers changed", cold, e)
	}
}
func TestNotificationChannelMissingHistoryOrHeadCannotReinitialize(t *testing.T) {
	for _, where := range []string{"head", "history", "marker"} {
		t.Run(where, func(t *testing.T) {
			ctx := context.Background()
			s, p := pluginStoreFixture(t)
			if _, e := p.SaveNotificationChannels(ctx, 0, []any{map[string]any{"name": "Lark", "config": map[string]any{"secret": "fixture"}}}); e != nil {
				t.Fatal(e)
			}
			var e error
			switch where {
			case "head":
				_, e = s.DB.Exec(`DELETE FROM notification_channel_head`)
			case "history":
				_, e = s.DB.Exec(`DELETE FROM notification_channel_history WHERE revision=0`)
			case "marker":
				_, e = s.DB.Exec(`DELETE FROM records WHERE kind='core_settings_lifecycle' AND id='notifications/v1'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = NewPluginStore(ctx, s); pluginErrorCodeInternal(e) != "NOTIFICATION_CHANNEL_EVIDENCE_MISSING" {
				t.Fatal("bootstrap repaired lost proof", e)
			}
		})
	}
}
