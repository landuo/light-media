package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"light-media/internal/media"
	"light-media/internal/metadata"
)

// Entirely synthetic fixtures. live_request below is a supplied label used to
// exercise parsing; this test never calls a provider or claims live evidence.
const testID = "00000000-0000-4000-8000-000000000001"

func pint(v int) *int          { return &v }
func pstring(v string) *string { return &v }
func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func sampleSource() map[string]metadata.Result {
	out := map[string]metadata.Result{}
	for i, s := range specs {
		year := 2007
		date := "2007-01-02"
		title := s.title
		id := s.id
		if s.kind == "movie" {
			year, date = 2026, "2026-02-14"
		}
		if s.kind == "episode" {
			year, date, title, id = 2014, "2014-01-02", "Synthetic episode "+strconv.Itoa(i), int64(900000000+i)
		}
		r := metadata.Result{Sample: s.name, Type: s.kind, RequestedSourceID: s.id, Status: "ok", Source: "tmdb", SourceID: id, Title: title, Year: year, Date: date}
		if s.kind == "episode" {
			r.ParentSourceID = s.id
			r.ParentContext = "verified_series_and_fixed_request_route"
			r.Season = s.season
			r.Episode = s.episode
		}
		out[s.name] = r
	}
	return out
}
func sourceFiles(t *testing.T, src map[string]metadata.Result) ([]byte, manifest, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r := report{Report: metadata.Report{Status: "ok", Evidence: "live_request", Requests: 5}}
	for _, s := range specs {
		r.Results = append(r.Results, src[s.name])
	}
	rb := encode(t, r)
	m := manifest{Status: "generated", Validation: "supplied_validated_report", InputEvidence: "live_request", InputSHA: hash(rb), Authenticity: "not_independently_verified", Readback: "unverified", NFOCount: 5}
	for _, s := range specs {
		v := src[s.name]
		kind := s.kind
		if kind == "tv" {
			kind = "tvshow"
		}
		n := media.NFOMetadata{Kind: kind, Title: v.Title, Year: v.Year, ShowTitle: s.show, Season: v.Season, Episode: v.Episode, UniqueIDs: []media.UniqueID{{Type: "tmdb", Default: true, Value: strconv.FormatInt(v.SourceID, 10)}}}
		if s.kind == "episode" {
			n.Aired = v.Date
		}
		b, e := media.EncodeNFO(n)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, s.path)
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		f := manifestFile{Sample: s.name, Path: s.path, SourceKey: sourceKey(v), Season: v.Season, Episode: v.Episode, Date: v.Date, Year: v.Year, Bytes: len(b), SHA: hash(b)}
		if s.kind == "episode" {
			f.ParentKey = "tmdb:tv:" + strconv.FormatInt(v.ParentSourceID, 10)
		}
		m.Files = append(m.Files, f)
	}
	return rb, m, root
}
func observationFor(src map[string]metadata.Result) (observation, fileEvidence) {
	root := "/vol2/1000/media/" + prefix + testID
	o := observation{Status: "observed", Action: "observe", ExperimentID: testID, OwnedGUID: "owned-library", Root: root, Baseline: true}
	f := fileEvidence{ExperimentID: testID, OwnedGUID: o.OwnedGUID, Root: root}
	for _, s := range specs {
		r := src[s.name]
		v := item{GUID: s.name, Type: map[string]string{"movie": "Movie", "tv": "TV", "episode": "Episode"}[s.kind], Title: r.Title, Ancestor: o.OwnedGUID, Parent: pstring(""), Scope: true, Trim: "opaque-synthetic-id"}
		if s.kind != "episode" {
			v.Release = strconv.Itoa(r.Year) + "-01-01"
		} else {
			tv := "series-bigbang"
			if s.id == 206484 {
				tv = "series-zhuxian"
			}
			season := item{GUID: tv + "-season", Type: "Season", Title: "Season", Ancestor: o.OwnedGUID, Parent: pstring(tv), Scope: true, Season: pint(s.season)}
			o.Items = append(o.Items, season)
			v.Parent = pstring(season.GUID)
			v.Season = pint(s.season)
			v.Episode = pint(s.episode)
			v.Air = r.Date
			v.Release = r.Date
			v.TVTitle = s.show
		}
		o.Items = append(o.Items, v)
		if s.kind != "tv" {
			p := strings.TrimSuffix(s.path, ".nfo") + ".mkv"
			f.Bindings = append(f.Bindings, binding{ItemGUID: v.GUID, Relative: p, Actual: root + "/" + p})
		}
	}
	return o, f
}
func TestSuppliedAssociationDoesNotAuthenticateSourceOrOpaqueID(t *testing.T) {
	src := sampleSource()
	rb, m, root := sourceFiles(t, src)
	loaded, e := inputs(rb, encode(t, m), root)
	if e != nil {
		t.Fatal(e)
	}
	o, f := observationFor(loaded)
	r := compare(testID, loaded, o, f)
	if r.Status != "passed" || r.DirectStandardIDReadback != "unverified" || r.SourceAuthenticity != "not_independently_verified" || r.NetworkRequests != 0 {
		t.Fatalf("wrong claims %+v", r)
	}
	for _, x := range r.IDs {
		if x.Status != "unverified" || x.TrimState != "opaque" {
			t.Fatal(x)
		}
	}
	// Exact provider date is not inferred from a root's year-only NFO.
	for i := range o.Items {
		if o.Items[i].Type == "Movie" {
			o.Items[i].Release = "2026-02-14"
		}
	}
	if compare(testID, loaded, o, f).Status != "passed" {
		t.Fatal("valid root year rejected because date was not Jan 1")
	}
}
func TestTypedNumbersRemainSeparateAndEpisodeUsesOwnID(t *testing.T) {
	src := sampleSource()
	v := src["episode-bigbang"]
	v.SourceID = 1418
	src[v.Sample] = v
	rb, m, root := sourceFiles(t, src)
	loaded, e := inputs(rb, encode(t, m), root)
	if e != nil {
		t.Fatal(e)
	}
	if sourceKey(loaded["episode-bigbang"]) == sourceKey(loaded["series-bigbang"]) {
		t.Fatal("cross-type numeric identity merged")
	}
	// A mismatched episode XML even with a recomputed manifest hash is refused.
	path := filepath.Join(root, specs[4].path)
	b, _ := os.ReadFile(path)
	b = bytes.Replace(b, []byte("900000004"), []byte("206484"), 1)
	os.WriteFile(path, b, 0600)
	m.Files[4].Bytes = len(b)
	m.Files[4].SHA = hash(b)
	if _, e := inputs(rb, encode(t, m), root); e == nil || e.Error() != "NFO_SOURCE_MISMATCH" {
		t.Fatal("episode parent substituted for own source ID", e)
	}
}
func TestInputBoundaries(t *testing.T) {
	for _, name := range []string{"unconfigured", "wrong_movie_id", "wrong_episode_parent", "wrong_season", "duplicate_episode", "manifest_source", "report_hash", "xml_hash", "xml_identity", "path", "alias", "synthetic_label"} {
		t.Run(name, func(t *testing.T) {
			src := sampleSource()
			rb, m, root := sourceFiles(t, src)
			var r report
			json.Unmarshal(rb, &r)
			switch name {
			case "unconfigured":
				r.Status = "unverified"
				r.Requests = 0
			case "wrong_movie_id":
				r.Results[0].SourceID = 123
			case "wrong_episode_parent":
				r.Results[2].ParentSourceID = 206484
			case "wrong_season":
				r.Results[2].Season = 1
			case "duplicate_episode":
				r.Results[4].SourceID = r.Results[2].SourceID
			case "manifest_source":
				m.Files[2].SourceKey = "tmdb:episode:1418"
			case "report_hash":
				m.InputSHA = "wrong"
			case "xml_hash":
				os.WriteFile(filepath.Join(root, specs[0].path), []byte("changed"), 0600)
			case "xml_identity":
				p := filepath.Join(root, specs[0].path)
				b, _ := os.ReadFile(p)
				b = bytes.Replace(b, []byte("1602727"), []byte("1602728"), 1)
				os.WriteFile(p, b, 0600)
				m.Files[0].SHA = hash(b)
				m.Files[0].Bytes = len(b)
			case "path":
				m.Files[0].Path = "../../foreign.nfo"
			case "synthetic_label":
				r.Evidence = "synthetic_http_roundtrip"
			}
			if name == "unconfigured" || name == "wrong_movie_id" || name == "wrong_episode_parent" || name == "wrong_season" || name == "duplicate_episode" || name == "synthetic_label" {
				rb = encode(t, r)
				m.InputSHA = hash(rb)
			}
			if name == "alias" {
				rb = append([]byte(`{"STATUS":"unverified",`), rb[1:]...)
				m.InputSHA = hash(rb)
			}
			if _, e := inputs(rb, encode(t, m), root); e == nil {
				t.Fatal("invalid source/manifest/NFO accepted")
			}
		})
	}
}
func TestActualWrongScopeParentEpisodeAndFileFail(t *testing.T) {
	for _, name := range []string{"root", "other_library", "missing_scope", "root_parent", "episode_parent", "season_parent", "missing_parent", "season", "episode", "title", "year", "date", "file_path", "file_guid", "duplicate_item", "missing_file"} {
		t.Run(name, func(t *testing.T) {
			src := sampleSource()
			o, f := observationFor(src)
			var movie, episode, season *item
			for i := range o.Items {
				switch o.Items[i].GUID {
				case "movie-nightking":
					movie = &o.Items[i]
				case "episode-bigbang":
					episode = &o.Items[i]
				case "series-bigbang-season":
					season = &o.Items[i]
				}
			}
			switch name {
			case "root":
				o.Root += "-other"
			case "other_library":
				episode.Ancestor = "foreign"
			case "missing_scope":
				movie.Scope = false
			case "root_parent":
				movie.Parent = pstring(o.OwnedGUID)
			case "episode_parent":
				episode.Parent = pstring("series-zhuxian-season")
			case "season_parent":
				season.Parent = pstring("series-zhuxian")
			case "missing_parent":
				episode.Parent = nil
			case "season":
				episode.Season = pint(1)
			case "episode":
				episode.Episode = pint(1)
			case "title":
				movie.Title = "other"
			case "year":
				movie.Release = "2025-01-01"
			case "date":
				episode.Air = "2015-01-02"
			case "file_path":
				f.Bindings[0].Actual = "/vol2/1000/media/production/夜王.mkv"
			case "file_guid":
				f.Bindings[0].ItemGUID = "foreign-item"
			case "duplicate_item":
				o.Items[0] = o.Items[1]
			case "missing_file":
				f.Bindings = f.Bindings[:2]
			}
			if compare(testID, src, o, f).Status != "failed" {
				t.Fatal("bad observed evidence accepted")
			}
		})
	}
}
func TestDirectReadbackRequiresExplicitNamespaceAndConsistency(t *testing.T) {
	src := sampleSource()
	o, f := observationFor(src)
	for i := range o.Items {
		v := &o.Items[i]
		if r, ok := src[v.GUID]; ok {
			v.TMDB = json.RawMessage(strconv.FormatInt(r.SourceID, 10))
			v.Source = "tmdb"
			v.SourceID = json.RawMessage(`"` + strconv.FormatInt(r.SourceID, 10) + `"`)
		}
	}
	if r := compare(testID, src, o, f); r.Status != "passed" || r.DirectStandardIDReadback != "verified" {
		t.Fatal(r)
	}
	for _, bad := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`0`), json.RawMessage(`"tm1602727"`), json.RawMessage(`1602728`)} {
		o.Items[0].TMDB = bad
		r := compare(testID, src, o, f)
		if r.Status != "failed" || r.DirectStandardIDReadback == "verified" {
			t.Fatal("conflicting explicit assertion accepted", r)
		}
	}
	if directID(item{Trim: "tm1602727", Source: "trim_id", SourceID: json.RawMessage(`"tm1602727"`)}, 1602727) != "unverified" {
		t.Fatal("opaque prefix inferred")
	}
}
func TestBoundedSymlinkReaderAndSchema(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "source")
	os.WriteFile(p, []byte("ok"), 0600)
	link := filepath.Join(root, "link")
	os.Symlink(p, link)
	if _, e := readFile(link); e == nil {
		t.Fatal("symlink accepted")
	}
	var r report
	for _, b := range []string{`{"status":"ok","status":"failed"}`, `{"STATUS":"ok"}`, `{"ſtatus":"ok"}`, `{} {}`, strings.Repeat(" ", limit+1)} {
		if decode([]byte(b), &r, true) == nil {
			t.Fatal("ambiguous/oversized JSON accepted")
		}
	}
}

