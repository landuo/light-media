package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSiteMessageBindingRejectsForeignIdentityBeforeReceiptRead(t *testing.T) {
	identity := siteMessageIdentity{Collection: "owned-collection", Ordinal: 0}
	p := &PluginStore{}
	for _, change := range []func(*pluginNoticeRoute){
		func(r *pluginNoticeRoute) { r.BusinessV2.Binding.SiteMessage = nil },
		func(r *pluginNoticeRoute) { r.Instance = "plugin-clone" },
		func(r *pluginNoticeRoute) { r.Generation = 2 },
		func(r *pluginNoticeRoute) { r.BusinessV2.Binding.Kind = "subscription_add" },
		func(r *pluginNoticeRoute) { r.BusinessV2.Binding.Inbox = "unrelated" },
		func(r *pluginNoticeRoute) { r.BusinessV2.Binding.CoreInputSHA = "invented" },
		func(r *pluginNoticeRoute) { r.BusinessV2.Binding.Ordinal = 1 },
	} {
		r := pluginNoticeRoute{Instance: siteMessageNoticeOwner, Generation: 1, BusinessV2: &pluginNoticeV2Business{Binding: pluginNoticeV2Binding{Kind: "site_message", SiteMessage: &identity}}}
		change(&r)
		if e := p.validateSiteMessageNoticeBinding(context.Background(), nil, r); e == nil {
			t.Fatal("foreign binding reached a trusted collection")
		}
	}
}

func TestNotificationSourceRetainsLegacyHistoryJSON(t *testing.T) {
	legacy := Notification{ID: "history", Title: "title"}
	raw, e := json.Marshal(legacy)
	if e != nil || strings.Contains(string(raw), `"source"`) {
		t.Fatal("legacy receipt JSON changed", string(raw), e)
	}
	legacy.Source = "shared/source:42"
	raw, e = json.Marshal(legacy)
	if e != nil {
		t.Fatal(e)
	}
	var restored Notification
	if e = json.Unmarshal(raw, &restored); e != nil || restored.Source != legacy.Source {
		t.Fatal("source identity lost", string(raw), e)
	}
}
