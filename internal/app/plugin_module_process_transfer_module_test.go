package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionTransferModule(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID {
		t.Fatal("actual module transfer worker", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Transfer"})
	clone := manifest.PluginID + "transfer"
	base := t.TempDir()
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	if os.MkdirAll(source, 0750) != nil || os.MkdirAll(target, 0750) != nil {
		t.Fatal("owned module directories")
	}
	var settings struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settings) != nil {
		t.Fatal("actual settings")
	}
	settings.Data.AllowedRoots = []string{source}
	settings.Data.Roots = []Root{{ID: "owned-module-transfer-root", Kind: "mixed", Path: target}}
	request("PUT", "/api/settings", settings.Data, 200)
	snapshot := func(id string) ownedModuleSnapshot {
		t.Helper()
		var out ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil || out.PID <= 0 {
			t.Fatal("actual module snapshot")
		}
		return out
	}
	fullMedia := func(identity int64, title string) json.RawMessage {
		wire, _ := json.Marshal(map[string]any{"id": identity, "title": title, "original_title": title, "release_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}})
		full, err := sourceTMDBMediaInfo(wire, "movie", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		return full
	}
	config := func(identity int64, title, method, mode string, result any) map[string]any {
		return map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": method}, "media_result": fullMedia(identity, title), "result": result, "storage_root": source, "storage_mode": "effect", "transfer_root": target, "transfer_mode": mode}
	}
	set := func(id string, cfg any) { success("PUT", "/api/plugins/"+id, cfg) }
	preview := func(name string, identity int64, mode string) ImportPlan {
		path := filepath.Join(source, name+".2024.mkv")
		if err := os.WriteFile(path, []byte("owned real module transfer"), 0640); err != nil {
			t.Fatal(err)
		}
		var out struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: mode, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}}, 200), &out) != nil || len(out.Data.Files) != 1 || out.Data.Files[0].Status != "ready" {
			t.Fatal("actual module preview", out.Data)
		}
		return out.Data
	}
	run := func(plan ImportPlan, want bool, mainSuccess ...bool) ImportPlan {
		wantMain := want
		if len(mainSuccess) > 0 {
			wantMain = mainSuccess[0]
		}
		var submitted struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/import/execute", map[string]any{"plan_id": plan.ID}, 200), &submitted) != nil || submitted.Data.ID == "" {
			t.Fatal("actual module execute")
		}
		var current ImportPlan
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			var list struct{ Data []ImportPlan }
			if json.Unmarshal(request("GET", "/api/import/plans", nil, 200), &list) != nil {
				t.Fatal("actual plan status")
			}
			for _, p := range list.Data {
				if p.ID == plan.ID {
					current = p
				}
			}
			if len(current.Files) == 1 && (current.Files[0].Status == "imported" || current.Files[0].Status == "error") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(current.Files) != 1 || (current.Files[0].Status == "imported") != wantMain || !wantMain && current.Files[0].Status != "error" {
			t.Fatal("actual module outcome", current)
		}
		if want {
			terminal := false
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				var detail struct {
					Data struct{ Job struct{ State string } }
				}
				if json.Unmarshal(request("GET", "/api/jobs/"+submitted.Data.ID, nil, 200), &detail) != nil {
					t.Fatal("actual successful job state")
				}
				if detail.Data.Job.State == "succeeded" {
					terminal = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !terminal {
				t.Fatal("successful publication has not completed its actual host job")
			}
		}
		if !want {
			terminal := false
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				var detail struct {
					Data struct{ Job struct{ State string } }
				}
				if json.Unmarshal(request("GET", "/api/jobs/"+submitted.Data.ID, nil, 200), &detail) != nil {
					t.Fatal("actual failed job state")
				}
				if detail.Data.Job.State == "failed" || detail.Data.Job.State == "needs_review" {
					terminal = true
					break
				}
				if detail.Data.Job.State == "retry_wait" {
					request("POST", "/api/jobs/"+submitted.Data.ID+"/cancel", nil, 200)
					terminal = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !terminal {
				t.Fatal("failed module job did not reach a stable terminal state")
			}
		}
		return current
	}
	model := map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "private": "owned-private-process-result"}}
	evidence := []any{}
	extraHistories := []transferHistoryItem{}
	waitExtra := func(jobID string, want string) manualOrganizeOutput {
		t.Helper()
		var detail struct {
			Data struct {
				Job      Job
				Transfer manualOrganizeOutput
			}
		}
		for until := time.Now().Add(15 * time.Second); time.Now().Before(until); {
			if json.Unmarshal(request("GET", "/api/jobs/"+jobID, nil, 200), &detail) != nil {
				t.Fatal("extra actual job")
			}
			if detail.Data.Job.State == want {
				return detail.Data.Transfer
			}
			if detail.Data.Job.State == "needs_review" || detail.Data.Job.State == "failed" && want != "failed" {
				t.Fatal("unexpected auxiliary terminal state", detail)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("auxiliary job did not finish", detail)
		return manualOrganizeOutput{}
	}
	replaceExtra := func(prior transferHistoryItem, identity int64, name string) transferHistoryItem {
		t.Helper()
		incoming := filepath.Join(source, "replacement", filepath.Base(prior.Source))
		if err := os.MkdirAll(filepath.Dir(incoming), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(incoming, []byte("owned actual replacement extra"), 0640); err != nil {
			t.Fatal(err)
		}
		set(manifest.PluginID, config(identity, name, "module_execute", "", nil))
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		bs, bc := snapshot(manifest.PluginID), snapshot(clone)
		input := manualOrganizeRequest{Paths: []string{incoming}, MediaType: "movie", TMDBID: identity, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)}
		var submitted struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &submitted) != nil || submitted.Data.Submitted != 1 {
			t.Fatal("replacement submit", submitted)
		}
		result := waitExtra(submitted.Data.Items[0].JobID, "succeeded")
		if result.Total != 1 || result.Failed != 0 {
			t.Fatal("independent replacement result", result)
		}
		var history struct {
			Data struct{ Items []transferHistoryItem }
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &history) != nil {
			t.Fatal("replacement history")
		}
		var next transferHistoryItem
		for _, item := range history.Data.Items {
			if item.Source == incoming {
				next = item
			}
		}
		if next.Status != "success" || next.Target != prior.Target {
			t.Fatal("replacement changed target", next, prior)
		}
		old, e1 := os.Stat(prior.Source)
		src, e2 := os.Stat(incoming)
		dst, e3 := os.Stat(next.Target)
		if e1 != nil || e2 != nil || e3 != nil || os.SameFile(old, dst) || !os.SameFile(src, dst) {
			t.Fatal("actual extra replacement without changing original", e1, e2, e3)
		}
		as, ac := snapshot(manifest.PluginID), snapshot(clone)
		if as.Calls["module_execute"]-bs.Calls["module_execute"] != 1 || ac.Calls["module_execute"]-bc.Calls["module_execute"] != 1 {
			t.Fatal("independent replacement dispatch", bs, as, bc, ac)
		}
		evidence = append(evidence, map[string]any{"name": "ExtraReplacement" + name, "prior": prior, "history": next, "result": result, "before_source": bs, "after_source": as, "before_clone": bc, "after_clone": ac})
		return next
	}
	for i, ext := range []string{".zh.srt", ".flac", ".mkv"} {
		name := []string{"SingleExtraSubtitle", "SingleExtraAudio", "SingleMainRetry"}[i]
		identity := int64(90009810 + i)
		main := filepath.Join(source, name+".2024.mkv")
		extra := filepath.Join(source, name+".2024"+ext)
		for _, p := range []string{main, extra} {
			if e := os.WriteFile(p, []byte("owned single extra process"), 0640); e != nil {
				t.Fatal(e)
			}
		}
		cfg := config(identity, name, "module_execute", "", nil)
		cfg["modules"].(map[string]string)["obtain_images"] = "module_images"
		cfg["image_mode"] = "false"
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		input := manualOrganizeRequest{MinFilesize: 10, Paths: []string{extra}, MediaType: "movie", TMDBID: identity, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
		if i == 2 {
			input.MinFilesize = 0
		}
		var preview struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &preview) != nil || preview.Data.Total != 1 || preview.Data.Failed != 0 || preview.Data.Items[0].Source != extra {
			t.Fatal("single extra preview only selected", preview)
		}
		if _, e := os.Stat(preview.Data.Items[0].Target); !os.IsNotExist(e) {
			t.Fatal("preview wrote extra", e)
		}
		if i != 1 {
			cfg["transfer_results"] = map[string]any{extra: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned single-extra rejection"}}}
			set(manifest.PluginID, cfg)
		}
		before, bc := snapshot(manifest.PluginID), snapshot(clone)
		input.Preview = false
		var submitted struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &submitted) != nil || submitted.Data.Submitted != 1 {
			t.Fatal("extra actual submission", submitted)
		}
		want := "succeeded"
		if i != 1 {
			want = "failed"
		}
		result := waitExtra(submitted.Data.Items[0].JobID, want)
		if result.Total != 1 || result.Failed != map[bool]int{true: 1, false: 0}[i != 1] {
			t.Fatal("single-file result", result)
		}
		var histories struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || histories.Data.Total != 1 {
			t.Fatal("single independent history", histories)
		}
		if i != 1 {
			if !histories.Data.Items[0].CanRetry {
				t.Fatal("failed extra retry unavailable", histories)
			}
			cfg["transfer_results"] = map[string]any{}
			set(manifest.PluginID, cfg)
			var retried struct{ Data Job }
			if json.Unmarshal(request("POST", "/api/history/transfers/"+histories.Data.Items[0].ID+"/retry", map[string]any{"confirm": true}, 200), &retried) != nil || retried.Data.ID == "" {
				t.Fatal("actual independent retry", retried)
			}
			result = waitExtra(retried.Data.ID, "succeeded")
			if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || histories.Data.Total != 1 {
				t.Fatal("retry stable history", histories)
			}
		}
		item := histories.Data.Items[0]
		if item.Status != "success" || item.Source != extra {
			t.Fatal(item)
		}
		a, e1 := os.Stat(extra)
		b, e2 := os.Stat(item.Target)
		if e1 != nil || e2 != nil || !os.SameFile(a, b) {
			t.Fatal("actual independent hardlink", e1, e2)
		}
		if _, e := os.Stat(filepath.Join(target, name+" (2024)", name+" (2024).mkv")); i < 2 && !os.IsNotExist(e) {
			t.Fatal("single extra copied main", e)
		}
		after, ac := snapshot(manifest.PluginID), snapshot(clone)
		wantCalls := 1
		if i != 1 {
			wantCalls = 2
		}
		if after.Calls["module_execute"]-before.Calls["module_execute"] != wantCalls || ac.Calls["module_execute"]-bc.Calls["module_execute"] != 1 {
			t.Fatal("actual source rejection/native fallback only selected extra", before.Calls, after.Calls, bc.Calls, ac.Calls)
		}
		if i < 2 {
			extraHistories = append(extraHistories, replaceExtra(item, identity, name))
		} else {
			extraHistories = append(extraHistories, item)
		}
		evidence = append(evidence, map[string]any{"name": name, "preview": preview.Data, "result": result, "history": item, "before_source": before, "after_source": after, "before_clone": bc, "after_clone": ac})
	}
	for i, ext := range []string{".mkv", ".zh.srt", ".flac"} {
		name := []string{"AutomaticMain34", "AutomaticSubtitle34", "AutomaticAudio34"}[i]
		identity := int64(90009830 + i)
		path := filepath.Join(source, name+".2024"+ext)
		for _, p := range []string{path, filepath.Join(source, name+".2024.mkv")} {
			if e := os.WriteFile(p, []byte("owned automatic recognition"), 0640); e != nil {
				t.Fatal(e)
			}
		}
		cfg := config(identity, name, "module_execute", "", nil)
		cfg["modules"].(map[string]string)["obtain_images"] = "module_images"
		cfg["image_mode"], cfg["media_result"] = "false", nil
		set(clone, config(identity, name, "module_execute", "", nil))
		input := manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)}
		if i == 0 {
			cfg["media_result"] = false
			set(manifest.PluginID, cfg)
			bs, bc := snapshot(manifest.PluginID), snapshot(clone)
			var failed struct{ Data manualOrganizeOutput }
			if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &failed) != nil || failed.Data.Failed != 1 || failed.Data.Submitted != 0 {
				t.Fatal("automatic missing recognition", failed)
			}
			as, ac := snapshot(manifest.PluginID), snapshot(clone)
			if as.Calls["module_search_media"]-bs.Calls["module_search_media"] != 1 || as.Calls["module_images"] != bs.Calls["module_images"] || as.Calls["module_execute"] != bs.Calls["module_execute"] || ac.Calls["module_search_media"] != bc.Calls["module_search_media"] {
				t.Fatal("false stops recognition and all later work", bs, as, bc, ac)
			}
			evidence = append(evidence, map[string]any{"name": "AutomaticMissing34", "result": failed.Data, "before_source": bs, "after_source": as, "before_clone": bc, "after_clone": ac})
		}
		cfg["media_result"] = nil
		set(manifest.PluginID, cfg)
		bs, bc := snapshot(manifest.PluginID), snapshot(clone)
		input.Preview = true
		var preview struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &preview) != nil || preview.Data.Failed != 0 || preview.Data.Total != 1 {
			t.Fatal("automatic preview", preview)
		}
		if _, e := os.Stat(preview.Data.Items[0].Target); !os.IsNotExist(e) {
			t.Fatal("automatic preview wrote target", e)
		}
		input.Preview = false
		var submitted struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &submitted) != nil || submitted.Data.Submitted != 1 {
			t.Fatal("automatic submit", submitted)
		}
		result := waitExtra(submitted.Data.Items[0].JobID, "succeeded")
		var histories struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || histories.Data.Total != 1 || histories.Data.Items[0].Status != "success" {
			t.Fatal("automatic stable history", histories)
		}
		item := histories.Data.Items[0]
		src, e1 := os.Stat(path)
		dst, e2 := os.Stat(item.Target)
		if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
			t.Fatal("automatic physical hardlink", e1, e2)
		}
		if i > 0 {
			if _, e := os.Stat(filepath.Join(target, name+" (2024)", name+" (2024).mkv")); !os.IsNotExist(e) {
				t.Fatal("independent extra must not publish main", e)
			}
		}
		as, ac := snapshot(manifest.PluginID), snapshot(clone)
		if as.Calls["module_search_media"]-bs.Calls["module_search_media"] != 2 || ac.Calls["module_search_media"]-bc.Calls["module_search_media"] != 2 || as.Calls["module_images"]-bs.Calls["module_images"] != 2 || as.Calls["module_execute"]-bs.Calls["module_execute"] != 2 || ac.Calls["module_execute"]-bc.Calls["module_execute"] != 2 {
			t.Fatal("preview and actual each recognize/image/transfer, no duplicate ID lookup", bs, as, bc, ac)
		}
		extraHistories = append(extraHistories, item)
		evidence = append(evidence, map[string]any{"name": name, "preview": preview.Data, "result": result, "history": item, "before_source": bs, "after_source": as, "before_clone": bc, "after_clone": ac})
	}
	{
		name := "HistoryMainReorganize35"
		identity := int64(90009850)
		main := filepath.Join(source, name+".2024.mkv")
		for _, ext := range []string{".mkv", ".srt", ".flac"} {
			if e := os.WriteFile(filepath.Join(source, name+".2024"+ext), []byte("owned history reorganization"), 0640); e != nil {
				t.Fatal(e)
			}
		}
		cfg := config(identity, name, "module_execute", "", nil)
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		var prepared struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: main}}}, 200), &prepared) != nil {
			t.Fatal("history setup")
		}
		run(prepared.Data, true)
		var histories struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || histories.Data.Total != 3 {
			t.Fatal("history setup result", histories)
		}
		var selected transferHistoryItem
		protected := map[string]os.FileInfo{}
		for _, item := range histories.Data.Items {
			if item.Source == main {
				selected = item
			} else {
				st, e := os.Stat(item.Target)
				if e != nil {
					t.Fatal(e)
				}
				protected[item.Target] = st
			}
		}
		dest := filepath.Join(target, "history-main-again")
		if e := os.Mkdir(dest, 0750); e != nil {
			t.Fatal(e)
		}
		input := manualOrganizeRequest{HistoryIDs: []string{selected.ID}, FromHistory: true, TargetPath: dest, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
		var previewed struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &previewed) != nil || previewed.Data.Failed != 0 || previewed.Data.Total != 3 {
			t.Fatal("history main preview", previewed)
		}
		if _, e := os.Stat(selected.Target); e != nil {
			t.Fatal("preview removed old main", e)
		}
		cfg["modules"].(map[string]string)["delete_file"] = "module_storage_delete"
		cfg["storage_root"], cfg["storage_mode"], cfg["result"] = base, "no_effect", false
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": base, "storage_mode": "effect", "result": true})
		bs, bc := snapshot(manifest.PluginID), snapshot(clone)
		input.Preview = false
		var failed struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &failed) != nil || failed.Data.Failed != 1 || failed.Data.Submitted != 0 {
			t.Fatal("manual false deletion must abort", failed)
		}
		as, ac := snapshot(manifest.PluginID), snapshot(clone)
		if as.Calls["module_storage_delete"]-bs.Calls["module_storage_delete"] != 1 || ac.Calls["module_storage_delete"] != bc.Calls["module_storage_delete"] || as.Calls["module_execute"] != bs.Calls["module_execute"] {
			t.Fatal("manual false terminal before transfer", bs, as, bc, ac)
		}
		if _, e := os.Stat(selected.Target); e != nil {
			t.Fatal("rejected deletion retained old main", e)
		}
		cfg["result"] = nil
		delete(cfg["modules"].(map[string]string), "transfer")
		set(manifest.PluginID, cfg)
		var submitted struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", input, 200), &submitted) != nil || submitted.Data.Submitted != 1 {
			t.Fatal("manual recovered submit", submitted)
		}
		result := waitExtra(submitted.Data.Items[0].JobID, "succeeded")
		if result.Total != 3 || result.Failed != 0 {
			t.Fatal("manual recovered three physical files", result)
		}
		if _, e := os.Stat(selected.Target); !os.IsNotExist(e) {
			t.Fatal("old main was not deleted", e)
		}
		for path, stamp := range protected {
			current, e := os.Stat(path)
			if e != nil || !os.SameFile(stamp, current) {
				t.Fatal("unselected old related file changed", path, e)
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || histories.Data.Total != 3 {
			t.Fatal("stable three histories", histories)
		}
		for _, item := range histories.Data.Items {
			if item.Status != "success" || !within(dest, item.Target) || item.Source == main && item.ID != selected.ID {
				t.Fatal("stable actual history", item)
			}
			src, e1 := os.Stat(item.Source)
			dst, e2 := os.Stat(item.Target)
			if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
				t.Fatal("new history physical hardlinks", e1, e2)
			}
			extraHistories = append(extraHistories, item)
		}
		after, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		if after.Calls["module_storage_delete"]-as.Calls["module_storage_delete"] != 1 || afterClone.Calls["module_storage_delete"]-ac.Calls["module_storage_delete"] != 1 {
			t.Fatal("source None then clone deletes selected main only", as, after, ac, afterClone)
		}
		evidence = append(evidence, map[string]any{"name": name, "preview": previewed.Data, "failed": failed.Data, "result": result, "old_main": selected, "old_related": protected, "history": histories.Data.Items, "before_source": bs, "after_false_source": as, "after_source": after, "before_clone": bc, "after_false_clone": ac, "after_clone": afterClone})
	}
	partialPlans := []ImportPlan{}
	for i, failure := range []string{"main", "subtitle"} {
		name := "PartialTask" + failure
		identity := int64(90001880 + i)
		main, sub, audio := filepath.Join(source, name+".2024.mkv"), filepath.Join(source, name+".2024.srt"), filepath.Join(source, name+".2024.flac")
		for _, path := range []string{sub, audio} {
			if err := os.WriteFile(path, []byte("owned partial extra"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		set(manifest.PluginID, config(identity, name, "module_execute", "", nil))
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		plan := preview(name, identity, "hardlink")
		rejected := main
		if failure == "subtitle" {
			rejected = sub
		}
		cfg := config(identity, name, "module_execute", "", nil)
		cfg["transfer_results"] = map[string]any{rejected: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned per-file rejection"}}}
		set(manifest.PluginID, cfg)
		before, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		actual := run(plan, false, failure != "main")
		after, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		if after.Calls["module_execute"]-before.Calls["module_execute"] != 3 || afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != 2 {
			t.Fatal("actual source reject / None to clone must continue all three tasks", name, before, after, beforeClone, afterClone)
		}
		var history struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &history) != nil || history.Data.Total != 3 {
			t.Fatal("actual partial task histories", name, history)
		}
		succeeded, failed := 0, 0
		for _, item := range history.Data.Items {
			if item.Source == rejected {
				if item.Status != "failed" {
					t.Fatal("rejected task history", item)
				}
				failed++
				continue
			}
			if item.Status != "success" {
				t.Fatal("later task did not succeed", item)
			}
			src, e1 := os.Stat(item.Source)
			dst, e2 := os.Stat(item.Target)
			if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
				t.Fatal("partial actual hardlink", item, e1, e2)
			}
			succeeded++
		}
		if succeeded != 2 || failed != 1 {
			t.Fatal("source partial counts", succeeded, failed)
		}
		if failure == "main" {
			for _, item := range history.Data.Items {
				if item.Source == sub {
					extraHistories = append(extraHistories, replaceExtra(item, identity, name))
				}
			}
		}
		partialPlans = append(partialPlans, actual)
		evidence = append(evidence, map[string]any{"name": name, "plan": actual, "history": history.Data, "before_source": before, "after_source": after, "before_clone": beforeClone, "after_clone": afterClone})
	}
	for i, kind := range []string{"subtitle", "audio"} {
		for j, outcome := range []string{"native", "false", "clone"} {
			name := "Aux" + kind + outcome
			identity := int64(90001820 + i*3 + j)
			ext := ".srt"
			if kind == "audio" {
				ext = ".flac"
			}
			aux := filepath.Join(source, name+".2024"+ext)
			if err := os.WriteFile(aux, []byte("owned independent auxiliary"), 0640); err != nil {
				t.Fatal(err)
			}
			set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": fullMedia(identity, name)})
			set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
			plan := preview(name, identity, "hardlink")
			cfg := config(identity, name, "module_execute", "", nil)
			if outcome == "false" {
				cfg["transfer_results"] = map[string]any{aux: false}
			}
			auxTarget := strings.TrimSuffix(plan.Files[0].Target, filepath.Ext(plan.Files[0].Target)) + ext
			if outcome == "clone" {
				mainTarget := filepath.Join(target, "Independent auxiliary clone", name+".mkv")
				auxTarget = filepath.Join(target, "Independent auxiliary clone", name+ext)
				makeResult := func(path string) any {
					return map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "target_item": originalTransferPreviewItem(path, "file", 26)}}
				}
				other := config(identity, name, "module_transfer", "", nil)
				other["modules"] = map[string]string{"transfer": "module_transfer"}
				other["transfer_targets"] = map[string]string{plan.Files[0].Source: mainTarget, aux: auxTarget}
				other["transfer_results"] = map[string]any{plan.Files[0].Source: makeResult(mainTarget), aux: makeResult(auxTarget)}
				set(clone, other)
			}
			set(manifest.PluginID, cfg)
			beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
			actual := run(plan, outcome != "false", true)
			afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
			if afterSource.Calls["module_execute"]-beforeSource.Calls["module_execute"] != 2 {
				t.Fatal("each main/auxiliary enters high-level module", name)
			}
			chainCalls := map[string]int{"native": 8, "false": 6, "clone": 4}[outcome]
			if afterSource.Calls["transfer_chain_event"]-beforeSource.Calls["transfer_chain_event"] != chainCalls {
				t.Fatal("each main/auxiliary storage then native-only naming", name, afterSource.Calls, beforeSource.Calls)
			}
			cloneCalls := map[string]int{"native": 2, "false": 1, "clone": 2}[outcome]
			cloneMethod := "module_execute"
			if outcome == "clone" {
				cloneMethod = "module_transfer"
			}
			if afterClone.Calls[cloneMethod]-beforeClone.Calls[cloneMethod] != cloneCalls {
				t.Fatal("independent auxiliary clone/terminal branch", name)
			}
			src, err := os.Stat(aux)
			dst, targetErr := os.Stat(auxTarget)
			if err != nil || outcome != "false" && (targetErr != nil || !os.SameFile(src, dst)) || outcome == "false" && !os.IsNotExist(targetErr) {
				t.Fatal("real auxiliary selected hardlink/veto", name, err, targetErr)
			}
			evidence = append(evidence, map[string]any{"name": name, "plan": actual, "auxiliary_source": aux, "auxiliary_target": auxTarget, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
		}
	}
	for index, sample := range []struct {
		name, collection string
		count            int
	}{{"CollectionParentFalse", "parent_false", 1}, {"CollectionListFalse", "list_false", 1}, {"CollectionTupleAudio", "tuple_audio", 2}, {"CollectionDuplicateList", "duplicate_list", 3}, {"CollectionCloneTuple", "clone_tuple", 2}} {
		identity := int64(90001870 + index)
		input := filepath.Join(source, sample.name+".2024.mkv")
		sub := filepath.Join(source, sample.name+".2024.zh.srt")
		audio := filepath.Join(source, sample.name+".2024.flac")
		for _, path := range []string{input, sub, audio} {
			if err := os.WriteFile(path, []byte("owned collection preview"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		cfg := config(identity, sample.name, "module_execute", "", nil)
		cfg["modules"].(map[string]string)["get_parent_item"] = "module_storage_parent"
		cfg["modules"].(map[string]string)["list_files"] = "module_storage_siblings"
		other := map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute", "get_parent_item": "module_storage_parent", "list_files": "module_storage_siblings"}, "result": nil}
		switch sample.collection {
		case "parent_false":
			cfg["storage_parent"] = false
		case "list_false":
			cfg["storage_siblings"] = false
		case "tuple_audio":
			cfg["storage_siblings"], _ = plugins.ModuleResultTuple(originalTransferPreviewItem(audio, "file", 24))
		case "duplicate_list":
			cfg["storage_siblings"] = []any{originalTransferPreviewItem(sub, "file", 24), originalTransferPreviewItem(sub, "file", 24)}
		case "clone_tuple":
			other["storage_siblings"], _ = plugins.ModuleResultTuple(originalTransferPreviewItem(sub, "file", 24))
		}
		set(manifest.PluginID, cfg)
		set(clone, other)
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		var out struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", manualOrganizeRequest{Paths: []string{input}, MediaType: "movie", TMDBID: identity, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}, 200), &out) != nil || out.Data.Total != sample.count || out.Data.Failed != 0 || out.Data.Submitted != 0 {
			t.Fatal("fixed sibling module terminal/list/None-to-clone behavior", sample.name, out.Data)
		}
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		listCalls := 1
		if sample.collection == "parent_false" {
			listCalls = 0
		}
		if afterSource.Calls["module_storage_parent"]-beforeSource.Calls["module_storage_parent"] != 1 || afterSource.Calls["module_storage_siblings"]-beforeSource.Calls["module_storage_siblings"] != listCalls || afterSource.Calls["module_execute"]-beforeSource.Calls["module_execute"] != sample.count || afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != sample.count {
			t.Fatal("actual independent collection + task dispatch counts", sample.name, beforeSource.Calls, afterSource.Calls, beforeClone.Calls, afterClone.Calls)
		}
		for _, row := range out.Data.Items {
			if sample.collection == "tuple_audio" && row.Source == sub || sample.collection == "clone_tuple" && row.Source == audio {
				t.Fatal("terminal tuple must not append native siblings", sample.name, row)
			}
			if row.Target == "" {
				t.Fatal("concrete preview target missing", row)
			}
			if _, err := os.Stat(row.Target); !os.IsNotExist(err) {
				t.Fatal("collection preview published a file", row.Target, err)
			}
		}
		evidence = append(evidence, map[string]any{"name": sample.name, "plan": out.Data, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	}
	for index := 0; index < 8; index++ {
		kind := "subtitle"
		ext := ".srt"
		if index >= 4 {
			kind, ext = "audio", ".flac"
		}
		deleteSource, deleteTarget := index%4 >= 2, index%2 == 1
		name := "IndependentHistory" + kind + string(rune('A'+index))
		identity := int64(90009000 + index)
		main := filepath.Join(source, name+".2024.mkv")
		aux := filepath.Join(source, name+".2024"+ext)
		for _, path := range []string{main, aux} {
			if err := os.WriteFile(path, []byte("owned independent deletion"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		set(manifest.PluginID, config(identity, name, "module_execute", "", nil))
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		var plan struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: main}}}, 200), &plan) != nil || len(plan.Data.Files) != 1 || plan.Data.Files[0].Status != "ready" {
			t.Fatal("independent history preparation", name, plan.Data)
		}
		actual := run(plan.Data, true)
		var history struct {
			Data struct{ Items []transferHistoryItem }
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &history) != nil || len(history.Data.Items) != 2 {
			t.Fatal("independent histories before deletion", name, history)
		}
		var selected transferHistoryItem
		for _, item := range history.Data.Items {
			if item.Source == aux {
				selected = item
			}
		}
		if selected.ID == "" || selected.Status != "success" {
			t.Fatal("missing actual auxiliary history", name, selected)
		}
		var deleted struct{ Data struct{ Deleted bool } }
		if json.Unmarshal(request("DELETE", "/api/history/transfers/"+selected.ID, historyDeleteRequest{Confirm: true, DeleteSource: deleteSource, DeleteTarget: deleteTarget, FileID: selected.executionFileID()}, 200), &deleted) != nil || !deleted.Data.Deleted {
			t.Fatal("actual history deletion response", name, deleted)
		}
		for path, deleted := range map[string]bool{aux: deleteSource, selected.Target: deleteTarget, main: false, actual.Files[0].Target: false} {
			if _, err := os.Stat(path); os.IsNotExist(err) != deleted {
				t.Fatal("actual independent file effect / protected video", name, path, deleted, err)
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &history) != nil || len(history.Data.Items) != 1 || history.Data.Items[0].Source != main {
			t.Fatal("deletion removed another history", name, history)
		}
		evidence = append(evidence, map[string]any{"name": name, "delete_source": deleteSource, "delete_target": deleteTarget, "selected": selected, "remaining": history.Data.Items})
	}
	for index, sample := range []struct {
		name                          string
		source, accepted, main, clone bool
	}{
		{name: "SourceFalse", source: true},
		{name: "SourceTrue", source: true, accepted: true},
		{name: "TargetFalse"},
		{name: "MainTargetTrue", accepted: true, main: true},
		{name: "TargetNoneClone", accepted: true, clone: true},
		{name: "SourceNoneClone", source: true, accepted: true, clone: true},
	} {
		identity := int64(90009500 + index)
		name := "StorageHistory" + sample.name
		mainPath := filepath.Join(source, name+".2024.mkv")
		subtitle := filepath.Join(source, name+".2024.srt")
		audio := filepath.Join(source, name+".2024.flac")
		for _, path := range []string{mainPath, subtitle, audio} {
			if err := os.WriteFile(path, []byte("owned storage history material"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		set(manifest.PluginID, config(identity, name, "module_execute", "", nil))
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		var previewed struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: mainPath}}}, 200), &previewed) != nil || len(previewed.Data.Files) != 1 || previewed.Data.Files[0].Status != "ready" {
			t.Fatal("ordinary storage history preview", previewed.Data)
		}
		run(previewed.Data, true)
		var histories struct {
			Data struct{ Items []transferHistoryItem }
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || len(histories.Data.Items) != 3 {
			t.Fatal("actual three histories", histories)
		}
		var selected transferHistoryItem
		protected := []string{mainPath, audio}
		for _, item := range histories.Data.Items {
			if item.Source == subtitle && !sample.main || item.Source == mainPath && sample.main {
				selected = item
			} else {
				protected = append(protected, item.Target)
			}
		}
		if sample.main {
			protected = append(protected, subtitle)
		}
		mode := "no_effect"
		if sample.accepted && !sample.clone {
			mode = "effect"
		}
		var result any = sample.accepted
		if sample.clone {
			result = nil
		}
		set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": base, "storage_mode": mode, "result": result})
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": base, "storage_mode": "effect", "result": true})
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		status := 200
		if sample.source && !sample.accepted {
			status = 400
		}
		response := request("DELETE", "/api/history/transfers/"+selected.ID, historyDeleteRequest{Confirm: true, DeleteSource: sample.source, DeleteTarget: !sample.source, FileID: selected.executionFileID()}, status)
		if status == 400 && !strings.Contains(string(response), "PLUGIN_FILE_OPERATION_REJECTED") {
			t.Fatal("source false must retain explicit failure", string(response))
		}
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		cloneCount := 0
		if sample.clone {
			cloneCount = 1
		}
		if afterSource.Calls["module_storage_delete"]-beforeSource.Calls["module_storage_delete"] != 1 || afterClone.Calls["module_storage_delete"]-beforeClone.Calls["module_storage_delete"] != cloneCount {
			t.Fatal("source terminal or None→clone ordinary dispatch", sample.name, beforeSource.Calls, afterSource.Calls, beforeClone.Calls, afterClone.Calls)
		}
		path := selected.Target
		if sample.source {
			path = selected.Source
		}
		if _, err := os.Stat(path); os.IsNotExist(err) != sample.accepted {
			t.Fatal("actual selected storage effect", path, sample.accepted, err)
		}
		for _, path := range protected {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("ordinary deletion touched another material", path, err)
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || len(histories.Data.Items) != map[bool]int{true: 2, false: 3}[status == 200] {
			t.Fatal("fixed history deletion branch", sample.name, histories)
		}
		evidence = append(evidence, map[string]any{"name": name, "selected": selected, "source": sample.source, "accepted": sample.accepted, "clone": sample.clone, "status": status, "remaining": histories.Data.Items, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	}
	for i, sample := range []struct {
		name, method, mode, worker string
		result                     any
		want, clone                bool
	}{{"OwnedSource", "module_transfer", "hardlink", "", model, true, false}, {"OwnedFalse", "module_execute", "hardlink", "", false, false, false}, {"OwnedDict", "module_execute", "hardlink", "", map[string]any{"success": true}, false, false}, {"OwnedClone", "module_execute", "hardlink", "", nil, true, true}, {"OwnedCopy", "module_transfer", "copy", "copy", model, true, false}, {"OwnedCorrupt", "module_transfer", "copy", "corrupt", model, false, false}, {"OwnedNativeFallback", "module_execute", "hardlink", "", nil, true, false}} {
		identity := int64(90001700 + i)
		cfg := config(identity, sample.name, sample.method, sample.worker, sample.result)
		empty := map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil}
		// These six cases exercise execution. Prepare their native plan with
		// recognition only; explicit preview dispatch has separate cases below.
		set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": fullMedia(identity, sample.name)})
		set(clone, empty)
		plan := preview(sample.name, identity, sample.mode)
		cfg["transfer_target"] = plan.Files[0].Target
		set(manifest.PluginID, cfg)
		if sample.clone {
			other := config(identity, sample.name, "module_transfer", sample.worker, model)
			other["modules"] = map[string]string{"transfer": "module_transfer"}
			other["transfer_target"] = plan.Files[0].Target
			set(clone, other)
		}
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		actual := run(plan, sample.want)
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		chainCalls := 2
		if sample.name == "OwnedNativeFallback" {
			chainCalls = 4
		}
		if afterSource.Calls["transfer_chain_event"]-beforeSource.Calls["transfer_chain_event"] != chainCalls {
			t.Fatal("storage selection then native-only naming", sample.name, beforeSource.Calls, afterSource.Calls)
		}
		if afterSource.Calls[sample.method]-beforeSource.Calls[sample.method] != 1 {
			t.Fatal("actual source module not invoked once")
		}
		expectedClone := 0
		if sample.clone {
			expectedClone = 1
		}
		if afterClone.Calls["module_transfer"]-beforeClone.Calls["module_transfer"] != expectedClone {
			t.Fatal("actual clone branch", sample.name)
		}
		src, err := os.Stat(actual.Files[0].Source)
		if err != nil {
			t.Fatal("source preserved", err)
		}
		dst, targetErr := os.Stat(actual.Files[0].Target)
		if sample.want && (targetErr != nil || sample.mode == "hardlink" && !os.SameFile(src, dst)) {
			t.Fatal("actual physical publication", targetErr)
		}
		if (sample.name == "OwnedFalse" || sample.name == "OwnedDict") && !os.IsNotExist(targetErr) {
			t.Fatal("terminal veto/dict made a file", targetErr)
		}
		evidence = append(evidence, map[string]any{"name": sample.name, "plan": actual, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	}
	for i, mode := range []string{"hardlink", "copy"} {
		name := []string{"OwnedReturnedSource", "OwnedReturnedClone"}[i]
		identity := int64(90001900 + i)
		set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": fullMedia(identity, name)})
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		plan := preview(name, identity, mode)
		returned := filepath.Join(target, "Plugin returned target", name+".mkv")
		result := map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "target_item": originalTransferPreviewItem(returned, "file", 26), "target_diritem": originalTransferPreviewItem(filepath.Dir(returned), "dir", 0), "file_list_new": []string{returned}, "transfer_type": originalTransferType(mode)}}
		cfg := config(identity, name, "module_transfer", "", result)
		cfg["transfer_target"] = returned
		if i == 1 {
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"] = nil
			other := config(identity, name, "module_transfer", "copy", result)
			other["modules"] = map[string]string{"transfer": "module_transfer"}
			other["transfer_target"] = returned
			set(clone, other)
		}
		set(manifest.PluginID, cfg)
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		actual := run(plan, true)
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		if actual.Files[0].Target != returned {
			t.Fatal("real returned destination not adopted", actual.Files)
		}
		if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
			t.Fatal("native destination written after terminal provider", err)
		}
		src, err := os.Stat(actual.Files[0].Source)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Stat(returned)
		if err != nil || (mode == "hardlink") != os.SameFile(src, dst) {
			t.Fatal("returned target actual method", err)
		}
		if bytes, err := os.ReadFile(returned); err != nil || string(bytes) != "owned real module transfer" {
			t.Fatal("actual returned bytes", err)
		}
		role := afterSource
		prior := beforeSource
		if i == 1 {
			role, prior = afterClone, beforeClone
		}
		if role.Calls["module_transfer"]-prior.Calls["module_transfer"] != 1 {
			t.Fatal("returned destination provider once", name)
		}
		var history struct {
			Data struct {
				Items []transferHistoryItem
				Total int
			}
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &history) != nil || history.Data.Total != 1 || len(history.Data.Items) != 1 || history.Data.Items[0].Target != returned {
			t.Fatal("actual source-returned history destination", history)
		}
		evidence = append(evidence, map[string]any{"name": name, "native_plan": plan, "returned_plan": actual, "history": history.Data, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	}
	previewEvidence := []any{}
	for i, sample := range []struct {
		name, method string
		result       any
		want, clone  bool
	}{{"PreviewFalse", "module_execute", false, false, false}, {"PreviewDict", "module_execute", map[string]any{"success": true}, false, false}, {"PreviewSource", "module_transfer", nil, true, false}, {"PreviewClone", "module_execute", nil, true, true}, {"PreviewNative", "module_fail", nil, true, false}} {
		identity := int64(90001800 + i)
		path := filepath.Join(source, sample.name+".mkv")
		if err := os.WriteFile(path, []byte("owned preview no publication"), 0640); err != nil {
			t.Fatal(err)
		}
		proposed := filepath.Join(target, "preview-only", sample.name+".mkv")
		result := sample.result
		if sample.name == "PreviewSource" {
			result = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "target_item": originalTransferPreviewItem(proposed, "file", 28)}}
		}
		cfg := config(identity, sample.name, sample.method, "", result)
		cfg["transfer_target"] = proposed
		set(manifest.PluginID, cfg)
		other := map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil}
		if sample.clone {
			other["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "target_item": originalTransferPreviewItem(proposed, "file", 28)}}
		}
		set(clone, other)
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		var out struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: identity, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}, 200), &out) != nil || len(out.Data.Items) != 1 || (out.Data.Failed == 0) != sample.want {
			t.Fatal("actual explicit preview result", sample.name, out.Data)
		}
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		if afterSource.Calls[sample.method]-beforeSource.Calls[sample.method] != 1 {
			t.Fatal("preview source invocation", sample.name)
		}
		if afterSource.Calls["transfer_event"] != beforeSource.Calls["transfer_event"] || afterClone.Calls["transfer_event"] != beforeClone.Calls["transfer_event"] {
			t.Fatal("preview invoked completion callback", sample.name)
		}
		if sample.clone && afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != 1 {
			t.Fatal("preview clone fallback")
		}
		var args struct{ Kwargs map[string]json.RawMessage }
		if json.Unmarshal(afterSource.Inputs[sample.method], &args) != nil || len(args.Kwargs) != 14 || string(args.Kwargs["preview"]) != "true" {
			t.Fatal("explicit production preview signature", sample.name)
		}
		if sample.name == "PreviewSource" || sample.clone {
			if out.Data.Items[0].Target != proposed {
				t.Fatal("hypothetical target", out.Data.Items[0].Target, proposed)
			}
		}
		if _, err := os.Stat(proposed); !os.IsNotExist(err) {
			t.Fatal("preview wrote actual target", sample.name, err)
		}
		if _, err := os.Stat(filepath.Join(target, "preview-only")); !os.IsNotExist(err) {
			t.Fatal("preview created directory", err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != "owned preview no publication" {
			t.Fatal("preview source changed", err)
		}
		previewEvidence = append(previewEvidence, map[string]any{"name": sample.name, "plan": out.Data, "before_source": beforeSource, "before_clone": beforeClone, "after_source": afterSource, "after_clone": afterClone})
	}
	for index, outcome := range []string{"native", "raw-false-subtitle", "typed-veto-subtitle"} {
		name := []string{"RelatedPreviewNative", "RelatedPreviewRaw", "RelatedPreviewVeto"}[index]
		identity := int64(90001840 + index)
		input := filepath.Join(source, name+".2024.mkv")
		sub := filepath.Join(source, name+".2024.zh.srt")
		audio := filepath.Join(source, name+".2024.flac")
		for _, path := range []string{input, sub, audio} {
			if err := os.WriteFile(path, []byte("owned related preview"), 0640); err != nil {
				t.Fatal(err)
			}
		}
		cfg := config(identity, name, "module_execute", "", nil)
		if outcome == "raw-false-subtitle" {
			cfg["transfer_results"] = map[string]any{sub: false}
		}
		if outcome == "typed-veto-subtitle" {
			cfg["transfer_results"] = map[string]any{sub: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned subtitle veto"}}}
		}
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		stem := filepath.Join(target, name+" (2024)", name+" (2024)")
		expected := map[string]string{input: stem + ".mkv", sub: stem + ".default.chi.zh-cn.srt", audio: stem + ".flac"}
		for _, path := range expected {
			if os.MkdirAll(filepath.Dir(path), 0750) != nil || os.WriteFile(path, []byte("preexisting preview target"), 0640) != nil {
				t.Fatal("owned preexisting preview target")
			}
		}
		beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
		var out struct{ Data manualOrganizeOutput }
		if json.Unmarshal(request("POST", "/api/files/organize", manualOrganizeRequest{Paths: []string{input}, MediaType: "movie", TMDBID: identity, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false), Preview: true}, 200), &out) != nil {
			t.Fatal("real related preview response")
		}
		failures := 0
		chain := 12
		cloneCalls := 3
		if outcome != "native" {
			failures = 1
			chain = 10
			cloneCalls = 2
		}
		if out.Data.Total != 3 || len(out.Data.Items) != 3 || out.Data.Failed != failures || out.Data.Submitted != 0 {
			t.Fatal("real per-task preview summary", out.Data)
		}
		for _, item := range out.Data.Items {
			if item.Source == sub && outcome != "native" {
				if item.Status != "failed" || item.Target != "" || outcome == "raw-false-subtitle" && item.Media.Title != "" || outcome == "typed-veto-subtitle" && item.Error != "owned subtitle veto" {
					t.Fatal("raw fallback/concrete preview failure", item)
				}
			} else if item.Status != "ready" || item.Target != expected[item.Source] {
				t.Fatal("real native related preview name", item, expected)
			}
			request("POST", "/api/import/execute", map[string]any{"plan_id": item.PlanID}, 400)
		}
		afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
		if afterSource.Calls["module_execute"]-beforeSource.Calls["module_execute"] != 3 || afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != cloneCalls || afterSource.Calls["transfer_chain_event"]-beforeSource.Calls["transfer_chain_event"] != chain || afterSource.Calls["transfer_event"] != beforeSource.Calls["transfer_event"] {
			t.Fatal("real independent related preview calls", outcome, beforeSource.Calls, afterSource.Calls, beforeClone.Calls, afterClone.Calls)
		}
		for _, path := range expected {
			if data, err := os.ReadFile(path); err != nil || string(data) != "preexisting preview target" {
				t.Fatal("preview changed owned destination", path, err)
			}
		}
		previewEvidence = append(previewEvidence, map[string]any{"name": name, "plan": out.Data, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	}
	for i, sample := range []struct {
		name   string
		fields map[string]any
		want   bool
	}{{"CtorBadSuccess", map[string]any{"success": 2}, true}, {"CtorBadCount", map[string]any{"file_count": 1.5}, true}, {"CtorBadItem", map[string]any{"target_item": map[string]any{"path": 123}}, true}, {"CtorVeto", map[string]any{"success": false, "message": "owned constructor veto", "need_scrape": nil}, false}} {
		identity := int64(90002000 + i)
		set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": fullMedia(identity, sample.name)})
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
		plan := preview(sample.name, identity, "hardlink")
		cfg := config(identity, sample.name, "module_execute", "", sample.fields)
		cfg["build_transfer_info"] = true
		set(manifest.PluginID, cfg)
		before := snapshot(manifest.PluginID)
		actual := run(plan, sample.want)
		after := snapshot(manifest.PluginID)
		if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
			t.Fatal("actual SDK constructor invocation", sample.name)
		}
		chain := 4
		// Veto has source storage2; source constructor errors fall through to
		// the native name pair. No invented host-side success is accepted.
		if !sample.want {
			chain = 2
		}
		if after.Calls["transfer_chain_event"]-before.Calls["transfer_chain_event"] != chain {
			t.Fatal("actual SDK constructor fallback", sample.name, before.Calls, after.Calls)
		}
		if sample.want {
			if b, err := os.ReadFile(actual.Files[0].Target); err != nil || string(b) != "owned real module transfer" {
				t.Fatal("real native constructor fallback effect", err)
			}
		} else {
			if _, err := os.Stat(actual.Files[0].Target); !os.IsNotExist(err) {
				t.Fatal("typed constructor veto made a file", err)
			}
		}
		evidence = append(evidence, map[string]any{"name": sample.name, "plan": actual, "before_source": before, "after_source": after})
	}
	for index, kind := range []string{"NativeNoScrapeUnspecified", "NativeNoScrapeFalse", "ModuleNoScrapeOverride"} {
		identity := int64(90009200 + index)
		path := filepath.Join(source, kind+".2024.mkv")
		if err := os.WriteFile(path, []byte("owned native flags source"), 0640); err != nil {
			t.Fatal(err)
		}
		cfg := config(identity, kind, "module_execute", "", nil)
		cfg["modules"] = map[string]string{"recognize_media": "module_search_media"}
		set(manifest.PluginID, cfg)
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{}})
		var scrape *bool
		if index > 0 {
			scrape = importFlag(index == 2)
		}
		var prepared struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: scrape, Files: []ImportFileRequest{{Path: path}}}, 200), &prepared) != nil || len(prepared.Data.Files) != 1 || prepared.Data.Files[0].Status != "ready" {
			t.Fatal("actual native flags preparation", kind, prepared.Data)
		}
		if index == 2 {
			cfg["modules"].(map[string]string)["transfer"] = "module_transfer"
			cfg["result"] = model
			cfg["transfer_target"] = prepared.Data.Files[0].Target
			set(manifest.PluginID, cfg)
		}
		before := snapshot(manifest.PluginID)
		actual := run(prepared.Data, true)
		var asset struct{ Data Asset }
		if json.Unmarshal(request("GET", "/api/assets/"+actual.Files[0].AssetID, nil, 200), &asset) != nil || asset.Data.ScrapeState != "skipped" {
			t.Fatal("native unspecified/false or module false must not leave pending scrape", kind, asset.Data)
		}
		var observed struct {
			Events []struct {
				Data struct {
					Info map[string]any `json:"transferinfo"`
				}
			} `json:"transfer_events"`
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 200), &observed) != nil || len(observed.Events) == 0 {
			t.Fatal("actual native flags ordinary callback", kind)
		}
		info := observed.Events[len(observed.Events)-1].Data.Info
		if info["need_notify"] != false || info["need_scrape"] != false {
			t.Fatal("source native flags not consumed", kind, info)
		}
		after := snapshot(manifest.PluginID)
		if after.Calls["module_transfer"]-before.Calls["module_transfer"] != map[bool]int{true: 1, false: 0}[index == 2] {
			t.Fatal("no transfer override must stay native", kind, before.Calls, after.Calls)
		}
		evidence = append(evidence, map[string]any{"name": kind, "plan": actual, "asset": asset.Data, "before_source": before, "after_source": after, "transfer_info": info})
	}
	{
		identity := int64(90009300)
		name := "HistoryBusyOwner"
		main := filepath.Join(source, name+".2024.mkv")
		extra := filepath.Join(source, name+".2024.srt")
		if os.WriteFile(main, []byte("owned held primary"), 0640) != nil || os.WriteFile(extra, []byte("owned held auxiliary"), 0640) != nil {
			t.Fatal("owned busy sources")
		}
		set(manifest.PluginID, config(identity, name, "module_execute", "", nil))
		set(clone, map[string]any{"enabled": true, "modules": map[string]string{}})
		var prepared struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: main}}}, 200), &prepared) != nil || len(prepared.Data.Files) != 1 {
			t.Fatal("busy owner preparation")
		}
		_ = run(prepared.Data, true)
		var histories struct {
			Data struct{ Items []transferHistoryItem }
		}
		if json.Unmarshal(request("GET", "/api/history/transfers?q="+name, nil, 200), &histories) != nil || len(histories.Data.Items) != 2 {
			t.Fatal("busy owner histories")
		}
		var selected transferHistoryItem
		for _, item := range histories.Data.Items {
			if item.Source == extra {
				selected = item
			}
		}
		if selected.ID == "" {
			t.Fatal("busy owner auxiliary history")
		}
		otherName := "HistoryBusyConsumer"
		other := filepath.Join(source, otherName+".2024.mkv")
		if err := os.WriteFile(other, []byte("owned held consumer"), 0640); err != nil {
			t.Fatal(err)
		}
		cfg := config(identity+1, otherName, "module_execute", "", nil)
		cfg["modules"].(map[string]string)["get_parent_item"] = "module_storage_parent"
		cfg["modules"].(map[string]string)["list_files"] = "module_storage_siblings"
		item := originalTransferPreviewItem(extra, "file", 20)
		item["name"], item["basename"] = otherName+".2024.srt", otherName+".2024"
		cfg["storage_siblings"], _ = plugins.ModuleResultTuple(item)
		gate := filepath.Join(source, "owned-transfer-hold.gate")
		cfg["transfer_hold"] = gate
		set(manifest.PluginID, cfg)
		var consumer struct{ Data ImportPlan }
		if json.Unmarshal(request("POST", "/api/import/preview", ImportRequest{MediaType: "movie", TMDBID: identity + 1, RootID: "owned-module-transfer-root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: other}}}, 200), &consumer) != nil || len(consumer.Data.Files) != 1 || consumer.Data.Files[0].Status != "ready" {
			t.Fatal("actual busy consumer plan", consumer.Data)
		}
		var submitted struct{ Data Job }
		if json.Unmarshal(request("POST", "/api/import/execute", map[string]any{"plan_id": consumer.Data.ID}, 200), &submitted) != nil || submitted.Data.ID == "" {
			t.Fatal("actual held consumer job")
		}
		entered := false
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
			if _, err := os.Stat(gate + ".entered"); err == nil {
				entered = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !entered {
			t.Fatal("actual SDK never entered held transfer")
		}
		var detail struct{ Data struct{ Job Job } }
		if json.Unmarshal(request("GET", "/api/jobs/"+submitted.Data.ID, nil, 200), &detail) != nil || detail.Data.Job.State != "running" {
			t.Fatal("actual held host job not running", detail.Data.Job)
		}
		var rejected struct{ Error struct{ Code string } }
		raw := request("DELETE", "/api/history/transfers/"+selected.ID, historyDeleteRequest{Confirm: true, DeleteSource: true, DeleteTarget: true, FileID: selected.executionFileID()}, 400)
		if json.Unmarshal(raw, &rejected) != nil || rejected.Error.Code != "FILE_IN_USE" {
			t.Fatal("active auxiliary consumer deletion not rejected", string(raw))
		}
		for _, path := range []string{extra, selected.Target, main, other} {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("held source/target changed", path, err)
			}
		}
		if err := os.WriteFile(gate, []byte("release owned SDK"), 0640); err != nil {
			t.Fatal(err)
		}
		completed := false
		for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
			if json.Unmarshal(request("GET", "/api/jobs/"+submitted.Data.ID, nil, 200), &detail) != nil {
				t.Fatal("held completion read")
			}
			if detail.Data.Job.State == "succeeded" {
				completed = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !completed {
			t.Fatal("released actual task did not complete", detail.Data.Job.State)
		}
		evidence = append(evidence, map[string]any{"name": "ActualBusyAuxiliaryConsumer", "owner_history": selected, "consumer_plan": consumer.Data, "consumer_job": detail.Data.Job, "rejected": rejected})
	}
	beforeSource, beforeClone := snapshot(manifest.PluginID), snapshot(clone)
	stop()
	start()
	afterSource, afterClone := snapshot(manifest.PluginID), snapshot(clone)
	if beforeSource.PID == afterSource.PID || beforeClone.PID == afterClone.PID || afterSource.Calls["module_transfer"] != 0 || afterClone.Calls["module_transfer"] != 0 {
		t.Fatal("actual cold module no replay")
	}
	var coldPlans struct{ Data []ImportPlan }
	if json.Unmarshal(request("GET", "/api/import/plans", nil, 200), &coldPlans) != nil {
		t.Fatal("cold partial plans")
	}
	for _, expected := range partialPlans {
		found := false
		for _, actual := range coldPlans.Data {
			if actual.ID == expected.ID {
				found = actual.State == "partial" && actual.Files[0].Status == expected.Files[0].Status
			}
		}
		if !found {
			t.Fatal("cold partial status lost", expected.ID)
		}
	}
	for _, expected := range extraHistories {
		var actual struct{ Data transferHistoryItem }
		if json.Unmarshal(request("GET", "/api/history/transfers/"+expected.ID, nil, 200), &actual) != nil || actual.Data.Source != expected.Source || actual.Data.Target != expected.Target || actual.Data.Status != "success" {
			t.Fatal("cold independent history", actual)
		}
		src, e1 := os.Stat(actual.Data.Source)
		dst, e2 := os.Stat(actual.Data.Target)
		if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
			t.Fatal("cold extra physical identity", e1, e2)
		}
	}
	if afterSource.Calls["module_execute"] != 0 || afterClone.Calls["module_execute"] != 0 {
		t.Fatal("cold extra replay", afterSource.Calls, afterClone.Calls)
	}
	evidence = append(evidence, map[string]any{"name": "SingleExtraCold", "histories": extraHistories, "source": afterSource, "clone": afterClone})
	evidence = append(evidence, map[string]any{"name": "PartialTasksColdRecovery", "partial_plans": partialPlans, "before_source": beforeSource, "after_source": afterSource, "before_clone": beforeClone, "after_clone": afterClone})
	cfg := config(90001799, "Owned high-level UI", "module_transfer", "", model)
	cfg["modules"].(map[string]string)["obtain_images"] = "module_images"
	cfg["image_mode"] = "false"
	var relatedSources []string
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_RELATED_UI") == "true" {
		for _, ext := range []string{".zh.srt", ".flac"} {
			path := filepath.Join(source, "OwnedUITransfer.2024"+ext)
			if err := os.WriteFile(path, []byte("owned UI related "+ext), 0640); err != nil {
				t.Fatal(err)
			}
			relatedSources = append(relatedSources, path)
		}
	}
	set(manifest.PluginID, map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media"}, "media_result": fullMedia(90001799, "Owned high-level UI")})
	set(clone, map[string]any{"enabled": true, "modules": map[string]string{"transfer": "module_execute"}, "result": nil})
	uiPlan := preview("OwnedUITransfer", 90001799, "hardlink")
	cfg["transfer_target"] = uiPlan.Files[0].Target
	if os.Getenv("PLUGIN_PROCESS_TRANSFER_RETURNED_TARGET") == "true" {
		cfg["transfer_target"] = filepath.Join(target, "Plugin returned UI target", "Actual plugin file.mkv")
	}
	cfg["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "target_item": originalTransferPreviewItem(cfg["transfer_target"].(string), "file", 26), "target_diritem": originalTransferPreviewItem(filepath.Dir(cfg["transfer_target"].(string)), "dir", 0)}}
	var relatedTargets []string
	if len(relatedSources) > 0 {
		targets := map[string]string{uiPlan.Files[0].Source: cfg["transfer_target"].(string)}
		results := map[string]any{}
		for index, path := range relatedSources {
			ext := []string{".srt", ".flac"}[index]
			mapped := filepath.Join(filepath.Dir(cfg["transfer_target"].(string)), "Actual plugin related"+ext)
			targets[path] = mapped
			relatedTargets = append(relatedTargets, mapped)
		}
		for path, mapped := range targets {
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			results[path] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "transfer_type": "link", "file_count": 1, "file_list": []string{path}, "file_list_new": []string{mapped}, "total_size": stat.Size(), "target_item": originalTransferPreviewItem(mapped, "file", stat.Size()), "target_diritem": originalTransferPreviewItem(filepath.Dir(mapped), "dir", 0)}}
		}
		cfg["transfer_targets"], cfg["transfer_results"] = targets, results
		if os.Getenv("PLUGIN_PROCESS_TRANSFER_COLLECTION_UI") == "true" {
			cfg["modules"].(map[string]string)["get_parent_item"] = "module_storage_parent"
			cfg["modules"].(map[string]string)["list_files"] = "module_storage_siblings"
			// Deliberately reverse native creation order. Transfer consumes the
			// terminal tuple as-is; the browser endpoint has a different contract.
			cfg["storage_siblings"], _ = plugins.ModuleResultTuple(originalTransferPreviewItem(relatedSources[1], "file", 22), originalTransferPreviewItem(relatedSources[0], "file", 24))
		}
	}
	set(manifest.PluginID, cfg)
	proof, _ := json.Marshal(map[string]any{"cases": evidence, "preview_cases": previewEvidence, "before_source": beforeSource, "before_clone": beforeClone, "after_source": afterSource, "after_clone": afterClone, "ui_plan": uiPlan})
	writeOwnedEventsProcessEvidence(t, "transfer-module-real-main.json", proof)
	if statePath := os.Getenv("PLUGIN_PROCESS_TRANSFER_MODULE_STATE"); statePath != "" {
		state, _ := json.Marshal(map[string]any{"origin": origin, "data_dir": dir, "directory": source, "target": target, "source": manifest.PluginID, "clone": clone, "source_config": cfg, "ui_plan": uiPlan, "expected_module_target": cfg["transfer_target"], "related_sources": relatedSources, "expected_related_targets": relatedTargets})
		if os.WriteFile(statePath, state, 0600) != nil {
			t.Fatal("write module browser state")
		}
		t.Log("actual transfer module ready for owned browser")
		finished := false
		deadline := time.Now().Add(45 * time.Minute)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statePath + ".restart"); err == nil {
				stop()
				start()
				_ = os.Remove(statePath + ".restart")
				_ = os.WriteFile(statePath+".restart.done", []byte("restarted"), 0600)
			}
			if _, err := os.Stat(statePath + ".done"); err == nil {
				finished = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !finished {
			t.Fatal("module transfer browser marker absent")
		}
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	t.Log("actual module source/false/plain dict/None→clone/copy/corruption/cold/uninstall verified; browser proof separate")
}