func TestObservedInvalidIDSidebandsCannotBecomeMissing(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"missing", `{}`, "unverified"},
		{"invalid_explicit_tmdb", `{"tmdb_id_state":"string"}`, "conflict"},
		{"null_explicit_tmdb", `{"tmdb_id_state":"null"}`, "conflict"},
		{"valid_plus_invalid_tmdb", `{"tmdb_id":1418,"tmdb_id_state":"string"}`, "conflict"},
		{"tmdb_bad_source_id", `{"source":"tmdb","source_id_state":"object"}`, "conflict"},
		{"themoviedb_bad_source_id", `{"source":"themoviedb","source_id_state":"null"}`, "conflict"},
		{"tmdb_id_bad_source_id", `{"source":"tmdb_id","source_id_state":"boolean"}`, "conflict"},
		{"contradictory_namespace_state", `{"source":"tmdb","source_id":1418,"source_state":"string"}`, "conflict"},
		{"invalid_unknown_source", `{"source_state":"string","source_id_state":"object"}`, "unverified"},
		{"bare_numeric_unknown_source", `{"source_state":"string","source_id":1418}`, "unverified"},
		{"trim_opaque", `{"source":"trim_id","source_id":"tt1418"}`, "unverified"},
		{"distinct_namespaces", `{"tmdb_id":1418,"source":"trim_id","source_id":"tt9876"}`, "verified"},
		{"explicit_tmdb_unknown_other_namespace", `{"tmdb_id":1418,"source_state":"string","source_id_state":"object"}`, "verified"},
		{"explicit_tmdb_invalid_trim_value", `{"tmdb_id":1418,"source":"trim_id","source_id_state":"string"}`, "verified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var v item
			if e := decode([]byte(tc.body), &v, false); e != nil {
				t.Fatal(e)
			}
			if got := directID(v, 1418); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	for _, namespace := range []string{"tmdb", "themoviedb", "tmdb_id"} {
		var v item
		b := []byte(`{"source":"` + namespace + `","source_id":"1418"}`)
		if decode(b, &v, false) != nil || directID(v, 1418) != "verified" {
			t.Fatal("known namespace rejected", namespace)
		}
	}
	// Exercise the actual comparison path after JSON decoding, not just helper
	// calls: an observer's invalid tmdb_id must fail association, not disappear.
	src := sampleSource()
	o, f := observationFor(src)
	b := encode(t, o)
	b = bytes.Replace(b, []byte(`"guid":"movie-nightking"`), []byte(`"tmdb_id_state":"object","guid":"movie-nightking"`), 1)
	var parsed observation
	if decode(b, &parsed, false) != nil {
		t.Fatal("valid sanitized observation rejected")
	}
	r := compare(testID, src, parsed, f)
	if r.Status != "failed" || r.DirectStandardIDReadback != "unverified" {
		t.Fatal("invalid explicit ID summarized as missing", r)
	}
	found := false
	for _, v := range r.IDs {
		if v.Sample == "movie-nightking" && v.Status == "conflict" {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid explicit field distinction lost")
	}
}

func TestFrozenV1AndV2PublishingProfiles(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		name := "v1_uniqueid_only"
		if v2 {
			name = "v2_tmdbid_and_uniqueid"
		}
		t.Run(name, func(t *testing.T) {
			src := sampleSource()
			rb, m, root := sourceFiles(t, src)
			for i, s := range specs {
				b, e := expectedNFO(src[s.name], s, v2)
				if e != nil {
					t.Fatal(e)
				}
				if bytes.Contains(b, []byte("<tmdbid>")) != v2 {
					t.Fatal("wrong profile")
				}
				if e = os.WriteFile(filepath.Join(root, s.path), b, 0600); e != nil {
					t.Fatal(e)
				}
				m.Files[i].Bytes = len(b)
				m.Files[i].SHA = hash(b)
			}
			if _, e := inputs(rb, encode(t, m), root); e != nil {
				t.Fatal("known profile refused", e)
			}
		})
	}
	// Freeze an independent v1 byte fixture; the shared encoder may now emit v2.
	src := sampleSource()
	b, e := expectedNFO(src["movie-nightking"], specs[0], false)
	if e != nil {
		t.Fatal(e)
	}
	want := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<movie>\n  <title>夜王</title>\n  <year>2026</year>\n  <uniqueid type=\"tmdb\" default=\"true\">1602727</uniqueid>\n</movie>\n"
	if string(b) != want {
		t.Fatal("historical exact v1 bytes changed")
	}
}

