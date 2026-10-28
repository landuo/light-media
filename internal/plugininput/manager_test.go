package plugininput

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func stringPtr(value string) *string { return &value }
func create(t *testing.T, m *Manager, r plugins.PendingInputInteraction, at time.Time) plugins.PendingInputInteraction {
	t.Helper()
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 120
	}
	out, err := m.Create(r, at)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestSourceMatchingReplacementAndOwnership(t *testing.T) {
	var m Manager
	at := time.Unix(100, 0)
	web, source := stringPtr("Web"), stringPtr("admin")
	global := create(t, &m, plugins.PendingInputInteraction{UserID: "admin", PluginID: "a"}, at)
	narrow := create(t, &m, plugins.PendingInputInteraction{UserID: "admin", PluginID: "a", Channel: web, Source: source, ChatID: stringPtr("one")}, at)
	other := create(t, &m, plugins.PendingInputInteraction{UserID: "admin", PluginID: "a", Channel: web, Source: source, ChatID: stringPtr("two")}, at)
	if got := m.Lookup(NewKey("admin", web, source, stringPtr("one")), "b", true, at); got != nil {
		t.Fatal("cross-plugin pop")
	}
	if got := m.Lookup(NewKey("admin", web, source, stringPtr("one")), "a", false, at); got == nil || got.RequestID != narrow.RequestID {
		t.Fatal("specific chat lost")
	}
	broad := create(t, &m, plugins.PendingInputInteraction{UserID: "admin", PluginID: "b", Channel: web}, at)
	if _, ok := m.byID[narrow.RequestID]; ok {
		t.Fatal("overlap not replaced")
	}
	if _, ok := m.byID[other.RequestID]; ok {
		t.Fatal("other chat not replaced by no-chat request")
	}
	if _, ok := m.byID[global.RequestID]; !ok {
		t.Fatal("None/None must remain a separate scope")
	}
	got, status := m.Consume(NewKey("admin", web, source, stringPtr("one")), nil, false, at)
	if got == nil || got.RequestID != broad.RequestID || status != "active" {
		t.Fatal("channel wildcard fallback")
	}
	if got, _ = m.Consume(NewKey("admin", web, source, nil), nil, false, at); got != nil {
		t.Fatal("global None/None must not match concrete incoming channel")
	}
}
func TestSourceExpiryBoundaryTombstoneAndPromptReply(t *testing.T) {
	var m Manager
	at := time.Unix(100, 0)
	ch, src, chat := stringPtr("Telegram"), stringPtr("bot"), stringPtr("9007199254740993")
	key := NewKey("7", ch, src, chat)
	r := plugins.PendingInputInteraction{UserID: "7", PluginID: "a", Channel: ch, Source: src, ChatID: chat, PromptMessageID: stringPtr("17"), TimeoutSeconds: 1}
	create(t, &m, r, at)
	if got, _ := m.Consume(key, stringPtr("18"), false, at); got != nil {
		t.Fatal("wrong reply consumed")
	}
	if got, status := m.Consume(key, stringPtr("17"), false, at.Add(time.Second)); got == nil || status != "active" {
		t.Fatal("equality at expiry must remain active")
	}
	create(t, &m, r, at)
	if got, _ := m.Consume(key, nil, true, at); got == nil {
		t.Fatal("cancel must bypass reply id")
	}
	create(t, &m, r, at)
	if got, status := m.Consume(key, nil, false, at.Add(time.Second+1)); got != nil || status != "" {
		t.Fatal("expired prompt should disappear silently")
	}
	r.PromptMessageID = nil
	create(t, &m, r, at)
	if got := m.Lookup(key, "a", false, at.Add(2*time.Second)); got != nil {
		t.Fatal("get should move expired request to tombstone")
	}
	if got, status := m.Consume(key, nil, false, at.Add(301*time.Second)); got == nil || status != "expired" {
		t.Fatal("exact grace boundary lost")
	}
	create(t, &m, r, at)
	_ = m.Lookup(key, "a", false, at.Add(2*time.Second))
	if got, _ := m.Consume(key, nil, false, at.Add(301*time.Second+1)); got != nil {
		t.Fatal("old tombstone retained")
	}
}
func TestSourceSingleConsumptionAndCopyIsolation(t *testing.T) {
	var m Manager
	at := time.Now()
	ch := stringPtr("Web")
	r := create(t, &m, plugins.PendingInputInteraction{UserID: "a", PluginID: "a", Channel: ch, Payload: []byte(`{"x":1}`)}, at)
	*r.Channel = "modified"
	r.Payload[2] = 'y'
	*ch = "changed"
	key := NewKey("a", stringPtr("Web"), nil, nil)
	got := m.Lookup(key, "a", false, at)
	if got == nil || *got.Channel != "Web" || string(got.Payload) != `{"x":1}` {
		t.Fatal("shared input/output storage")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, _ := m.Consume(key, nil, false, at); r != nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("duplicate consumption", successes.Load())
	}
	m.Clear()
	if got, _ := m.Consume(key, nil, false, at); got != nil {
		t.Fatal("clear")
	}
}
