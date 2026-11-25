package pluginuninstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Expectations are the independent, actually executed Python3.12 AST oracle.
// These fixtures model host boundaries, not an App/main acceptance claim.
type freshCase struct {
	Name  string `json:"name"`
	Input *struct {
		Installed []string          `json:"installed"`
		Selection json.RawMessage   `json:"selection"`
		Flags     []json.RawMessage `json:"flags"`
		Fault     string            `json:"fault"`
		Clone     bool              `json:"clone"`
		Residual  bool              `json:"rmtree_residual"`
		Config    string            `json:"config"`
	} `json:"input"`
	Error            json.RawMessage `json:"error"`
	Classes, Running []string
	Jobs             map[string]map[string]string
	Backend          []string `json:"backend_jobs"`
	Files            []string `json:"filesystem"`
	DB, Cache, Data  map[string]json.RawMessage
	Trace            []json.RawMessage
	Options          json.RawMessage
	Form             []json.RawMessage
	Raises           string
}

func freshCases(t *testing.T) []freshCase {
	t.Helper()
	raw, err := os.ReadFile("reference/python312-extended-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Cases []freshCase }
	if err = decode(raw, &v); err != nil || len(v.Cases) != 50 {
		t.Fatal("independent source50 fixture", err)
	}
	return v.Cases
}
func cloneRawMap(v map[string]json.RawMessage) map[string]json.RawMessage {
	r := map[string]json.RawMessage{}
	for k, b := range v {
		r[k] = slices.Clone(b)
	}
	return r
}
func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

type freshHost struct {
	installed               []string
	own, fault              string
	residual                bool
	classes, running, files map[string]bool
	jobs                    map[string]map[string]string
	backend                 []string
	db, cache, data         map[string]json.RawMessage
	trace                   []any
}

func newFreshHost(c freshCase) *freshHost {
	in := c.Input
	own := "PluginUnInstall"
	if in.Clone {
		own += "owned"
	}
	values := map[string]json.RawMessage{"UserInstalledPlugins": rawJSON(in.Installed), "plugin.Alpha": rawJSON(map[string]string{"keep": "a"}), "plugin.Beta": rawJSON(map[string]string{"keep": "b"}), "plugin.PluginUnInstall": rawJSON(map[string]any{"plugin_ids": in.Selection, "unknown": "lost"}), "plugin.PluginUnInstallowned": rawJSON(map[string]any{"plugin_ids": in.Selection, "unknown": "clone-lost"})}
	h := &freshHost{installed: slices.Clone(in.Installed), own: own, fault: in.Fault, residual: in.Residual, classes: map[string]bool{}, running: map[string]bool{}, files: map[string]bool{}, db: cloneRawMap(values), cache: cloneRawMap(values), data: map[string]json.RawMessage{}, jobs: map[string]map[string]string{"a": {"pid": "Alpha", "name": "Adefault"}, "ax": {"pid": "Alpha", "name": "Aextra"}, "b": {"pid": "Beta", "name": "Bdefault"}, "self": {"pid": own, "name": "Self"}}, backend: []string{"a|1", "ax|2", "b|3", "self|4"}, trace: []any{}}
	for i, id := range []string{"Alpha", "Beta", "PluginUnInstall", "PluginUnInstallowned", "Uninstalled"} {
		h.classes[id] = true
		h.running[id] = true
		h.files["/owned-root/app/plugins/"+strings.ToLower(id)] = true
		if i < 4 {
			h.data[id] = rawJSON(map[string]int{"owned": i + 1})
		}
	}
	return h
}
func (h *freshHost) Installed(context.Context) ([]string, error) {
	return slices.Clone(h.installed), nil
}
func (h *freshHost) RemoveJobs(_ context.Context, id string) error {
	for k, v := range h.jobs {
		if v["pid"] != id {
			continue
		}
		delete(h.jobs, k)
		if h.fault == "scheduler-get" || h.fault == "scheduler-lookup" {
			continue
		}
		h.backend = slices.DeleteFunc(h.backend, func(s string) bool { return strings.HasPrefix(s, k+"|") })
	}
	return nil
}
func (h *freshHost) RemovePlugin(_ context.Context, id string) error {
	delete(h.classes, id)
	delete(h.running, id)
	return nil
}
func (h *freshHost) PackageExists(_ context.Context, id string) (bool, error) {
	p := "/owned-root/app/plugins/" + strings.ToLower(id)
	h.trace = append(h.trace, []any{"fs.exists", p})
	if h.fault == "exists" {
		return false, errors.New("source PermissionError")
	}
	return h.files[p], nil
}
func (h *freshHost) RemovePackage(_ context.Context, id string) error {
	p := "/owned-root/app/plugins/" + strings.ToLower(id)
	h.trace = append(h.trace, []any{"fs.rmtree", p, map[string]bool{"ignore_errors": true}})
	if h.fault == "rmtree-unexpected" {
		return errors.New("source TypeError")
	}
	if !h.residual {
		delete(h.files, p)
	}
	return nil
}
func (h *freshHost) DeleteConfig(_ context.Context, id string, force bool) (bool, error) {
	if !force && !h.classes[id] {
		return false, nil
	}
	delete(h.cache, "plugin."+id)
	delete(h.db, "plugin."+id)
	return true, nil
}
func (h *freshHost) DeleteData(_ context.Context, id string, force bool) (bool, error) {
	if !force && !h.classes[id] {
		return false, nil
	}
	delete(h.data, id)
	return true, nil
}
func (h *freshHost) LogRemoved(_ context.Context, id string) error {
	h.trace = append(h.trace, []any{"log", "info", "插件 " + id + " 已卸载"})
	return nil
}
func (h *freshHost) save(key string, v json.RawMessage) error {
	old := h.cache[key]
	if reflect.DeepEqual(normalizeRaw(old), normalizeRaw(v)) {
		return nil
	}
	h.cache[key] = slices.Clone(v) // actual SystemConfigOper caches before DB.
	val, _ := value(v)
	if !truthy(val) {
		if _, ok := h.db[key]; ok {
			h.trace = append(h.trace, []any{"db.delete", key})
			delete(h.db, key)
		}
		return nil
	}
	action := "db.update"
	if _, ok := h.db[key]; !ok {
		action = "db.create"
	}
	h.trace = append(h.trace, []any{action, key, v})
	if h.fault == "db:"+key {
		return errors.New("source DB failure")
	}
	h.db[key] = slices.Clone(v)
	return nil
}
func (h *freshHost) SaveInstalled(_ context.Context, v []string) error {
	err := h.save("UserInstalledPlugins", rawJSON(v))
	if err == nil {
		h.installed = slices.Clone(v)
	}
	return err
}
func (h *freshHost) SaveOwnConfig(_ context.Context, v json.RawMessage) error {
	return h.save("plugin."+h.own, v)
}
func normalizeRaw(raw json.RawMessage) any { v, _ := value(raw); return v }
func compareFresh(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(normalized(t, got), normalized(t, want)) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Fatalf("%s: got %s want %s", label, g, w)
	}
}
func projectedTrace(t *testing.T, in []json.RawMessage) []any {
	out := []any{}
	for _, r := range in {
		var a []any
		if decode(r, &a) != nil {
			t.Fatal("source trace")
		}
		kind, _ := a[0].(string)
		if strings.HasPrefix(kind, "fs.") || strings.HasPrefix(kind, "db.") || (kind == "log" && len(a) > 2 && strings.HasPrefix(a[2].(string), "插件 ") && strings.HasSuffix(a[2].(string), " 已卸载")) {
			out = append(out, a)
		}
	}
	return out
}
func TestFreshPython312Execution36(t *testing.T) {
	count := 0
	for _, c := range freshCases(t) {
		if c.Input == nil {
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) { assertFreshExecution(t, c) })
	}
	if count != 36 {
		t.Fatalf("execution case count %d", count)
	}
}
func assertFreshExecution(t *testing.T, c freshCase) {
	t.Helper()
	h := newFreshHost(c)
	in := c.Input
	raw := rawJSON(map[string]any{"plugin_ids": in.Selection, "clear_config": in.Flags[0], "clear_data": in.Flags[1], "unknown": "lost"})
	if in.Config == "none" {
		raw = json.RawMessage(`null`)
	}
	if in.Config == "empty" {
		raw = json.RawMessage(`{}`)
	}
	_, err := Run(context.Background(), raw, h)
	if (err != nil) != (string(c.Error) != "null") {
		t.Fatalf("source error boundary: %v expected %s", err, c.Error)
	}
	compareFresh(t, "classes", keys(h.classes), sorted(c.Classes))
	compareFresh(t, "running", keys(h.running), sorted(c.Running))
	compareFresh(t, "jobs", h.jobs, c.Jobs)
	compareFresh(t, "backend jobs", h.backend, c.Backend)
	compareFresh(t, "filesystem", keys(h.files), c.Files)
	compareFresh(t, "DB", h.db, c.DB)
	compareFresh(t, "cache", h.cache, c.Cache)
	compareFresh(t, "data", h.data, c.Data)
	compareFresh(t, "business boundary order", h.trace, projectedTrace(t, c.Trace))
}

