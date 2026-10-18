package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	lark "light-media/internal/plugins/larkmessager"
)

func installLarkKVFixture(t *testing.T, p *PluginStore, id string) pluginLarkKV {
	t.Helper()
	manifest, err := json.Marshal(lark.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Install(context.Background(), PluginInstallation{InstanceID: id, PluginID: "LarkMessager", Version: "0.9.1", ManifestSchemaVersion: 1, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.Installation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return pluginLarkKV{p, id, in.Generation}
}

func TestLarkOriginalPageAndStatusUseActualEncryptedKVAcrossCold(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p, err := NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	kv := installLarkKVFixture(t, p, "LarkMessager")
	// Unknown nested fields and integers greater than IEEE754's exact range must
	// survive this storage adapter just as they do in original plugin data.
	value, err := lark.DecodeSourceObject([]byte(`{"ok":true,"msg":"owned-private-chat-result","time":"2026-10-18 00:00:00","displayed":false,"extra":{"count":900719925474099312345}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"last_chats", "last_test_result"} {
		if err = kv.Put(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	var ciphertext []byte
	if err = store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='LarkMessager' AND kind='data' AND item_key='last_chats'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("owned-private-chat-result")) {
		t.Fatal("Lark result persisted in plaintext")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err = NewPluginStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	kv.store = p
	plugin := lark.New(lark.PluginHost{KV: kv})
	if _, err = plugin.Init(lark.Object{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	page, err := plugin.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rendered, _ := json.Marshal(page)
	if strings.Count(string(rendered), "owned-private-chat-result") != 2 {
		t.Fatal("actual source Page did not display both stored results")
	}
	for _, key := range []string{"last_chats", "last_test_result"} {
		got, err := kv.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if got["displayed"] != true {
			t.Fatal("original Page display acknowledgement not persisted", key)
		}
		raw, err := json.Marshal(got)
		if err != nil || !bytes.Contains(raw, []byte(`900719925474099312345`)) {
			t.Fatal("unknown data or integer precision lost", err)
		}
		got["msg"] = "caller mutation"
		again, err := kv.Get(ctx, key)
		if err != nil || again["msg"] != "owned-private-chat-result" {
			t.Fatal("caller alias mutated stored result", err)
		}
	}
	page, err = plugin.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rendered, _ = json.Marshal(page)
	if bytes.Contains(rendered, []byte("owned-private-chat-result")) {
		t.Fatal("already displayed source result shown again")
	}
	if _, err = plugin.Status(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"last_chats", "last_test_result"} {
		got, err := kv.Get(ctx, key)
		if err != nil || got != nil {
			t.Fatal("source Status did not delete retained result", key, err)
		}
	}
}

func TestLarkKVRejectsRetiredOwnerAndKeepsActualNamespacesSeparate(t *testing.T) {
	ctx := context.Background()
	_, p := pluginStoreFixture(t)
	one := installLarkKVFixture(t, p, "LarkMessager")
	two := installLarkKVFixture(t, p, "LarkMessager.other")
	for _, x := range []struct {
		k   pluginLarkKV
		msg string
	}{{one, "one-private"}, {two, "two-private"}} {
		if err := x.k.Put(ctx, "last_test_result", lark.Object{"msg": x.msg}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := one.Get(ctx, "last_test_result")
	if err != nil || got["msg"] != "one-private" {
		t.Fatal("instance result leaked", err)
	}
	if _, err = one.Get(ctx, "history"); !errors.Is(err, lark.ErrDenied) {
		t.Fatal("unrelated key was exposed", err)
	}
	if err = p.Reset(ctx, "LarkMessager"); err != nil {
		t.Fatal(err)
	}
	if _, err = one.Get(ctx, "last_test_result"); !errors.Is(err, lark.ErrDenied) {
		t.Fatal("retired generation could read", err)
	}
	if err = one.Put(ctx, "last_test_result", lark.Object{"msg": "resurrected"}); !errors.Is(err, lark.ErrDenied) {
		t.Fatal("retired generation could write", err)
	}
	if err = one.Delete(ctx, "last_test_result"); !errors.Is(err, lark.ErrDenied) {
		t.Fatal("retired generation could delete", err)
	}
	got, err = two.Get(ctx, "last_test_result")
	if err != nil || got["msg"] != "two-private" {
		t.Fatal("reset affected another instance", err)
	}
	current, err := p.Installation(ctx, "LarkMessager")
	if err != nil {
		t.Fatal(err)
	}
	next := pluginLarkKV{p, "LarkMessager", current.Generation}
	if err = next.Put(ctx, "last_chats", lark.Object{"msg": "new-generation"}); err != nil {
		t.Fatal(err)
	}
	if err = p.Uninstall(ctx, "LarkMessager"); err != nil {
		t.Fatal(err)
	}
	if err = next.Delete(ctx, "last_chats"); !errors.Is(err, lark.ErrDenied) {
		t.Fatal("uninstalled generation could delete retained data", err)
	}
}
