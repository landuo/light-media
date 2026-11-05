package app

import (
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalMediaChineseTitleFixedSource(t *testing.T) {
	b, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/zhconv/title-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit string
		Cases  []struct {
			Kind, Title, Error string
			Input              json.RawMessage
		}
	}
	if err = json.Unmarshal(b, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 20 {
		t.Fatal("source binding")
	}
	for i, c := range oracle.Cases {
		got, err := sourceTMDBMediaInfo(c.Input, c.Kind, "image.tmdb.org", "")
		if c.Error != "" {
			if err == nil {
				t.Errorf("%d expected failed conversion", i)
			}
			continue
		}
		if err != nil {
			t.Errorf("%d: %v", i, err)
			continue
		}
		var info map[string]any
		if err = json.Unmarshal(got, &info); err != nil {
			t.Fatal(err)
		}
		if info["title"] != c.Title {
			t.Errorf("%d %s got title %q want %q", i, c.Kind, info["title"], c.Title)
		}
	}
}

func TestOriginalMediaChineseTitleLocaleGuardFixedSource(t *testing.T) {
	// tmdbapi.py:640 and :1978 explicitly enumerate these two locales, rather
	// than testing a language prefix or treating an explicit empty value as zh.
	raw := json.RawMessage(`{"id":88002,"title":"Owned","original_title":"Owned","original_language":"en","alternative_titles":{"titles":[{"iso_3166_1":"CN","title":"中文别名"}]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
	for _, locale := range []string{"zh", "zh-CN", "en", "zh-TW", "zh-Hans", "ZH", ""} {
		result, err := sourceTMDBMediaInfoLocale(raw, "movie", "image.tmdb.org", "", locale)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(result, &got); err != nil {
			t.Fatal(err)
		}
		want := "Owned"
		if locale == "zh" || locale == "zh-CN" {
			want = "中文别名"
		}
		if got["title"] != want {
			t.Errorf("%q: got %v want %s", locale, got["title"], want)
		}
	}
}
