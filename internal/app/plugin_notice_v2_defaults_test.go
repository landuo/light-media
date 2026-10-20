package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPluginNoticeV2ProductionDefaultsSeedOnceAndPreserveExplicitValues(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	value, e := a.pluginStore.noticePreference(ctx, a.Store.DB, "templates", "NotificationTemplates")
	if e != nil {
		t.Fatal(e)
	}
	var expected map[string]any
	if e = json.Unmarshal(notificationTemplateDefaults, &expected); e != nil || !reflect.DeepEqual(value, expected) || len(expected) != 4 {
		t.Fatal("new native default production path missing", value, e)
	}
	for _, value := range []any{nil, map[string]any{}, map[string]any{"subscribeAdded": "{'title':'user custom'}"}} {
		noticePreferenceTest(t, a, "templates", "NotificationTemplates", value)
		if _, e = a.Store.DB.Exec(`DELETE FROM notice_template_initialization`); e != nil {
			t.Fatal(e)
		}
		p, e := NewPluginStore(ctx, a.Store)
		if e != nil {
			t.Fatal(e)
		}
		got, e := p.noticePreference(ctx, a.Store.DB, "templates", "NotificationTemplates")
		if e != nil || !reflect.DeepEqual(value, got) {
			t.Fatal("native initialization overwrote explicit source value", value, got, e)
		}
	}
	if _, e = a.Store.DB.Exec(`DELETE FROM plugin_notice_preferences WHERE kind='templates'`); e != nil {
		t.Fatal(e)
	}
	p, e := NewPluginStore(ctx, a.Store)
	if e != nil {
		t.Fatal(e)
	}
	got, e := p.noticePreference(ctx, a.Store.DB, "templates", "NotificationTemplates")
	if e != nil || got != nil {
		t.Fatal("later absent migration semantics were re-seeded", got, e)
	}
}
