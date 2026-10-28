package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"light-media/internal/fileextensions"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

func TestOriginalTransferDirectoryPlannerFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-directory-plan-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name      string
			Items     []map[string]any
			Sync      bool
			Min       int64 `json:"min_filesize"`
			Pattern   string
			Metadata  map[string]map[string]any
			Output    []string
			Inherited map[string]string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 10 {
		t.Fatal("fixed source oracle", err)
	}
	snapshot, err := fileextensions.Resolve(fileextensions.Inputs{Namespace: "directory-oracle", SourceID: "fixed-planner", EnvironmentKnown: true, ConfigurationKnown: true, Configuration: map[string]json.RawMessage{fileextensions.MediaKey: json.RawMessage(`[".mkv",".mp4"]`), fileextensions.SubtitleKey: json.RawMessage(`[".srt"]`), fileextensions.AudioKey: json.RawMessage(`[".flac"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	classifier, err := transferextensions.New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			var format *regexp.Regexp
			if sample.Pattern != "" {
				format = regexp.MustCompile(sample.Pattern)
			}
			items := []originalTransferCandidate{}
			for _, item := range sample.Items {
				candidate := originalTransferCandidate{Item: item}
				if originalDirectoryAllowed(candidate, classifier, sample.Min, format) {
					items = append(items, candidate)
				}
			}
			actual, inherited, err := originalPlanTransferDirectory(items, classifier, sample.Sync, func(path string) (*releasemeta.MetaObject, error) {
				m := releasemeta.EmptyMetaObject()
				for k, v := range sample.Metadata[path] {
					if k == "name" {
						k = "cn_name"
					}
					if n, ok := v.(float64); ok {
						v = int64(n)
					}
					m.Set(k, v)
				}
				return m, nil
			})
			paths := []string{}
			for _, item := range actual {
				paths = append(paths, sourceText(item.Item["path"]))
			}
			if err != nil || !reflect.DeepEqual(paths, sample.Output) || !reflect.DeepEqual(inherited, sample.Inherited) {
				t.Fatal("fixed order/inheritance", paths, inherited, sample.Output, sample.Inherited, err)
			}
		})
	}
}

func TestOriginalTransferDirectoryExtrasActualSDK(t *testing.T) {
	a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	source := filepath.Join(filepath.Dir(plan.Files[0].Source), "planned")
	main := filepath.Join(source, "Owned.2024.mkv")
	sub := filepath.Join(source, "Owned.2024.zh.srt")
	audio := filepath.Join(source, "Orphan.S01E03.flac")
	for _, path := range []string{main, sub, audio} {
		importWrite(t, path, []byte("owned directory task"))
	}
	item := func(path string) any {
		return map[string]any{"storage": "local", "type": "file", "path": path, "name": filepath.Base(path), "extension": strings.TrimPrefix(filepath.Ext(path), "."), "size": 21}
	}
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute", "list_files": "module_storage_tree"}
	cfg["result"] = nil
	cfg["storage_tree"] = map[string]any{"list_files:" + source: map[string]any{"__plugin_module_tuple": []any{item(audio), item(sub), item(main)}}}
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	scan, err := a.manualOrganizeScan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range scan.(map[string]any)["files"].([]importScanFile) {
		if file.Path == audio && (file.originalResolvedMeta == nil || file.originalResolvedMeta.Number("begin_episode") == nil || file.originalResolvedMeta.Number("begin_episode").Int64() != 3) {
			t.Fatal("orphan keeps full source MetaInfoPath numbering", file.originalResolvedMeta)
		}
	}
	in := manualOrganizeRequest{Paths: []string{source}, MediaType: "movie", TMDBID: 123, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Preview: true}
	out, err := a.manualOrganize(ctx, in)
	if err != nil || out.Failed != 0 || len(out.Items) != 3 {
		t.Fatal("directory main+related+orphan preview", out, err)
	}
	for i, path := range []string{main, sub, audio} {
		if out.Items[i].Source != path {
			t.Fatal("fixed planner order", out)
		}
	}
	in.Preview = false
	out, err = a.manualOrganize(ctx, in)
	if err != nil || out.Failed != 0 || out.Submitted != 3 {
		t.Fatal("independent directory tasks", out, err)
	}
	before := ownedModuleRead(t, object)
	for _, item := range out.Items {
		var p ImportPlan
		if err = a.Store.Get(ctx, "import_plan", item.PlanID, &p); err != nil || len(p.Files) != 1 {
			t.Fatal(p, err)
		}
		job := overwriteStart(t, a, p)
		if err = a.runImport(ctx, job, p.ID, nil); err != nil {
			t.Fatal(err)
		}
		s, err := os.Stat(item.Source)
		if err != nil {
			t.Fatal(err)
		}
		d, err := os.Stat(item.Target)
		if err != nil || !os.SameFile(s, d) {
			t.Fatal("actual independent hardlink", item, err)
		}
	}
	a.wg.Wait()
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 3 {
		t.Fatal("directory extras must not also run as main sidecars", before.Calls, after.Calls)
	}
}
