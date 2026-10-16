package releasemeta

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
)

func streamMetaInfoOracle(t *testing.T, path string, visit func(string, json.RawMessage)) {
	t.Helper()
	file, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	zip, e := gzip.NewReader(file)
	if e != nil {
		t.Fatal(e)
	}
	defer zip.Close()
	decoder := json.NewDecoder(zip)
	for {
		var record struct {
			Record string
			Data   json.RawMessage
		}
		e = decoder.Decode(&record)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		visit(record.Record, record.Data)
	}
}
func decodeMetaInfoReference(t *testing.T, raw json.RawMessage, out any) {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(out); e != nil {
		t.Fatal(e)
	}
}
func checkMetaInfoReference(t *testing.T, name string, got, want any) {
	t.Helper()
	a, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(want)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("%s\ngot:%s\nwant:%s", name, a, b)
	}
}

type metaInfoTitleReference struct {
	Name         string
	Status       string `json:"reference_status"`
	Error, Title string
	Subtitle     *string
	Mode         string
	Global       []string  `json:"global_words"`
	Groups       []string  `json:"release_groups"`
	Custom       []string  `json:"customization"`
	Local        *[]string `json:"local_words"`
	Result       *struct {
		Kind       string
		Keys       []string `json:"instance_keys"`
		Attributes map[string]any
		Dict       map[string]any `json:"to_dict"`
		Derived    map[string]any
	}
}

func TestCompleteInstalledMetaInfoTitleObjects(t *testing.T) {
	testCompleteTitleReference(t, "../../testdata/plugins/metainfo-title-oracle.ndjson.gz", 1166, 8)
}
func TestCompleteInstalledMetaInfoVideoObjects(t *testing.T) {
	testCompleteTitleReference(t, "../../testdata/plugins/metainfo-video-oracle.ndjson.gz", 1516, 0)
}
func testCompleteTitleReference(t *testing.T, path string, wantCount, wantNonterm int) {
	var platforms map[string]string
	count, nonterm := 0, 0
	streamMetaInfoOracle(t, path, func(kind string, raw json.RawMessage) {
		if kind == "header" {
			var header struct {
				Platforms map[string]string `json:"streaming_platforms"`
			}
			decodeMetaInfoReference(t, raw, &header)
			platforms = header.Platforms
			return
		}
		if kind != "case" {
			t.Fatal(kind)
		}
		var sample metaInfoTitleReference
		decodeMetaInfoReference(t, raw, &sample)
		count++
		if sample.Status == "non_terminating_within_2_seconds" {
			nonterm++
		}
		t.Run(sample.Name, func(t *testing.T) {
			parser, e := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: sample.Mode, CustomWords: sample.Global, ReleaseGroups: sample.Groups, Customization: sample.Custom, MediaExts: strings.Fields(".mp4 .mkv .avi .mov .ts .iso .m2ts .wmv .flv .srt .ass .ssa .sub .vtt .mp3 .flac .aac .wav")}, StreamingPlatforms: platforms})
			if e != nil {
				t.Fatal(e)
			}
			object, e := parser.ParseTitle(sample.Title, sample.Subtitle, sample.Local)
			if sample.Result == nil {
				want := sample.Error
				if sample.Status == "non_terminating_within_2_seconds" {
					want = "source_non_terminating"
				}
				if want == "" || e == nil || e.Error() != want {
					t.Fatalf("missing/mismatched original state: got %v want %s status %s", e, want, sample.Status)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			checkMetaInfoReference(t, "kind", object.Kind, sample.Result.Kind)
			attrs := map[string]any{}
			for k := range sample.Result.Attributes {
				attrs[k] = object.Get(k)
			}
			checkMetaInfoReference(t, "all attributes", attrs, sample.Result.Attributes)
			derived, e := object.Derived()
			if e != nil {
				t.Fatal(e)
			}
			checkMetaInfoReference(t, "all derived fields", derived, sample.Result.Derived)
			dict, e := object.ToDict()
			if e != nil {
				t.Fatal(e)
			}
			checkMetaInfoReference(t, "full source to_dict", dict, sample.Result.Dict)
			keys := []string{}
			for k := range object.Instance {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			checkMetaInfoReference(t, "instance membership", keys, sample.Result.Keys)
			encoded, e := object.EncodeState()
			if e != nil {
				t.Fatal(e)
			}
			restored, e := DecodeMetaObjectState(encoded)
			if e != nil {
				t.Fatal(e)
			}
			restoredDict, e := restored.ToDict()
			if e != nil {
				t.Fatal(e)
			}
			checkMetaInfoReference(t, "state restored source to_dict", restoredDict, sample.Result.Dict)
			for k := range object.Instance {
				if !restored.Has(k) {
					t.Fatal("lost presence", k)
				}
			}
		})
	})
	if count != wantCount || nonterm != wantNonterm {
		t.Fatalf("incomplete complete-object reference: %d/%d source nontermination %d/%d", count, wantCount, nonterm, wantNonterm)
	}
}

