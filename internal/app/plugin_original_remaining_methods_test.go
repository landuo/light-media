package app

import (
	"context"
	"encoding/json"
	"testing"

	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/remoteidentifiers"
	"light-media/internal/plugins/subscribegroup"
)

func remainingAssertState(t *testing.T, p *originalPluginInstance, want bool) {
	t.Helper()
	got, handled, err := originalRemainingState(context.Background(), p)
	if err != nil || !handled || got != want {
		t.Fatalf("state=%v handled=%v err=%v want=%v", got, handled, err, want)
	}
}

// All initialization below enters the real App/PluginStore/host path. The
// fixture does not start scheduler pumps or send any external request.
func TestOriginalRemainingAppRemoteRetainsActualMember(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "remaining-remote-source", builtin.RemoteIdentifiersManifest())
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": true, "cron": "bad cron", "file_urls": 17})
	if err := p.init(context.Background()); err != nil {
		t.Fatal("public service validation must not run inside original init", err)
	}
	member, ok := p.nativeState.(*remoteidentifiers.State)
	if !ok {
		t.Fatalf("actual member=%T", p.nativeState)
	}
	remainingAssertState(t, p, true)
	// The DB config changes, but the loaded getter must keep the initialized member.
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": false})
	remainingAssertState(t, p, true)
	originalBuiltinIntegrationSave(t, p, map[string]any{})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.nativeState != member || member.Cron != "bad cron" {
		t.Fatal("empty direct init replaced or reset member")
	}
	remainingAssertState(t, p, true)
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=?`, p.installation.InstanceID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("init registered public service: count=%d err=%v", count, err)
	}
	sibling := originalBuiltinIntegrationObject(t, a, "remaining-remote-sibling", builtin.RemoteIdentifiersManifest())
	originalBuiltinIntegrationSave(t, sibling, map[string]any{"enable": false})
	if err := sibling.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	remainingAssertState(t, sibling, false)
	if sibling.nativeState == member {
		t.Fatal("source/clone share member")
	}
}

func TestOriginalRemainingAppRemotePartialFailure(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "remaining-remote-partial", builtin.RemoteIdentifiersManifest())
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": false})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := p.nativeState
	// Missing source timezone is an actual existing dependency failure, before
	// private date scheduling. No fake receipt, service invocation or network.
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": true, "onlyonce": true})
	if err := p.init(context.Background()); err == nil {
		t.Fatal("expected unavailable timezone dependency")
	}
	if p.nativeState != before {
		t.Fatal("failed init replaced member")
	}
	remainingAssertState(t, p, true)
}

func TestOriginalRemainingAppSubscribeCategoryAndEmpty(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	p := originalBuiltinIntegrationObject(t, a, "remaining-subscribe-source", builtin.SubscribeGroupManifest())
	originalBuiltinIntegrationSave(t, p, map[string]any{"enabled": false, "category": true, "update_confs": "category:owned#resolution:1080p"})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	member, ok := p.nativeState.(*subscribegroup.Machine)
	if !ok {
		t.Fatalf("actual member=%T", p.nativeState)
	}
	remainingAssertState(t, p, true)
	originalBuiltinIntegrationSave(t, p, map[string]any{})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.nativeState != member || !member.Config.Category || member.Config.Enabled || len(member.Rules) != 1 {
		t.Fatal("empty init changed category/member/rules")
	}
	remainingAssertState(t, p, true)
	jobs, handled, err := originalRemainingServices(context.Background(), p)
	if err != nil || !handled || jobs != nil {
		t.Fatalf("services=%v handled=%v err=%v", jobs, handled, err)
	}
	sibling := originalBuiltinIntegrationObject(t, a, "remaining-subscribe-clone", builtin.SubscribeGroupManifest())
	originalBuiltinIntegrationSave(t, sibling, map[string]any{"enable": false, "enabled": false, "category": true})
	if err := sibling.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	remainingAssertState(t, sibling, true)
	if sibling.nativeState == member {
		t.Fatal("source/clone share member")
	}
}

func TestOriginalRemainingAppBrushCapturesActualObject(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	manifest, _, ok := builtin.Lookup("BrushFlowLanduo")
	if !ok {
		t.Fatal("missing compiled brush")
	}
	p := originalBuiltinIntegrationObject(t, a, "remaining-brush-source", manifest)
	originalBuiltinIntegrationSave(t, p, map[string]any{"enabled": false, "tasks": []any{}})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	member, ok := p.nativeState.(*bf.Core)
	if !ok {
		t.Fatalf("actual member=%T", p.nativeState)
	}
	remainingAssertState(t, p, false)
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.nativeState != member {
		t.Fatal("direct init replaced brush object")
	}
	jobs, handled, err := originalRemainingServices(context.Background(), p)
	if err != nil || !handled || len(jobs) != 0 {
		t.Fatalf("services=%v handled=%v err=%v", jobs, handled, err)
	}
}

func TestOriginalRemainingAbsentMemberNeverReconstructs(t *testing.T) {
	for _, id := range []string{"RemoteIdentifiers", "SubscribeGroup", "BrushFlowLanduo"} {
		p := &originalPluginInstance{installation: PluginInstallation{PluginID: id}, appliedConfig: json.RawMessage(`{"enabled":true,"enable":true,"category":true}`)}
		if _, handled, err := originalRemainingState(context.Background(), p); !handled || err == nil {
			t.Fatalf("%s silently reconstructed", id)
		}
	}
}

func TestOriginalRemainingAppRemoteServicesSeparatePrivateOnce(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	identifiersSeedSource(t, a)
	p := originalBuiltinIntegrationObject(t, a, "remaining-remote-services", builtin.RemoteIdentifiersManifest())
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": true, "onlyonce": true, "cron": "0 4 * * *", "file_urls": 17})
	if err := p.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	member := p.nativeState.(*remoteidentifiers.State)
	if remoteidentifiers.Truth(member.OnlyOnce) {
		t.Fatal("private once member was not cleared")
	}
	jobs, handled, err := originalRemainingServices(context.Background(), p)
	if err != nil || !handled || len(jobs) != 1 || jobs[0].ID != "RemoteIdentifiers" || jobs[0].Trigger.Kind != "cron" {
		t.Fatalf("jobs=%+v handled=%v err=%v", jobs, handled, err)
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key='once'`, p.installation.InstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("private once jobs=%d err=%v", count, err)
	}
	if _, _, err := member.Task(); err == nil {
		t.Fatal("fixture must keep invalid task URL type; service getter must not validate it")
	}
	originalBuiltinIntegrationSave(t, p, map[string]any{"enable": true, "cron": "invalid"})
	if err := p.init(context.Background()); err != nil {
		t.Fatal("init incorrectly evaluated public cron", err)
	}
	if _, _, err := originalRemainingServices(context.Background(), p); err == nil {
		t.Fatal("public invalid cron was swallowed")
	}
}
