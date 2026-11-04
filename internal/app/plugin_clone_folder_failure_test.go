package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"

	"modernc.org/sqlite"
)

// This root exercises the fixed source folder contract through the real clone
// endpoint/SDK. It does not label target output a newly sampled Python oracle.
func TestOriginalCloneFolderFailureActualSDK(t *testing.T) {
	a, cookie, source := cloneLiteralOwnedFixture(t, true)
	ctx := context.Background()
	id := source.installation.InstanceID
	saveOwnedModule(t, a, cookie, source, `{"enabled":true,"marker":"owned-folder","modules":{}}`)
	source = originalBaseSDKLoaded(t, a, id)
	beforeWorker := ownedModuleRead(t, source)
	beforeConfig, err := a.pluginStore.Config(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	code, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
	if err != nil {
		t.Fatal(err)
	}
	reserved := id + "reserved"
	failed := id + "failed"
	recovered := id + "recovered"
	setFolders := func(folders string) PluginOrganization {
		t.Helper()
		body := `{"folders":` + folders + `,"order":["owned-order"]}`
		r := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/organization", body, cookie, "")
		if r.Code != http.StatusOK {
			t.Fatal("owned folder API", r.Code, r.Body.String())
		}
		org, err := a.pluginStore.Organization(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return org
	}
	initial := setFolders(`{"prefix-string":{"plugins":"unrelated"},"prefix-dict":{"plugins":{"unrelated":true}},"z-first":{"plugins":["` + id + `","` + reserved + `"],"icon":"owned-icon","opaque":{"keep":[0,false," "]}},"a-second":["` + id + `"]}`)
	if !slices.Equal(originalFolderKeys(initial), []string{"prefix-string", "prefix-dict", "z-first", "a-second"}) {
		t.Fatal("actual API folder insertion order")
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_clone_folder_fail BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned clone SQL sentinel must not enter logs'); END`); err != nil {
		t.Fatal(err)
	}
	// This owned statement establishes the actual typed driver exception used by
	// the finite classifier; no permission/IO probe and no fabricated error code.
	_, actualSQL := a.Store.DB.Exec(`UPDATE plugin_organization SET revision=revision WHERE owner_id=1`)
	var constraint *sqlite.Error
	if !errors.As(actualSQL, &constraint) || constraint.Code() != 1811 {
		t.Fatal("owned SQLite ABORT exception missing", actualSQL)
	}
	ordinary := &originalCloneFolderSQLError{cause: actualSQL}
	if message, ok := originalCloneFolderFailure(ctx, ordinary); !ok || message != "处理插件文件夹时出错：SQLite 错误（代码1811）" {
		t.Fatal("typed ordinary mapping", message, ok)
	}
	for _, denied := range []error{
		actualSQL,
		bad("INVALID_PLUGIN_FOLDER", "outside helper"),
		&originalCloneFolderValueError{cause: bad("PLUGIN_REVISION_CONFLICT", "owned CAS")},
		&originalCloneFolderSQLError{cause: context.Canceled},
		&originalCloneFolderSQLError{cause: context.DeadlineExceeded},
		&originalCloneFolderSQLError{cause: os.ErrPermission},
		&originalCloneFolderSQLError{cause: errors.New("owned unknown platform error")},
		&originalCloneFolderSQLError{cause: errors.Join(actualSQL, os.ErrPermission)},
		errors.Join(ordinary, context.Canceled),
	} {
		if _, ok := originalCloneFolderFailure(ctx, denied); ok {
			t.Fatalf("nonlocal/platform/joined error swallowed: %T", denied)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := originalCloneFolderFailure(canceled, ordinary); ok {
		t.Fatal("current cancellation lost to ordinary SQL classification")
	}
	if err = a.addOriginalCloneToFolder(canceled, id, id+"canceled"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled folder operation was admitted", err)
	}
	// Static projections of the complete pinned helper's ordinary JSON `in`
	// rules, not new source observations or additional HTTP/worker scenarios.
	for _, row := range []struct {
		raw                                    string
		source, target, appendable, shapeError bool
	}{
		{`{"plugins":"prefix-source-suffix"}`, true, false, false, false},
		{`{"plugins":"prefix-sourcecopy-suffix"}`, true, true, false, false},
		{`{"plugins":{"source":0}}`, true, false, false, false},
		{`{"plugins":{"source":0,"sourcecopy":false}}`, true, true, false, false},
		{`{"plugins":null}`, false, false, false, true},
		{`{"plugins":false}`, false, false, false, true},
		{`{"plugins":12}`, false, false, false, true},
	} {
		got, err := readOriginalCloneFolderMembership(json.RawMessage(row.raw), "source", "sourcecopy")
		_, ordinary := originalCloneFolderFailure(ctx, err)
		if got.source != row.source || got.target != row.target || got.appendable != row.appendable || ordinary != row.shapeError || (!row.shapeError && err != nil) {
			t.Fatal("source-private JSON membership", row.raw, got, err)
		}
	}

	pids := map[int]bool{beforeWorker.PID: true}
	clone := func(suffix string) {
		t.Helper()
		target := id + plugins.OriginalPluginLower(suffix)
		body, _ := json.Marshal(PluginCloneRequest{Suffix: suffix})
		r := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/"+id+"/clone", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		var reply struct {
			Data struct {
				Created          bool
				InstanceID       string `json:"instance_id"`
				SourceInstanceID string `json:"source_instance_id"`
			}
		}
		if json.Unmarshal(r.Body.Bytes(), &reply) != nil || !reply.Data.Created || reply.Data.InstanceID != target || reply.Data.SourceInstanceID != id {
			t.Fatal("actual clone success identity", r.Body.String())
		}
		a.wg.Wait()
		object := originalBaseSDKLoaded(t, a, target)
		observed := ownedModuleRead(t, object)
		if !object.external || observed.PID <= 0 || pids[observed.PID] || observed.Calls["init"] != 1 || object.manifest.PluginID != source.manifest.PluginID || object.manifest.Version != source.manifest.Version {
			t.Fatal("clone did not retain actual independent SDK initializer", target, observed.PID, observed.Calls["init"])
		}
		pids[observed.PID] = true
		meta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, target)
		if err != nil || meta.SourceInstanceID != id || meta.Name != source.manifest.Name {
			t.Fatal("clone metadata not retained after folder phase", target, meta, err)
		}
		installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), target, validatePluginArtifactContract)
		if err != nil || installed.SHA256 != code.SHA256 || installed.Manifest.PluginID != code.Manifest.PluginID {
			t.Fatal("folder failure changed copied executable", target, err)
		}
		config, err := a.pluginStore.Config(ctx, target)
		if err != nil || !originalBaseJSONEqual(config.Value, json.RawMessage(`{"enabled":false,"enable":false,"marker":"owned-folder","modules":{}}`)) {
			t.Fatal("folder failure changed clone configuration copy", target, err)
		}
	}
	check := func(store *PluginStore, disk bool, base PluginOrganization, members []string, revision int64) {
		t.Helper()
		var got PluginOrganization
		var err error
		if disk {
			got, err = store.organization(ctx, store.store.DB)
		} else {
			got, err = store.Organization(ctx)
		}
		if err != nil || got.Revision != revision || !bytes.Equal(got.Order, base.Order) || !slices.Equal(originalFolderKeys(got), originalFolderKeys(base)) {
			t.Fatal("folder layer revision/order", disk, got.Revision, revision, err)
		}
		want := cloneOriginalFolders(base.Folders)
		var first map[string]json.RawMessage
		if json.Unmarshal(want["z-first"], &first) != nil {
			t.Fatal("fixture object")
		}
		first["plugins"], _ = json.Marshal(members)
		want["z-first"], _ = json.Marshal(first)
		if !originalBaseJSONEqual(mustCleanSeedJSON(got.Folders), mustCleanSeedJSON(want)) {
			t.Fatal("folder contents or opaque fields changed", disk, string(mustCleanSeedJSON(got.Folders)))
		}
	}

	// A normal API can predeclare a not-yet-created clone ID. Source set still
	// deepcopies that equal value, but must not attempt the failing SQL write.
	a.pluginStore.folders.state.Lock()
	priorBacking := a.pluginStore.folders.folders["z-first"]
	a.pluginStore.folders.state.Unlock()
	clone("Reserved")
	a.pluginStore.folders.state.Lock()
	nextBacking := a.pluginStore.folders.folders["z-first"]
	a.pluginStore.folders.state.Unlock()
	if len(priorBacking) == 0 || len(nextBacking) == 0 || &priorBacking[0] == &nextBacking[0] || !bytes.Equal(priorBacking, nextBacking) {
		t.Fatal("predeclared-member synchronous set did not publish an equal deepcopy")
	}
	check(a.pluginStore, false, initial, []string{id, reserved}, initial.Revision)
	check(a.pluginStore, true, initial, []string{id, reserved}, initial.Revision)

	clone("Failed")
	check(a.pluginStore, false, initial, []string{id, reserved, failed}, initial.Revision)
	check(a.pluginStore, true, initial, []string{id, reserved}, initial.Revision)
	cold, err := NewPluginStore(ctx, a.Store)
	if err != nil {
		t.Fatal(err)
	}
	// Fresh DAO/cache over the same owned DB, not a claimed new host process.
	check(cold, false, initial, []string{id, reserved}, initial.Revision)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_clone_folder_fail`); err != nil {
		t.Fatal(err)
	}
	clone("Recovered")
	check(a.pluginStore, false, initial, []string{id, reserved, failed, recovered}, initial.Revision+1)
	check(a.pluginStore, true, initial, []string{id, reserved, failed, recovered}, initial.Revision+1)
	check(cold, false, initial, []string{id, reserved, failed, recovered}, initial.Revision+1)

	// JSON-valid malformed membership is admitted by the ordinary folder API.
	// Only the source folder helper's own shape error is caught; no global
	// INVALID_PLUGIN_FOLDER exception exemption is introduced.
	malformed := setFolders(`{"bad-first":{"plugins":null},"z-first":{"plugins":["` + id + `"]}}`)
	clone("Malformed")
	for _, disk := range []bool{false, true} {
		var got PluginOrganization
		if disk {
			got, err = a.pluginStore.organization(ctx, a.Store.DB)
		} else {
			got, err = a.pluginStore.Organization(ctx)
		}
		if err != nil || got.Revision != malformed.Revision || !originalBaseJSONEqual(mustCleanSeedJSON(got.Folders), mustCleanSeedJSON(malformed.Folders)) || !bytes.Equal(got.Order, malformed.Order) {
			t.Fatal("malformed source folder exception changed storage", disk, err)
		}
	}
	// A direct private folder-stage check, not a claimed fifth SDK clone: the
	// original selected empty key is falsy and must stop before the later match.
	standalone := setFolders(`{"":{"plugins":["` + id + `"]},"later":["` + id + `"]}`)
	if err = a.addOriginalCloneToFolder(ctx, id, id+"standalone"); err != nil {
		t.Fatal("no-folder source stage", err)
	}
	for _, disk := range []bool{false, true} {
		var got PluginOrganization
		if disk {
			got, err = a.pluginStore.organization(ctx, a.Store.DB)
		} else {
			got, err = a.pluginStore.Organization(ctx)
		}
		if err != nil || got.Revision != standalone.Revision || !originalBaseJSONEqual(mustCleanSeedJSON(got.Folders), mustCleanSeedJSON(standalone.Folders)) {
			t.Fatal("empty selected key continued to later folder", disk, err)
		}
	}
	// The real logger assigns Seq when enqueuing. This checks producer order,
	// not wall-time sorting or a callback that fabricates log observations.
	wantEvents := []string{
		"clone_folder_added:已将分身插件 " + failed + " 添加到文件夹 'z-first' 中",
		"clone_folder_failed:处理插件文件夹时出错：SQLite 错误（代码1811）",
		"clone_folder_added:已将分身插件 " + recovered + " 添加到文件夹 'z-first' 中",
		"clone_folder_failed:处理插件文件夹时出错：文件夹成员格式无效",
		"clone_folder_independent:原插件 " + id + " 不在任何文件夹中，分身插件 " + id + "standalone 将保持独立",
	}
	var gotEvents []string
	var lastSequence uint64
	a.logs.mu.Lock()
	for _, entry := range a.logs.ring {
		var row map[string]any
		if json.Unmarshal(entry.Data, &row) != nil {
			continue
		}
		event, _ := row["event"].(string)
		if !strings.HasPrefix(event, "clone_folder_") {
			continue
		}
		level := "INFO"
		if event == "clone_folder_failed" {
			level = "ERROR"
		}
		if row["level"] != level {
			a.logs.mu.Unlock()
			t.Fatal("folder log severity", event, row["level"])
		}
		if entry.Seq <= lastSequence {
			a.logs.mu.Unlock()
			t.Fatal("non-increasing real log sequence")
		}
		lastSequence = entry.Seq
		message, _ := row["message"].(string)
		gotEvents = append(gotEvents, event+":"+message)
	}
	a.logs.mu.Unlock()
	if !slices.Equal(gotEvents, wantEvents) {
		t.Fatal("actual folder INFO/ERROR sequence; existing member must not log append", gotEvents)
	}
	page, err := a.queryLogs(ctx, map[string]string{"module": "plugins", "from": "0001-01-01T00:00:00Z", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	wantLogs := map[string]int{"处理插件文件夹时出错：SQLite 错误（代码1811）": 1, "处理插件文件夹时出错：文件夹成员格式无效": 1}
	for _, row := range page.Entries {
		if row["event"] != "clone_folder_failed" {
			continue
		}
		message, _ := row["message"].(string)
		if row["level"] != "ERROR" || strings.Contains(message, "owned clone SQL sentinel") || wantLogs[message] != 1 {
			t.Fatal("unsafe or unexpected folder diagnostic", row)
		}
		wantLogs[message]--
	}
	for _, remaining := range wantLogs {
		if remaining != 0 {
			t.Fatal("missing real folder exception log")
		}
	}
	afterWorker := ownedModuleRead(t, source)
	afterConfig, err := a.pluginStore.Config(ctx, id)
	if err != nil || afterWorker.PID != beforeWorker.PID || afterWorker.Calls["init"] != beforeWorker.Calls["init"] || afterConfig.Revision != beforeConfig.Revision || !bytes.Equal(afterConfig.Value, beforeConfig.Value) {
		t.Fatal("folder handling mutated source worker/config", err)
	}
}
