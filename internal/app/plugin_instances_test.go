package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func installCloneSource(t *testing.T, p *PluginStore, m plugins.Manifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Install(context.Background(), PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginInstanceCloneBuiltinCatalogNamespaces(t *testing.T) {
	for _, m := range builtin.Catalog() {
		t.Run(m.PluginID, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			installCloneSource(t, p, m)
			// No business run is needed to create an isolated instance of a real builtin.
			out, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "Second", Name: "合成分身", Description: "isolated", Version: "display-9"})
			if e != nil {
				t.Fatal(e)
			}
			if out.InstanceID != m.PluginID+"second" || out.PluginID != m.PluginID || out.Version != m.Version || out.DesiredEnabled {
				t.Fatal(out)
			}
			config, e := p.Config(ctx, out.InstanceID)
			if e != nil || string(config.Value) != `{}` {
				t.Fatal(string(config.Value), e)
			}
			meta, e := p.instanceMetadata(ctx, s.DB, out.InstanceID)
			if e != nil || meta.SourceInstanceID != m.PluginID || meta.DisplayVersion != "display-9" {
				t.Fatal(meta, e)
			}
			for _, table := range []string{"plugin_runs", "plugin_schedules", "plugin_event_subscriptions"} {
				var n int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=?`, out.InstanceID).Scan(&n); e != nil || n != 0 {
					t.Fatal(table, n, e)
				}
			}
			source, e := p.Installation(ctx, m.PluginID)
			if e != nil || source.Generation != 1 || !source.DesiredEnabled {
				t.Fatal(source, e)
			}
		})
	}
}

func TestPluginInstanceCloneCopiesConfigNotHistoryAndKeepsFolder(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	secret := "synthetic-instance-secret-2026"
	original, e := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": true, "enable": true, "secret": secret, "future": map[string]any{"empty": "", "zero": 0, "flag": false}, "onlyonce": true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.SaveData(ctx, m.PluginID, "history", 0, []string{"source-only"}); e != nil {
		t.Fatal(e)
	}
	org, e := p.SaveOrganization(ctx, PluginOrganization{Folders: map[string]json.RawMessage{"synthetic": json.RawMessage(`{"plugins":["SystemProcess","future"],"icon":"x","unknown":false}`)}, Order: json.RawMessage(`["synthetic"]`)})
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "copy", Name: "Copy", Version: "9.9", Icon: "https://synthetic.invalid/icon.svg"})
	if e != nil {
		t.Fatal(e)
	}
	copied, e := p.Config(ctx, out.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	var config map[string]any
	if json.Unmarshal(copied.Value, &config) != nil || config["enabled"] != false || config["enable"] != false || config["secret"] != secret || config["onlyonce"] != true {
		t.Fatal("copy changed business fields", string(copied.Value))
	}
	if _, e = p.Data(ctx, out.InstanceID, "history"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("copied data/history", e)
	}
	current, e := p.Config(ctx, m.PluginID)
	if e != nil || !bytes.Equal(original.Value, current.Value) || original.Revision != current.Revision {
		t.Fatal("source changed", e)
	}
	got, e := p.Organization(ctx)
	if e != nil || got.Revision != org.Revision+1 || string(got.Order) != string(org.Order) || !strings.Contains(string(got.Folders["synthetic"]), `"unknown":false`) || !strings.Contains(string(got.Folders["synthetic"]), `["SystemProcess","future","SystemProcesscopy"]`) {
		t.Fatal(got, e)
	}
	var sourceSealed, targetSealed []byte
	s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config'`, m.PluginID).Scan(&sourceSealed)
	s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config'`, out.InstanceID).Scan(&targetSealed)
	if bytes.Equal(sourceSealed, targetSealed) || bytes.Contains(targetSealed, []byte(secret)) {
		t.Fatal("config was not namespace reencrypted")
	}
	if _, e = p.unseal("config", out.InstanceID, "main", sourceSealed); e == nil {
		t.Fatal("source ciphertext accepted in clone namespace")
	}
	if _, e = p.SaveData(ctx, out.InstanceID, "history", 0, []string{"clone-only"}); e != nil {
		t.Fatal(e)
	}
	sourceData, _ := p.Data(ctx, m.PluginID, "history")
	if string(sourceData.Value) != `["source-only"]` {
		t.Fatal("cross-instance data")
	}
	p2, e := NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	meta, e := p2.instanceMetadata(ctx, s.DB, out.InstanceID)
	if e != nil || meta.Name != "Copy" || meta.DisplayVersion != "9.9" {
		t.Fatal(meta, e)
	}
	manifest, _, _ := builtin.Lookup(m.PluginID)
	card, e := p2.presentInstance(ctx, out, manifest)
	if e != nil || card.Version != m.Version || card.DisplayVersion != "9.9" || card.Name != "Copy" {
		t.Fatal(card, e)
	}
}

func TestPluginInstanceCloneConflictRollbackAndConcurrentCreation(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	request := PluginCloneRequest{Suffix: "one", Name: "Copy"}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := p.Clone(ctx, m.PluginID, request)
			if e == nil {
				success.Add(1)
			} else if pluginErrorCode(e) != "PLUGIN_CLONE_CONFLICT" {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("non-atomic creation", success.Load())
	}
	if e := p.Uninstall(ctx, m.PluginID+"one"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Clone(ctx, m.PluginID, request); pluginErrorCode(e) != "PLUGIN_CLONE_CONFLICT" {
		t.Fatal("reused tombstone", e)
	}
	if _, e := s.DB.Exec(`CREATE TRIGGER clone_metadata_fail BEFORE INSERT ON plugin_instance_metadata BEGIN SELECT RAISE(ABORT,'synthetic clone metadata failure'); END;`); e != nil {
		t.Fatal(e)
	}
	request.Suffix = "rollback"
	if _, e := p.Clone(ctx, m.PluginID, request); e == nil {
		t.Fatal("failed final write reported success")
	}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_instance_metadata"} {
		var n int
		if e := s.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=?`, m.PluginID+"rollback").Scan(&n); e != nil || n != 0 {
			t.Fatal("partial clone", table, n, e)
		}
	}
}

func TestPluginInstanceCloneInputAndUnavailableSource(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	for _, r := range []PluginCloneRequest{{Suffix: "../escape", Name: "x"}, {Suffix: "", Name: "x"}, {Suffix: "_x", Name: "x"}, {Suffix: "x", Name: "x", Icon: "javascript:alert(1)"}, {Suffix: "x", Name: "x", Icon: "https://user:secret@synthetic.invalid/x"}, {Suffix: "x", Name: "x\n"}} {
		if _, e := p.Clone(ctx, m.PluginID, r); pluginErrorCode(e) != "INVALID_PLUGIN_CLONE" {
			t.Fatal(r, e)
		}
	}
	if e := p.Uninstall(ctx, m.PluginID); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "x", Name: "x"}); pluginErrorCode(e) != "PLUGIN_NOT_INSTALLED" {
		t.Fatal(e)
	}
	installStoredPlugin(t, p, "unavailable")
	if _, e := p.Clone(ctx, "unavailable", PluginCloneRequest{Suffix: "x", Name: "x"}); pluginErrorCode(e) != "PLUGIN_IMPLEMENTATION_UNAVAILABLE" {
		t.Fatal(e)
	}
}

func TestPluginInstanceCloneDefaultReferenceIsolation(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, _, _ := builtin.Lookup("AutoSignIn")
	installCloneSource(t, p, m)
	if _, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "second", Name: "Sign in copy"}); e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, peers, e := p.siteControlPeersTx(ctx, tx)
	tx.Rollback()
	if e != nil || peers[m.PluginID].ID != m.PluginID {
		t.Fatal("clone made source reference ambiguous", peers, e)
	}
	if e = p.Uninstall(ctx, m.PluginID); e != nil {
		t.Fatal(e)
	}
	tx, e = s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, peers, e = p.siteControlPeersTx(ctx, tx)
	tx.Rollback()
	if e != nil {
		t.Fatal(e)
	}
	if _, exists := peers[m.PluginID]; exists {
		t.Fatal("source reference silently retargeted clone")
	}
}

func TestPluginInstanceCloneActualWorkersAndHTTP(t *testing.T) {
	for _, id := range []string{"CategoryEditor", "SystemProcess", "SiteStatistic", "AutoSignIn", "RemoveLink", "BrushFlowLanduo"} {
		t.Run(id, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			h := a.Handler()
			ctx := context.Background()
			w := callTest(t, h, "POST", "/api/plugins/install", `{"plugin_id":"`+id+`"}`, cookie, "")
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			run, e := a.pluginStore.ClaimRun(ctx)
			if e != nil {
				t.Fatal(e)
			}
			a.executePluginRun(ctx, run)
			finished, e := a.pluginStore.Run(ctx, run.ID)
			if e != nil || finished.State != "succeeded" {
				t.Fatal(finished, e)
			}
			source, e := a.pluginStore.Config(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			path := "/api/plugins/" + id + "/clone"
			body := `{"suffix":"copy","name":"Actual clone","version":"display-2"}`
			if w = callTest(t, h, "POST", path, body, nil, ""); w.Code != 401 {
				t.Fatal("unauthenticated clone", w.Code)
			}
			if w = callTest(t, h, "POST", path, body, cookie, "http://evil.example"); w.Code != 403 {
				t.Fatal("cross-origin clone", w.Code)
			}
			if w = callTest(t, h, "POST", path, body, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"created":true`) {
				t.Fatal(w.Code, w.Body.String())
			}
			clone := id + "copy"
			if _, e = a.pluginStore.ClaimRun(ctx); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("creation queued business execution", e)
			}
			if w = callTest(t, h, "GET", "/api/plugins/"+clone+"/form", "", cookie, ""); w.Code != 200 {
				t.Fatal("real clone worker form", w.Body.String())
			}
			value, e := a.pluginStore.Config(ctx, clone)
			if e != nil {
				t.Fatal(e)
			}
			var config map[string]any
			if json.Unmarshal(value.Value, &config) != nil {
				t.Fatal("config parse")
			}
			saveBody, e := json.Marshal(map[string]any{"revision": value.Revision, "config": config})
			if e != nil {
				t.Fatal(e)
			}
			if w = callTest(t, h, "PUT", "/api/plugins/"+clone+"/config", string(saveBody), cookie, ""); w.Code != 200 {
				t.Fatal("clone configuration API", w.Code, w.Body.String())
			}
			run, e = a.pluginStore.ClaimRun(ctx)
			if e != nil || run.InstanceID != clone {
				t.Fatal(run, e)
			}
			a.executePluginRun(ctx, run)
			finished, e = a.pluginStore.Run(ctx, run.ID)
			if e != nil || finished.State != "succeeded" {
				t.Fatal("real clone init", finished, e)
			}
			current, e := a.pluginStore.Config(ctx, id)
			if e != nil || current.Revision != source.Revision || !bytes.Equal(current.Value, source.Value) {
				t.Fatal("clone init modified source", e)
			}
			if w = callTest(t, h, "POST", path, body, cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_CLONE_CONFLICT") {
				t.Fatal(w.Code, w.Body.String())
			}
			if w = callTest(t, h, "GET", "/api/plugins", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"display_version":"display-2"`) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestPluginInstanceCloneColdReopenAndNestedLineage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	open := true
	t.Cleanup(func() {
		if open {
			s.Close()
		}
	})
	p, e := NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, e = p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enable": true, "future": false}); e != nil {
		t.Fatal(e)
	}
	first, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "A", Name: "First", Description: "custom", Version: "view-8"})
	if e != nil {
		t.Fatal(e)
	}
	second, e := p.Clone(ctx, first.InstanceID, PluginCloneRequest{Suffix: "B"})
	if e != nil {
		t.Fatal(e)
	}
	if second.InstanceID != "SystemProcessab" {
		t.Fatal(second)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	open = false
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	open = true
	p, e = NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	for _, instance := range []string{first.InstanceID, second.InstanceID} {
		in, e := p.Installation(ctx, instance)
		if e != nil || in.DesiredEnabled || in.Version != m.Version {
			t.Fatal(in, e)
		}
		config, e := p.Config(ctx, instance)
		if e != nil || string(config.Value) != `{"enable":false,"future":false}` {
			t.Fatal(string(config.Value), e)
		}
		meta, e := p.instanceMetadata(ctx, s.DB, instance)
		if e != nil || meta.Name != "First" || meta.DisplayVersion != "view-8" || meta.Description != "custom" {
			t.Fatal(meta, e)
		}
	}
	if _, e = p.ClaimRun(ctx); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("cold recovery started inactive clone", e)
	}
}

func TestPluginInstanceCloneRollbackMatrixAndCanceledRequest(t *testing.T) {
	for _, table := range []string{"plugin_values", "plugin_instance_metadata", "plugin_organization"} {
		t.Run(table, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			m, _, _ := builtin.Lookup("SiteStatistic")
			installCloneSource(t, p, m)
			if _, e := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enabled": true}); e != nil {
				t.Fatal(e)
			}
			if _, e := p.SaveOrganization(ctx, PluginOrganization{Folders: map[string]json.RawMessage{"f": json.RawMessage(`["SiteStatistic"]`)}, Order: json.RawMessage(`[]`)}); e != nil {
				t.Fatal(e)
			}
			before, e := p.Organization(ctx)
			if e != nil {
				t.Fatal(e)
			}
			// Every synthetic fault happens after the install transaction has started.
			if _, e = s.DB.Exec(`CREATE TRIGGER synthetic_clone_write_fail BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'synthetic clone failure'); END;`); e != nil {
				t.Fatal(e)
			}
			if _, e = p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "broken"}); e == nil {
				t.Fatal("fault reported success")
			}
			for _, table := range []string{"plugin_installations", "plugin_values", "plugin_instance_metadata", "native_site_reference_protocols"} {
				var n int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE instance_id='SiteStatisticbroken'`).Scan(&n); e != nil || n != 0 {
					t.Fatal("partial creation", table, n, e)
				}
			}
			after, e := p.Organization(ctx)
			if e != nil || after.Revision != before.Revision || !bytes.Equal(after.Folders["f"], before.Folders["f"]) {
				t.Fatal("partial folder mutation", after, e)
			}
		})
	}
	_, p := pluginStoreFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	cancel()
	if _, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "canceled"}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := p.Installation(context.Background(), m.PluginID+"canceled"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("canceled request created instance", e)
	}
}

