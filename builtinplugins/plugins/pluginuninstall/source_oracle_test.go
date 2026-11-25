package pluginuninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type referenceCase struct {
	Name           string          `json:"name"`
	Config         json.RawMessage `json:"config"`
	Installed      []string        `json:"installed"`
	Packages       []string        `json:"package_ids"`
	Fail           string          `json:"fail"`
	FailedPackages []string        `json:"failed_package_ids"`
	Expected       struct {
		Trace     json.RawMessage `json:"trace"`
		Error     string          `json:"error"`
		Remaining []string        `json:"remaining"`
		OwnConfig json.RawMessage `json:"own_config"`
		Removed   []string        `json:"removed"`
		ConfigIDs []string        `json:"config_ids"`
		DataIDs   []string        `json:"data_ids"`
		Packages  []string        `json:"package_directories"`
		Classes   []string        `json:"class_ids"`
	} `json:"expected"`
}

type oracle struct {
	SourceHash  string          `json:"installed_source_sha256"`
	ManagerHash string          `json:"manager_source_sha256"`
	Cases       []referenceCase `json:"cases"`
	FormCases   []struct {
		Name    string          `json:"name"`
		Local   []LocalPlugin   `json:"local_plugins"`
		Options json.RawMessage `json:"options"`
		Form    json.RawMessage `json:"form"`
		Model   json.RawMessage `json:"model"`
		Error   string          `json:"error"`
	} `json:"form_cases"`
}

