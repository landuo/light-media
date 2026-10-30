package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedSystemConfigSourceCodepointsActualWorkerCold(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "testdata", "plugin-shared-host-20261030", "system-config", "source", "unicode-consumer.json"))
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Observations []struct {
			Case  string `json:"case"`
			Input struct {
				Rules json.RawMessage `json:"rules"`
				Title string          `json:"title"`
			} `json:"input"`
			SetReturn     json.RawMessage `json:"set_return"`
			TitleResponse string          `json:"title_response_body"`
			TitleError    string          `json:"title_response_error"`
		} `json:"observations"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	for _, observation := range source.Observations {
		t.Logf("actual original WordsMatcher + title-only JSONResponse %s", observation.Case)
		result, e := systemConfigSet(t, p, string(observation.Input.Rules))
		if e != nil {
			t.Fatal(e)
		}
		assertSharedOracleJSON(t, result, observation.SetReturn)
		check := func() {
			got, e := a.prepareRecognitionTitleChecked(context.Background(), observation.Input.Title)
			if observation.TitleError != "" {
				if e == nil || got != "" {
					t.Fatalf("source %s title failed %s but target returned %q: %v", observation.Case, observation.TitleError, got, e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			var want string
			if e = json.Unmarshal([]byte(observation.TitleResponse), &want); e != nil {
				t.Fatal(e)
			}
			if got != want {
				t.Fatalf("source %s got %q want %q", observation.Case, got, want)
			}
		}
		check()
		observed, e := readSharedSystemConfigRow(context.Background(), a.Store.DB)
		if e != nil {
			t.Fatal(e)
		}
		stored, e := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", []byte(observed.sealed))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(observation.Input.Rules, []byte(`\ud800`)) && !bytes.Contains(stored, []byte(`\ud800`)) {
			t.Fatal("source codepoint fact lost")
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
		if e != nil {
			t.Fatal(e)
		}
		p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
		if p == nil {
			t.Fatal("cold worker absent")
		}
		check()
	}
	// The native editor's text adapter must preserve unpaired code points too,
	// never round-trip through Go's replacement-character string decoder.
	response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"A => B\n\ud800"}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	a.wg.Wait()
	events := systemConfigEvents(t, p)
	if len(events) != 1 || !bytes.Contains(events[0], []byte(`\ud800`)) {
		t.Fatal("native config.updated lost source codepoint", events)
	}
	if got, e := a.prepareRecognitionTitleChecked(context.Background(), "A"); e != nil || got != "B" {
		t.Fatal(got, e)
	}
	row, e := readSharedSystemConfigRow(context.Background(), a.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", []byte(row.sealed))
	if e != nil || !bytes.Contains(stored, []byte(`\ud800`)) {
		t.Fatal(string(stored), e)
	}
}