func TestPluginInstanceCloneDoesNotBorrowUnclaimedReferencesOrRuns(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, _, _ := builtin.Lookup("AutoSignIn")
	installCloneSource(t, p, m)
	_, sourceRun, e := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"enabled": false})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "copy"}); e != nil {
		t.Fatal(e)
	}
	after, e := p.Run(ctx, sourceRun.ID)
	if e != nil || after.State != sourceRun.State || after.Generation != sourceRun.Generation || !bytes.Equal(after.Input, sourceRun.Input) {
		t.Fatal("source run changed", after, e)
	}
	in, e := p.Installation(ctx, m.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	protocol := siteReferenceProtocol{Version: 1, Purpose: "site-reference-protocol/v1", Protocol: originalUnclaimedSiteReferenceProtocol, Instance: in.InstanceID, Plugin: in.PluginID, PluginVersion: in.Version, Generation: in.Generation, Source: "synthetic-unclaimed"}
	sealed, e := s.Seal(protocol)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id=? AND generation=?`, sealed, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "blocked"}); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("unclaimed references relabeled native", e)
	}
	if _, e = p.Installation(ctx, m.PluginID+"blocked"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("blocked clone left installation", e)
	}
}

func TestPluginInstanceCloneMissingMetadataDoesNotRetargetReferences(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m, _, _ := builtin.Lookup("AutoSignIn")
	installCloneSource(t, p, m)
	clone, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "copy"})
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Uninstall(ctx, m.PluginID); e != nil {
		t.Fatal(e)
	}
	// Owned synthetic corruption: authenticated manifest marker must prevent a
	// missing metadata row from turning this clone into an implicit default peer.
	if _, e = s.DB.Exec(`DELETE FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.instanceMetadata(ctx, s.DB, clone.InstanceID); pluginErrorCode(e) != "PLUGIN_INSTANCE_METADATA_INVALID" {
		t.Fatal("missing clone identity accepted", e)
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = p.siteControlPeersTx(ctx, tx)
	tx.Rollback()
	if pluginErrorCode(e) != "PLUGIN_INSTANCE_METADATA_INVALID" {
		t.Fatal("damaged clone silently became default provider", e)
	}
}

func TestPluginInstanceClonePinnedOriginalOrchestration(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/plugins/instance-framework/original-clone-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		SourceBlob   string `json:"source_blob"`
		ExcerptSHA   string `json:"excerpt_sha256"`
		Cases        []struct {
			Name, Suffix string
			Config       json.RawMessage
			Success      bool
			Result       string
			CloneConfig  json.RawMessage `json:"clone_config"`
			InitCount    int             `json:"init_count"`
		}
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile("../../testdata/plugins/instance-framework/reference/clone_plugin.py")
	if e != nil || tokenHash(string(source)) != oracle.ExcerptSHA || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || oracle.SourceBlob != "8fc8954d7e7df32b970f16f461efa244fa2f142c" {
		t.Fatal("original provenance mismatch", e)
	}
	for _, c := range oracle.Cases {
		if !c.Success {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			if string(c.Config) != "null" {
				if _, e = p.SaveConfig(ctx, m.PluginID, 0, c.Config); e != nil {
					t.Fatal(e)
				}
			}
			clone, e := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: c.Suffix, Name: "合成分身"})
			if e != nil || clone.InstanceID != c.Result {
				t.Fatal("original ID differs", clone, e)
			}
			value, e := p.Config(ctx, clone.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			var original, actual map[string]any
			json.Unmarshal(c.CloneConfig, &original)
			json.Unmarshal(value.Value, &actual)
			switch c.Name {
			case "copied_flags_and_unknown_values":
				if !reflect.DeepEqual(original, actual) {
					t.Fatal("copy contract differs", original, actual)
				}
			case "absent_config":
				if len(actual) != 0 {
					t.Fatal("invented absent business configuration", actual)
				}
			case "single_flag_original_adds_second_spelling":
				if original["enabled"] != false || actual["enable"] != false {
					t.Fatal(original, actual)
				}
				if _, exists := actual["enabled"]; exists {
					t.Fatal("typed plugin received invented second spelling")
				}
			default:
				t.Fatal("unreviewed original oracle case", c.Name)
			}
			var n int
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, clone.InstanceID).Scan(&n); e != nil || n != 0 {
				t.Fatal("cold-creation safety contract", n, e)
			}
			if c.Name != "absent_config" && c.InitCount != 1 {
				t.Fatal("original activation delta no longer documented", c.InitCount)
			}
		})
	}
}
