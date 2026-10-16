package releasemeta

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestInstalledLegacyAnimeFullPipelineWrapper(t *testing.T) {
	data, err := os.ReadFile("../../testdata/plugins/release-meta-legacy-anime-wrapper-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string  `json:"name"`
			Title    string  `json:"title"`
			Subtitle *string `json:"subtitle"`
			Options  Options `json:"options"`
			Status   string  `json:"reference_status"`
			Legacy   struct {
				Error  string   `json:"error"`
				Result Metadata `json:"result"`
			} `json:"legacy"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			sample.Options.SourceMode = "legacy"
			got, err := Parse(sample.Title, sample.Subtitle, sample.Options)
			if sample.Status == "non_terminating_within_2_seconds" {
				if err == nil || err.Error() != "source_non_terminating" {
					t.Fatalf("source cannot advance: got %v", err)
				}
				return
			}
			if sample.Legacy.Error != "" {
				if err == nil {
					t.Fatal("source rejected but Go accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, sample.Legacy.Result) {
				t.Fatalf("title=%q\ngot:%+v\nwant:%+v", sample.Title, got, sample.Legacy.Result)
			}
		})
	}
}

func TestInstalledLegacyChineseNumberControlDependencies(t *testing.T) {
	data, err := os.ReadFile("../../testdata/plugins/release-meta-legacy-cn-number-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Input  string `json:"input"`
			Error  string `json:"error"`
			Result *int   `json:"result"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for i, sample := range oracle.Cases {
		valid := legacyValidateChineseNumber(sample.Input) == nil
		if valid != (sample.Error == "") {
			t.Errorf("case%d input=%q valid=%t want error%q", i, sample.Input, valid, sample.Error)
		}
		if sample.Result != nil {
			got, ok := legacyChineseNumber(sample.Input)
			if !ok || got != *sample.Result {
				t.Errorf("case%d input=%q got %d/%t want%d", i, sample.Input, got, ok, *sample.Result)
			}
		}
	}
}

func TestActualInstalledLegacyRoutingCharacterBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../testdata/plugins/releasemeta-legacy-reference/installed-regex-routing.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Digit   [][2]rune         `json:"unicode_digit"`
		Space   [][2]rune         `json:"unicode_space"`
		Letters map[string][]rune `json:"casefold_letters"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	contains := func(r rune, ranges [][2]rune) bool {
		for _, item := range ranges {
			if r >= item[0] && r <= item[1] {
				return true
			}
		}
		return false
	}
	fold := func(r rune, letter string) bool {
		for _, v := range source.Letters[letter] {
			if r == v {
				return true
			}
		}
		return false
	}
	for _, pair := range source.Digit {
		for _, r := range []rune{pair[0] - 1, pair[0], pair[1], pair[1] + 1} {
			want := contains(r, source.Digit) || fold(r, "v")
			title := "[Group] Example Show - " + string(r) + " [1080p]"
			if got := isLegacyAnime(title); got != want {
				t.Fatalf("route decimal U+%X got%t want%t", r, got, want)
			}
		}
	}
	for _, r := range []rune{'I', 'i', 'ı', 'İ', 'V', 'v', 'X', 'x', 'P', 'p', 'ſ'} {
		want := fold(r, "I") || fold(r, "V") || fold(r, "X") || fold(r, "P")
		if got := isLegacyAnime("[" + string(r) + "][1080p]"); got != want {
			t.Fatalf("route fold U+%X got%t want%t", r, got, want)
		}
	}
	for _, pair := range source.Space {
		for _, r := range []rune{pair[0] - 1, pair[0], pair[1], pair[1] + 1} {
			want := contains(r, source.Space)
			if got := isLegacyAnime("[Group] Example Show" + string(r) + "- 01 [1080p]"); got != want {
				t.Fatalf("route whitespace U+%X got%t want%t", r, got, want)
			}
		}
	}
}

func TestActualInstalledLegacyCompleteRouting(t *testing.T) {
	file, err := os.Open("../../testdata/plugins/release-meta-legacy-route-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		if key != "cases" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
		for decoder.More() {
			var source struct {
				Title string `json:"title"`
				Anime bool   `json:"is_anime"`
			}
			if err := decoder.Decode(&source); err != nil {
				t.Fatal(err)
			}
			count++
			if got := isLegacyAnime(source.Title); got != source.Anime {
				t.Fatalf("source complete route mismatch; title=%q got%t want%t", source.Title, got, source.Anime)
			}
		}
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	if count < 10000 {
		t.Fatalf("incomplete route reference:%d", count)
	}
}