func TestV2ConflictingOrAdditionalTMDBIDRefused(t *testing.T) {
	for _, name := range []string{"wrong_movie_id", "parent_as_episode_id", "duplicate_tmdbid", "unrecognized_tag", "different_position", "extra_field", "wrong_uniqueid"} {
		t.Run(name, func(t *testing.T) {
			src := sampleSource()
			rb, m, root := sourceFiles(t, src)
			index := 0
			if name == "parent_as_episode_id" {
				index = 2
			}
			s := specs[index]
			b, e := expectedNFO(src[s.name], s, true)
			if e != nil {
				t.Fatal(e)
			}
			switch name {
			case "wrong_movie_id":
				b = bytes.Replace(b, []byte("<tmdbid>1602727</tmdbid>"), []byte("<tmdbid>1602728</tmdbid>"), 1)
			case "parent_as_episode_id":
				b = bytes.Replace(b, []byte("<tmdbid>900000002</tmdbid>"), []byte("<tmdbid>1418</tmdbid>"), 1)
			case "duplicate_tmdbid":
				b = bytes.Replace(b, []byte("<tmdbid>1602727</tmdbid>"), []byte("<tmdbid>1602727</tmdbid>\n  <tmdbid>1602727</tmdbid>"), 1)
			case "unrecognized_tag":
				b = bytes.ReplaceAll(b, []byte("tmdbid"), []byte("tmdbId"))
			case "different_position":
				line := []byte("  <tmdbid>1602727</tmdbid>\n")
				b = bytes.Replace(b, line, nil, 1)
				b = bytes.Replace(b, []byte("</movie>"), append(line, []byte("</movie>")...), 1)
			case "extra_field":
				b = bytes.Replace(b, []byte("</movie>"), []byte("  <plot>unsupplied</plot>\n</movie>"), 1)
			case "wrong_uniqueid":
				b = bytes.Replace(b, []byte(`default="true">1602727</uniqueid>`), []byte(`default="true">1602728</uniqueid>`), 1)
			}
			if e = os.WriteFile(filepath.Join(root, s.path), b, 0600); e != nil {
				t.Fatal(e)
			}
			m.Files[index].Bytes = len(b)
			m.Files[index].SHA = hash(b)
			if _, e := inputs(rb, encode(t, m), root); e == nil || e.Error() != "NFO_SOURCE_MISMATCH" {
				t.Fatal("conflict/unrecognized profile accepted", name, e)
			}
		})
	}
}