func TestFreshPython312InstalledDBFailureDelta(t *testing.T) {
	raw, err := os.ReadFile("reference/python312-installed-DB-delta.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Cases []freshCase }
	if err = decode(raw, &fixture); err != nil || len(fixture.Cases) != 1 {
		t.Fatal("source DB delta", err)
	}
	assertFreshExecution(t, fixture.Cases[0])
}

func sorted(v []string) []string { v = slices.Clone(v); slices.Sort(v); return v }

// Inputs below are literal operands of sealed options_extension.py, not target-
// generated expected results. JSON retains float kind and object insertion order.
var freshOptions = map[string]string{
	"order-above-2pow53":              `[{"id":"late","plugin_order":9007199254740993},{"id":"early","plugin_order":9007199254740992},{"id":"tied","plugin_order":9007199254740992}]`,
	"order-large-negative":            `[{"id":"later","plugin_order":-1267650600228229401496703205375},{"id":"earlier","plugin_order":-1267650600228229401496703205376}]`,
	"order-int-float-exact":           `[{"id":"int-late","plugin_order":9007199254740993},{"id":"float-early","plugin_order":9007199254740992.0},{"id":"int-tie","plugin_order":9007199254740992}]`,
	"order-float-fractions":           `[{"id":"z","plugin_order":2.5},{"id":"a","plugin_order":2},{"id":"b","plugin_order":2.0},{"id":"c","plugin_order":-1.5}]`,
	"order-bool-integer-tie":          `[{"id":"a","plugin_order":true},{"id":"b","plugin_order":1},{"id":"zero","plugin_order":false}]`,
	"order-none-mixed":                `[{"id":"none","plugin_order":null},{"id":"zero","plugin_order":0}]`,
	"order-string-mixed":              `[{"id":"str","plugin_order":"1"},{"id":"int","plugin_order":1}]`,
	"metadata-none":                   `[{"id":"n","plugin_name":null,"plugin_version":null}]`,
	"metadata-boolean":                `[{"id":"b","plugin_name":false,"plugin_version":true}]`,
	"metadata-numeric":                `[{"id":"i","plugin_name":9007199254740993,"plugin_version":1.5}]`,
	"metadata-list-dict":              `[{"id":"c","plugin_name":["x",null,true],"plugin_version":{"z":1,"a":"q"}}]`,
	"metadata-missing":                `[{"id":"id"}]`,
	"raw-options-stability-fallbacks": `[{"id":"Z","installed":true,"plugin_order":2,"plugin_name":null,"plugin_version":null},{"id":"A","installed":"false","plugin_order":1},{"id":"B","installed":true,"plugin_order":1,"plugin_name":"Bee","plugin_version":"2"},{"id":"","installed":true,"plugin_order":0},{"id":"NO","installed":false,"plugin_order":-1}]`,
	"actual-manager-options-and-form": `[{"id":"Alpha","plugin_order":0,"plugin_name":"Alpha Name","plugin_version":"1.0"},{"id":"Beta","plugin_order":1,"plugin_name":"Beta Name","plugin_version":"1.0"},{"id":"PluginUnInstallowned","plugin_order":3,"plugin_name":"PluginUnInstallowned Name","plugin_version":"1.0"},{"id":"PluginUnInstall","plugin_order":98,"plugin_name":"插件彻底卸载","plugin_version":"2.2"}]`,
}

func TestFreshPython312Options14(t *testing.T) {
	count := 0
	for _, c := range freshCases(t) {
		raw, ok := freshOptions[c.Name]
		if !ok {
			continue
		}
		count++
		t.Run(c.Name, func(t *testing.T) {
			var rows []LocalPlugin
			if err := decode([]byte(raw), &rows); err != nil {
				t.Fatal(err)
			}
			for _, p := range rows {
				if _, ok := p["installed"]; !ok {
					p["installed"] = true
				}
				if _, ok := p["plugin_order"]; !ok {
					p["plugin_order"] = json.Number("0")
				}
			}
			got, err := LocalOptions(rows)
			if (err != nil) != (c.Raises != "") {
				t.Fatal("source option exception", err, c.Raises)
			}
			if err == nil {
				compareFresh(t, "source options", got, c.Options)
				if len(c.Form) == 2 {
					f, e := Form(got)
					if e != nil {
						t.Fatal(e)
					}
					compareFresh(t, "source complete form", f, map[string]json.RawMessage{"conf": c.Form[0], "model": c.Form[1]})
				}
			}
		})
	}
	if count != 14 {
		t.Fatalf("options count %d", count)
	}
}