func loadOracle(t *testing.T) oracle {
	t.Helper()
	raw, err := os.ReadFile("reference/initial-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference oracle
	if err = decode(raw, &reference); err != nil || len(reference.Cases) != 31 || len(reference.FormCases) != 5 || reference.SourceHash != "2210e9dea47eeccdc46208d515e45ce1222dcadf50dbfc63cbeb41f94e470487" || reference.ManagerHash != "b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7" {
		t.Fatal("invalid installed source oracle", err)
	}
	return reference
}

func normalized(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err = decode(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type fixtureHost struct {
	t           *testing.T
	root        string
	trace       []map[string]any
	installed   []string
	classes     map[string]bool
	configs     map[string]bool
	data        map[string]bool
	ownConfig   json.RawMessage
	fail        string
	failedFiles []string
}

var errSynthetic = errors.New("synthetic host failure")

// The initial historical source oracle did not instrument logger calls.
// The fresh3.12 oracle and actual worker tests verify that ordered boundary.
func (h *fixtureHost) LogRemoved(context.Context, string) error { return nil }

func newFixtureHost(t *testing.T, input referenceCase) *fixtureHost {
	t.Helper()
	host := &fixtureHost{t: t, root: t.TempDir(), trace: []map[string]any{}, installed: append([]string{}, input.Installed...), classes: map[string]bool{}, configs: map[string]bool{}, data: map[string]bool{}, ownConfig: bytes.Clone(input.Config), fail: input.Fail, failedFiles: input.FailedPackages}
	for _, id := range input.Installed {
		host.classes[id], host.configs[id], host.data[id] = true, true, true
	}
	for _, id := range input.Packages {
		path := host.path(id)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "synthetic.txt"), []byte("owned fixture only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return host
}

func (h *fixtureHost) path(id string) string {
	h.t.Helper()
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, "/\\.\x00\r\n") {
		h.t.Fatal("unsafe synthetic package ID")
	}
	return filepath.Join(h.root, strings.ToLower(id))
}

func (h *fixtureHost) record(kind string, fields map[string]any) error {
	fields["kind"] = kind
	h.trace = append(h.trace, fields)
	if h.fail == kind {
		return errSynthetic
	}
	return nil
}
func (h *fixtureHost) Installed(context.Context) ([]string, error) {
	return slices.Clone(h.installed), h.record("installed", map[string]any{})
}
func (h *fixtureHost) RemoveJobs(_ context.Context, id string) error {
	return h.record("remove_jobs", map[string]any{"id": id})
}
func (h *fixtureHost) RemovePlugin(_ context.Context, id string) error {
	if err := h.record("remove_plugin", map[string]any{"id": id}); err != nil {
		return err
	}
	delete(h.classes, id)
	return nil
}
func (h *fixtureHost) PackageExists(_ context.Context, id string) (bool, error) {
	if err := h.record("package_exists", map[string]any{"directory": strings.ToLower(id)}); err != nil {
		return false, err
	}
	_, err := os.Lstat(h.path(id))
	return err == nil, nil
}
func (h *fixtureHost) RemovePackage(_ context.Context, id string) error {
	if err := h.record("remove_package", map[string]any{"directory": strings.ToLower(id)}); err != nil {
		return err
	}
	if slices.Contains(h.failedFiles, id) {
		// Historical source fixture represents ordinary rmtree(ignore_errors=True):
		// the host retains the directory while returning normally.
		return nil
	}
	return os.RemoveAll(h.path(id)) // Only this test's own immediate child.
}
func (h *fixtureHost) DeleteConfig(_ context.Context, id string, force bool) (bool, error) {
	if err := h.record("delete_config", map[string]any{"id": id, "force": force}); err != nil {
		return false, err
	}
	if !force && !h.classes[id] {
		return false, nil
	}
	delete(h.configs, id)
	return true, nil
}
func (h *fixtureHost) DeleteData(_ context.Context, id string, force bool) (bool, error) {
	if err := h.record("delete_data", map[string]any{"id": id, "force": force}); err != nil {
		return false, err
	}
	if !force && !h.classes[id] {
		return false, nil
	}
	delete(h.data, id)
	return true, nil
}
func (h *fixtureHost) SaveInstalled(_ context.Context, ids []string) error {
	if err := h.record("save_installed", map[string]any{"value": ids}); err != nil {
		return err
	}
	h.installed = append([]string{}, ids...)
	return nil
}
func (h *fixtureHost) SaveOwnConfig(_ context.Context, raw json.RawMessage) error {
	if err := h.record("save_own_config", map[string]any{"value": raw}); err != nil {
		return err
	}
	h.ownConfig = bytes.Clone(raw)
	return nil
}

func keys(values map[string]bool) []string {
	out := []string{}
	for key := range values {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func TestInstalledExecutionOracleAndOwnedPackageRemoval(t *testing.T) {
	for _, input := range loadOracle(t).Cases {
		t.Run(input.Name, func(t *testing.T) {
			host := newFixtureHost(t, input)
			result, err := Run(context.Background(), input.Config, host)
			if (err != nil) != (input.Expected.Error != "") {
				t.Fatal("source error boundary changed", err, input.Expected.Error)
			}
			if !reflect.DeepEqual(normalized(t, host.trace), normalized(t, input.Expected.Trace)) {
				t.Fatalf("source operation order changed: got %#v, want %s", host.trace, input.Expected.Trace)
			}
			if !reflect.DeepEqual(host.installed, input.Expected.Remaining) || !reflect.DeepEqual(normalized(t, host.ownConfig), normalized(t, input.Expected.OwnConfig)) || !reflect.DeepEqual(keys(host.classes), input.Expected.Classes) || !reflect.DeepEqual(keys(host.configs), input.Expected.ConfigIDs) || !reflect.DeepEqual(keys(host.data), input.Expected.DataIDs) {
				t.Fatal("installed manager effects/config retention changed")
			}
			entries, readErr := os.ReadDir(host.root)
			if readErr != nil {
				t.Fatal(readErr)
			}
			files := []string{}
			for _, entry := range entries {
				files = append(files, entry.Name())
			}
			if !reflect.DeepEqual(files, input.Expected.Packages) || !reflect.DeepEqual(result.Removed, input.Expected.Removed) || len(result.ConfigCleared)+len(result.DataCleared) != 0 {
				t.Fatal("removed package, duplicate installed ID or no-op cleanup result changed", result, files)
			}
		})
	}
}

func TestInstalledFormOptionsOracle(t *testing.T) {
	for _, input := range loadOracle(t).FormCases {
		t.Run(input.Name, func(t *testing.T) {
			options, err := LocalOptions(input.Local)
			if (err != nil) != (input.Error != "") {
				t.Fatal("source option sort error changed", err, input.Error)
			}
			if err == nil && !reflect.DeepEqual(normalized(t, options), normalized(t, input.Options)) {
				t.Fatal("source installed filtering, stable order or missing/null fallback changed", options, string(input.Options))
			}
			if err == nil {
				form, formErr := Form(options)
				want := map[string]json.RawMessage{"conf": input.Form, "model": input.Model}
				if formErr != nil || !reflect.DeepEqual(normalized(t, form), normalized(t, want)) {
					t.Fatal("complete source configuration form changed", formErr)
				}
			}
		})
	}
}
