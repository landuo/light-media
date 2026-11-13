package app

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestOriginalAuthSkippedDeclarationsDoNotReadDisplayMetadata(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixtureBinary(t, a, os.Getenv("OWNED_AUTH_CACHED_CONFIG_FIXTURE_BINARY"))
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":[null,false,{},42,"skip"]}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	// The real SDK state/provider/render getters remain available. Only the
	// class-name metadata projection is deliberately unavailable, equivalent to
	// the fixed oracle's throwing plugin_name property for skipped rows.
	if _, err := a.Store.DB.Exec(`DROP TABLE plugin_instance_metadata`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(pluginInstanceSchema); err != nil {
			t.Error(err)
		}
	}()
	object := originalBaseSDKLoaded(t, a, "OwnedAuth")
	if enabled, err := object.getState(context.Background()); err != nil || !enabled {
		t.Fatal("cached SDK state getter unavailable", enabled, err)
	}
	if _, err := object.callOriginalMethod(context.Background(), "get_auth_providers", nil); err != nil {
		t.Fatal("cached SDK provider getter unavailable", err)
	}
	if _, _, _, err := object.originalViewRenderMode(context.Background()); err != nil {
		t.Fatal("cached SDK rendering getter unavailable", err)
	}
	providers, err := a.originalPluginAuthProviders(context.Background())
	if err != nil || len(providers) != 0 {
		t.Fatal("skipped provider declarations read source class name", err, providers)
	}
}

func TestOriginalAuthLiveDeclarationStillRequiresDisplayMetadata(t *testing.T) {
	a := newTestApp(t)
	cookie := installOwnedAuthFixtureBinary(t, a, os.Getenv("OWNED_AUTH_CACHED_CONFIG_FIXTURE_BINARY"))
	response := ownedAuthConfig(t, a, cookie, "OwnedAuth", `{"enabled":true,"providers":[{"name":"owned live provider"}]}`)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	// The real SDK state/provider/render getters remain available. Only the
	// class-name metadata projection is deliberately unavailable, equivalent to
	// the fixed oracle's throwing plugin_name property for skipped rows.
	if _, err := a.Store.DB.Exec(`DROP TABLE plugin_instance_metadata`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(pluginInstanceSchema); err != nil {
			t.Error(err)
		}
	}()
	object := originalBaseSDKLoaded(t, a, "OwnedAuth")
	if enabled, err := object.getState(context.Background()); err != nil || !enabled {
		t.Fatal("cached SDK state getter unavailable", enabled, err)
	}
	if _, err := object.callOriginalMethod(context.Background(), "get_auth_providers", nil); err != nil {
		t.Fatal("cached SDK provider getter unavailable", err)
	}
	if _, _, _, err := object.originalViewRenderMode(context.Background()); err != nil {
		t.Fatal("cached SDK rendering getter unavailable", err)
	}
	providers, err := a.originalPluginAuthProviders(context.Background())
	if err == nil || len(providers) != 0 {
		t.Fatal("live provider bypassed unavailable class-name metadata", err, providers)
	}
}

func TestOriginalAuthTicketModernEpochPreservesSourceFloatClock(t *testing.T) {
	var store originalAuthRuntime
	at := time.Unix(1_700_000_000, 0)
	ticket, err := store.create(1, "epoch", nil, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.consume(ticket, at.Add(120*time.Second+time.Nanosecond)); !ok {
		t.Fatal("source double wall clock still equals120s at this epoch")
	}
	ticket, err = store.create(1, "epoch", nil, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.consume(ticket, at.Add(120*time.Second+time.Microsecond)); ok {
		t.Fatal("source wall clock exceeds120s")
	}
	// Distinct native nanoseconds that round to the same source wall time must
	// retain source dict insertion order during oldest-item eviction.
	first, err := store.create(1, "epoch", nil, at.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.create(1, "epoch", nil, at)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1024; i++ {
		if _, err := store.create(1, "epoch", nil, at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := store.tickets[first]; ok {
		t.Fatal("oldest source insertion not evicted")
	}
	if _, ok := store.tickets[second]; !ok {
		t.Fatal("native nanosecond order replaced source equal-clock insertion order")
	}
}