func TestCompleteOriginalMetaBaseMethods(t *testing.T) {
	makeObject := func(fields map[string]any) *MetaObject {
		o := EmptyMetaObject()
		o.Set("org_string", "Synthetic")
		o.Set("subtitle", nil)
		o.Set("isfile", false)
		for k, v := range fields {
			o.Set(k, v)
		}
		return o
	}
	methods, bits, steps := 0, 0, 0
	streamMetaInfoOracle(t, "../../testdata/plugins/metainfo-mutators-oracle.ndjson.gz", func(kind string, raw json.RawMessage) {
		if kind == "header" {
			return
		}
		if kind == "bit" {
			var c struct {
				Text   string
				Result *string
			}
			decodeMetaInfoReference(t, raw, &c)
			checkMetaInfoReference(t, "original extract_video_bit "+c.Text, ExtractVideoBit(c.Text), c.Result)
			bits++
			return
		}
		if kind != "method" {
			t.Fatal(kind)
		}
		var c struct {
			Name    string
			Initial map[string]any
			Steps   []struct {
				Action struct {
					Method string
					Args   []any
				}
				Value any
				Error string
				State struct {
					Keys    []string       `json:"instance_keys"`
					Attrs   map[string]any `json:"attributes"`
					Dict    map[string]any `json:"to_dict"`
					Derived map[string]any
				}
			}
		}
		decodeMetaInfoReference(t, raw, &c)
		methods++
		steps += len(c.Steps)
		t.Run(c.Name, func(t *testing.T) {
			o := makeObject(c.Initial)
			for _, s := range c.Steps {
				var value any
				var e error
				a := s.Action.Args
				switch s.Action.Method {
				case "name":
					e = o.SetName(a[0])
				case "set_season":
					e = o.SetSeason(a[0])
				case "set_episode":
					e = o.SetEpisode(a[0])
				case "set_episodes":
					e = o.SetEpisodes(a[0], a[1])
				case "is_in_season":
					value, e = o.IsInSeason(a[0])
				case "is_in_episode":
					value, e = o.IsInEpisode(a[0])
				case "merge":
					o.MergeLegacy(makeObject(a[0].(map[string]any)))
				default:
					t.Fatal(s.Action.Method)
				}
				gotError := ""
				if e != nil {
					gotError = e.Error()
					value = nil
				}
				checkMetaInfoReference(t, "source error "+s.Action.Method, gotError, s.Error)
				checkMetaInfoReference(t, "result", value, s.Value)
				attrs := map[string]any{}
				for k := range s.State.Attrs {
					attrs[k] = o.Get(k)
				}
				checkMetaInfoReference(t, "all attributes after operation", attrs, s.State.Attrs)
				derived, e := o.Derived()
				if e != nil {
					t.Fatal(e)
				}
				checkMetaInfoReference(t, "derived after operation", derived, s.State.Derived)
				dict, e := o.ToDict()
				if e != nil {
					t.Fatal(e)
				}
				checkMetaInfoReference(t, "dict after operation", dict, s.State.Dict)
				keys := []string{}
				for k := range o.Instance {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				checkMetaInfoReference(t, "membership after operation", keys, s.State.Keys)
			}
		})
	})
	if methods != 144 || steps != 354 || bits != 1176 {
		t.Fatalf("incomplete source methods %d/%d/%d", methods, steps, bits)
	}
}
func TestCompleteOriginalRustMerge(t *testing.T) {
	makeObject := func(values map[string]any) *MetaObject {
		o := EmptyMetaObject()
		for k, v := range values {
			if k == "media_type" {
				k = "type"
			}
			o.Set(k, v)
		}
		return o
	}
	count := 0
	streamMetaInfoOracle(t, "../../testdata/plugins/metainfo-rust-merge-oracle.ndjson.gz", func(kind string, raw json.RawMessage) {
		if kind == "header" {
			return
		}
		if kind != "case" {
			t.Fatal(kind)
		}
		count++
		var c struct {
			Name                   string
			Target, Source, Result map[string]any
		}
		decodeMetaInfoReference(t, raw, &c)
		t.Run(c.Name, func(t *testing.T) {
			o := makeObject(c.Target)
			o.MergeRust(makeObject(c.Source))
			for k, want := range c.Result {
				if k == "media_type" {
					k = "type"
				}
				checkMetaInfoReference(t, k, o.Get(k), want)
			}
		})
	})
	if count != 162 {
		t.Fatal("incomplete Rust original merge", count)
	}
}
