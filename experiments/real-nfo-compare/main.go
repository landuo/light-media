// real-nfo-compare is a bounded offline M0 evidence comparator, not an API client.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"light-media/internal/media"
	"light-media/internal/metadata"
)

const limit = 2 << 20
const prefix = "light-media-m0-test-"

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var guidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var decimal = regexp.MustCompile(`^[1-9][0-9]*$`)

type spec struct {
	name, kind, title, path, show string
	id                            int64
	season, episode               int
}

var specs = []spec{
	{"movie-nightking", "movie", "夜王", "夜王 (2026)/夜王 (2026).nfo", "", 1602727, 0, 0},
	{"series-bigbang", "tv", "生活大爆炸", "生活大爆炸/tvshow.nfo", "", 1418, 0, 0},
	{"episode-bigbang", "episode", "", "生活大爆炸/Season 7/生活大爆炸 S07E17.nfo", "生活大爆炸", 1418, 7, 17},
	{"series-zhuxian", "tv", "诛仙", "诛仙/tvshow.nfo", "", 206484, 0, 0},
	{"episode-zhuxian", "episode", "", "诛仙/Season 4/诛仙 S04E05.nfo", "诛仙", 206484, 4, 5},
}

type report struct {
	metadata.Report
	RunID   string          `json:"run_id"`
	Logging json.RawMessage `json:"logging"`
}
type manifestFile struct {
	Sample    string `json:"sample"`
	Path      string `json:"relative_path"`
	SourceKey string `json:"source_key"`
	ParentKey string `json:"parent_source_key"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Date      string `json:"date"`
	Year      int    `json:"year"`
	Bytes     int    `json:"bytes"`
	SHA       string `json:"sha256"`
}
type manifest struct {
	Status        string         `json:"status"`
	Validation    string         `json:"validation"`
	InputEvidence string         `json:"input_evidence"`
	InputSHA      string         `json:"input_sha256"`
	Authenticity  string         `json:"input_source_authenticity"`
	Readback      string         `json:"fnos_readback"`
	Synthetic     bool           `json:"synthetic_input"`
	Requests      int            `json:"network_requests"`
	NFOCount      int            `json:"nfo_count"`
	VideoCount    int            `json:"video_count"`
	ImageCount    int            `json:"image_count"`
	ActorCount    int            `json:"actor_count"`
	Files         []manifestFile `json:"files"`
}
type item struct {
	GUID          string          `json:"guid"`
	Type          string          `json:"type"`
	Title         string          `json:"title"`
	TVTitle       string          `json:"tv_title"`
	Ancestor      string          `json:"ancestor_guid"`
	Parent        *string         `json:"parent_guid"`
	Scope         bool            `json:"scope_verified"`
	Season        *int            `json:"season_number"`
	Episode       *int            `json:"episode_number"`
	Release       string          `json:"release_date"`
	Air           string          `json:"air_date"`
	Year          *int            `json:"year"`
	Trim          string          `json:"trim_id"`
	TMDB          json.RawMessage `json:"tmdb_id,omitempty"`
	Source        string          `json:"source"`
	SourceID      json.RawMessage `json:"source_id,omitempty"`
	TMDBState     json.RawMessage `json:"tmdb_id_state,omitempty"`
	SourceIDState json.RawMessage `json:"source_id_state,omitempty"`
	SourceState   json.RawMessage `json:"source_state,omitempty"`
}
type observation struct {
	Status       string `json:"status"`
	Action       string `json:"action"`
	ExperimentID string `json:"experiment_id"`
	OwnedGUID    string `json:"owned_guid"`
	Root         string `json:"root"`
	Baseline     bool   `json:"baseline_unchanged"`
	Items        []item `json:"items"`
}
type binding struct {
	ItemGUID string `json:"item_guid"`
	Relative string `json:"relative_path"`
	Actual   string `json:"actual_path"`
}
type fileEvidence struct {
	ExperimentID string    `json:"experiment_id"`
	OwnedGUID    string    `json:"owned_guid"`
	Root         string    `json:"root"`
	Bindings     []binding `json:"bindings"`
}
type check struct {
	Sample string `json:"sample"`
	Field  string `json:"field"`
	Passed bool   `json:"passed"`
}
type identityReadback struct {
	Sample    string `json:"sample"`
	Status    string `json:"status"`
	TrimState string `json:"trim_id_state"`
}
type result struct {
	Status                   string             `json:"status"`
	Code                     string             `json:"code,omitempty"`
	Contract                 string             `json:"contract"`
	SourceAuthenticity       string             `json:"input_source_authenticity"`
	APIReadSuccess           bool               `json:"api_read_success"`
	DirectStandardIDReadback string             `json:"direct_standard_id_readback"`
	IDs                      []identityReadback `json:"identity_readback,omitempty"`
	Checks                   []check            `json:"checks,omitempty"`
	NetworkRequests          int                `json:"network_requests"`
	Note                     string             `json:"note"`
}

func newResult() result {
	return result{Status: "failed", Contract: "real_nfo_sample_association_v1", SourceAuthenticity: "not_independently_verified", DirectStandardIDReadback: "unverified", Note: "Offline supplied-evidence association only. Passed does not authenticate provider responses or prove direct standard-ID readback, shared-cache isolation, or video content identity."}
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func sourceKey(r metadata.Result) string {
	return "tmdb:" + r.Type + ":" + strconv.FormatInt(r.SourceID, 10)
}
func safeText(s string) bool {
	if s == "" || len(s) > 512 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func jsonKeys(d *json.Decoder, depth int) error {
	if depth > 24 {
		return errors.New("INVALID_JSON")
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || !keyPattern.MatchString(s) || seen[s] {
				return errors.New("INVALID_JSON")
			}
			seen[s] = true
		}
		if e := jsonKeys(d, depth+1); e != nil {
			return e
		}
	}
	_, e = d.Token()
	return e
}
func decode(b []byte, v any, strict bool) error {
	if len(b) == 0 || len(b) > limit {
		return errors.New("INVALID_JSON")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if jsonKeys(d, 0) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("INVALID_JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	if strict {
		d.DisallowUnknownFields()
	}
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("INVALID_JSON")
	}
	return nil
}
func readFile(p string) ([]byte, error) {
	abs, e := filepath.Abs(p)
	if e != nil {
		return nil, errors.New("INPUT_UNAVAILABLE")
	}
	resolved, e := filepath.EvalSymlinks(abs)
	if e != nil || resolved != abs {
		return nil, errors.New("INPUT_SYMLINK_OR_UNAVAILABLE")
	}
	f, e := os.Open(abs)
	if e != nil {
		return nil, errors.New("INPUT_UNAVAILABLE")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("INPUT_LIMIT_OR_TYPE")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || len(b) > limit {
		return nil, errors.New("INPUT_LIMIT_OR_TYPE")
	}
	return b, nil
}
func inputs(reportBytes, manifestBytes []byte, root string) (map[string]metadata.Result, error) {
	var r report
	var m manifest
	if decode(reportBytes, &r, true) != nil || decode(manifestBytes, &m, true) != nil {
		return nil, errors.New("INVALID_INPUT_SCHEMA")
	}
	if r.Status != "ok" || r.Evidence != "live_request" || r.Requests != 5 || len(r.Results) != 5 {
		return nil, errors.New("REPORT_NOT_VERIFIED")
	}
	if m.Status != "generated" || m.Validation != "supplied_validated_report" || m.InputEvidence != "live_request" || m.InputSHA != hash(reportBytes) || m.Authenticity != "not_independently_verified" || m.Readback != "unverified" || m.Synthetic || m.Requests != 0 || m.NFOCount != 5 || m.VideoCount != 0 || m.ImageCount != 0 || m.ActorCount != 0 || len(m.Files) != 5 {
		return nil, errors.New("MANIFEST_MISMATCH")
	}
	bySample := map[string]metadata.Result{}
	files := map[string]manifestFile{}
	seen := map[string]bool{}
	for _, v := range r.Results {
		if _, ok := bySample[v.Sample]; ok {
			return nil, errors.New("DUPLICATE_SAMPLE")
		}
		bySample[v.Sample] = v
	}
	for _, v := range m.Files {
		if _, ok := files[v.Sample]; ok {
			return nil, errors.New("DUPLICATE_FILE")
		}
		files[v.Sample] = v
	}
	for _, s := range specs {
		v, ok := bySample[s.name]
		if !ok || v.Status != "ok" || v.Reason != "" || v.Source != "tmdb" || v.Type != s.kind || v.RequestedSourceID != s.id || v.SourceID <= 0 || !safeText(v.Title) {
			return nil, errors.New("SOURCE_IDENTITY_MISMATCH")
		}
		date, e := time.Parse("2006-01-02", v.Date)
		if e != nil || date.Format("2006-01-02") != v.Date || v.Year != date.Year() || v.Year < 1800 || v.Year > 2199 {
			return nil, errors.New("SOURCE_DATE_MISMATCH")
		}
		if s.kind == "episode" {
			if v.ParentSourceID != s.id || v.Season != s.season || v.Episode != s.episode || (v.ParentContext != "verified_series_and_fixed_request_route" && v.ParentContext != "verified_response_show_id_and_request_route") {
				return nil, errors.New("SOURCE_PARENT_MISMATCH")
			}
		} else if v.SourceID != s.id || v.Title != s.title || v.ParentSourceID != 0 || v.ParentContext != "" || v.Season != 0 || v.Episode != 0 || (s.kind == "movie" && v.Year != 2026) {
			return nil, errors.New("SOURCE_IDENTITY_MISMATCH")
		}
		if seen[sourceKey(v)] {
			return nil, errors.New("DUPLICATE_TYPED_IDENTITY")
		}
		seen[sourceKey(v)] = true
		f, ok := files[s.name]
		parent := ""
		if s.kind == "episode" {
			parent = "tmdb:tv:" + strconv.FormatInt(v.ParentSourceID, 10)
		}
		if !ok || f.Path != s.path || f.SourceKey != sourceKey(v) || f.ParentKey != parent || f.Season != v.Season || f.Episode != v.Episode || f.Year != v.Year || f.Date != v.Date {
			return nil, errors.New("MANIFEST_IDENTITY_MISMATCH")
		}
		b, e := readFile(filepath.Join(root, filepath.FromSlash(s.path)))
		if e != nil {
			return nil, e
		}
		if len(b) != f.Bytes || hash(b) != f.SHA {
			return nil, errors.New("NFO_MANIFEST_HASH_MISMATCH")
		}

		v1, e := expectedNFO(v, s, false)
		if e != nil {
			return nil, errors.New("NFO_ENCODE_FAILED")
		}
		v2, e := expectedNFO(v, s, true)
		if e != nil {
			return nil, errors.New("NFO_ENCODE_FAILED")
		}
		var parsed struct {
			XMLName xml.Name
			IDs     []media.UniqueID `xml:"uniqueid"`
			TMDBIDs []string         `xml:"tmdbid"`
		}
		if xml.Unmarshal(b, &parsed) != nil || len(parsed.IDs) != 1 || len(parsed.TMDBIDs) > 1 || (len(parsed.TMDBIDs) == 1 && parsed.TMDBIDs[0] != strconv.FormatInt(v.SourceID, 10)) || (!bytes.Equal(b, v1) && !bytes.Equal(b, v2)) {
			return nil, errors.New("NFO_SOURCE_MISMATCH")
		}
	}
	return bySample, nil
}

// Freeze exactly the two published M0 profiles. Calling the shared encoder here
// would make later encoder changes silently invalidate historical v1 evidence.
// v2 adds only same-ID tmdbid immediately before uniqueid; no arbitrary XML,
// unknown tags, alternate formatting or reordered metadata is accepted.
func expectedNFO(v metadata.Result, s spec, withTMDBID bool) ([]byte, error) {
	d := struct {
		XMLName   xml.Name
		Title     string           `xml:"title"`
		Year      int              `xml:"year,omitempty"`
		Season    *int             `xml:"season,omitempty"`
		Episode   *int             `xml:"episode,omitempty"`
		ShowTitle string           `xml:"showtitle,omitempty"`
		Aired     string           `xml:"aired,omitempty"`
		TMDBID    string           `xml:"tmdbid,omitempty"`
		IDs       []media.UniqueID `xml:"uniqueid"`
	}{XMLName: xml.Name{Local: map[string]string{"movie": "movie", "tv": "tvshow", "episode": "episodedetails"}[s.kind]}, Title: v.Title, Year: v.Year, ShowTitle: s.show, IDs: []media.UniqueID{{Type: "tmdb", Default: true, Value: strconv.FormatInt(v.SourceID, 10)}}}
	if s.kind == "episode" {
		d.Season = &v.Season
		d.Episode = &v.Episode
		d.Aired = v.Date
	}
	if withTMDBID {
		d.TMDBID = strconv.FormatInt(v.SourceID, 10)
	}
	b, e := xml.MarshalIndent(d, "", "  ")
	if e != nil {
		return nil, e
	}
	return append(append([]byte(xml.Header), b...), '\n'), nil
}

func positiveID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var s string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &s) != nil {
			return 0, false
		}
	} else {
		s = string(raw)
	}
	if !decimal.MatchString(s) {
		return 0, false
	}
	n, e := strconv.ParseInt(s, 10, 64)
	return n, e == nil && n > 0
}

// A Trim ID is opaque. Only explicit TMDB namespace claims can verify readback.
func directID(v item, expected int64) string {
	// The observer omits unsafe raw values and emits a state instead. Presence
	// means explicitly returned but invalid, never "field was not returned".
	if len(v.TMDBState) > 0 {
		return "conflict"
	}
	standardSource := v.Source == "tmdb" || v.Source == "themoviedb" || v.Source == "tmdb_id"
	if standardSource && (len(v.SourceIDState) > 0 || len(v.SourceState) > 0) {
		return "conflict"
	}
	found := false
	if len(v.TMDB) > 0 {
		n, ok := positiveID(v.TMDB)
		if !ok || n != expected {
			return "conflict"
		}
		found = true
	}
	if standardSource {
		n, ok := positiveID(v.SourceID)
		if !ok || n != expected {
			return "conflict"
		}
		found = true
	}
	if found {
		return "verified"
	}
	return "unverified"
}
func exactInt(p *int, n int) bool { return p != nil && *p == n }
func compare(id string, source map[string]metadata.Result, o observation, f fileEvidence) result {
	out := newResult()
	out.APIReadSuccess = o.Status == "observed"
	root := "/vol2/1000/media/" + prefix + id
	if !uuidPattern.MatchString(id) || o.Status != "observed" || o.Action != "observe" || o.ExperimentID != id || !guidPattern.MatchString(o.OwnedGUID) || o.Root != root || !o.Baseline || len(o.Items) != 7 {
		out.Code = "OBSERVATION_SCOPE_INVALID"
		return out
	}
	if f.ExperimentID != id || f.OwnedGUID != o.OwnedGUID || f.Root != root || len(f.Bindings) != 3 {
		out.Code = "FILE_SCOPE_INVALID"
		return out
	}
	byGUID := map[string]item{}
	byPath := map[string]binding{}
	boundGUIDs := map[string]bool{}
	for _, v := range o.Items {
		if !guidPattern.MatchString(v.GUID) || !v.Scope || v.Ancestor != o.OwnedGUID || v.Parent == nil {
			out.Code = "ITEM_SCOPE_INVALID"
			return out
		}
		if _, ok := byGUID[v.GUID]; ok {
			out.Code = "DUPLICATE_ITEM"
			return out
		}
		byGUID[v.GUID] = v
	}
	for _, b := range f.Bindings {
		if _, ok := byPath[b.Relative]; ok || boundGUIDs[b.ItemGUID] || !guidPattern.MatchString(b.ItemGUID) {
			out.Code = "FILE_BINDING_INVALID"
			return out
		}
		byPath[b.Relative] = b
		boundGUIDs[b.ItemGUID] = true
	}
	add := func(sample, field string, pass bool) { out.Checks = append(out.Checks, check{sample, field, pass}) }
	matched := map[string]bool{}
	allDirect := true
	for _, s := range specs {
		expected := source[s.name]
		kind := map[string]string{"movie": "Movie", "tv": "TV", "episode": "Episode"}[s.kind]
		var matches []item
		for _, v := range o.Items {
			if v.Type == kind && v.Title == expected.Title {
				matches = append(matches, v)
			}
		}
		add(s.name, "unique_title_and_type", len(matches) == 1)
		if len(matches) != 1 {
			allDirect = false
			continue
		}
		v := matches[0]
		matched[v.GUID] = true
		status := directID(v, expected.SourceID)
		trim := "empty"
		if v.Trim != "" {
			trim = "opaque"
		}
		out.IDs = append(out.IDs, identityReadback{s.name, status, trim})
		if status != "verified" {
			allDirect = false
		}
		add(s.name, "no_conflicting_explicit_standard_id", status != "conflict")
		if s.kind != "episode" {
			add(s.name, "root_parent", *v.Parent == "")
			release, e := time.Parse("2006-01-02", v.Release)
			add(s.name, "year", e == nil && release.Year() == expected.Year && (v.Year == nil || *v.Year == expected.Year))
		} else {
			add(s.name, "season_episode", exactInt(v.Season, s.season) && exactInt(v.Episode, s.episode))
			add(s.name, "aired", v.Air == expected.Date)
			// release_date is an additional date assertion when the server supplies it.
			add(s.name, "release_date_if_present", v.Release == "" || v.Release == expected.Date)
			add(s.name, "show_title", v.TVTitle == s.show)
			season, exists := byGUID[*v.Parent]
			parentOK := exists && season.Type == "Season" && exactInt(season.Season, s.season) && season.Parent != nil && *season.Parent != ""
			if parentOK {
				tv, ok := byGUID[*season.Parent]
				parentOK = ok && tv.Type == "TV" && tv.Title == s.show && tv.Parent != nil && *tv.Parent == ""
				if parentOK {
					matched[season.GUID] = true
				}
			}
			add(s.name, "actual_parent_chain", parentOK)
		}
		if s.kind != "tv" {
			path := strings.TrimSuffix(s.path, ".nfo") + ".mkv"
			b, ok := byPath[path]
			add(s.name, "actual_file_binding", ok && b.ItemGUID == v.GUID && b.Actual == root+"/"+path)
		}
	}
	add("all", "all_seven_nodes_accounted_for", len(matched) == 7)
	if allDirect && len(out.IDs) == 5 {
		out.DirectStandardIDReadback = "verified"
	}
	out.Status = "passed"
	for _, c := range out.Checks {
		if !c.Passed {
			out.Status = "failed"
			out.Code = "FIELDS_MISMATCH"
			break
		}
	}
	return out
}
func run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("real-nfo-compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("experiment-id", "", "fresh experiment UUID")
	rp := flags.String("report", "", "supplied real metadata report")
	mp := flags.String("manifest", "", "metadata-nfo generated manifest")
	root := flags.String("fixture-dir", "", "local five-NFO root")
	op := flags.String("observation", "", "scoped observation JSON")
	fp := flags.String("file-evidence", "", "actual three-file bindings JSON")
	fail := func(code string) int { r := newResult(); r.Code = code; json.NewEncoder(out).Encode(r); return 1 }
	if flags.Parse(args) != nil || flags.NArg() != 0 || *rp == "" || *mp == "" || *root == "" || *op == "" || *fp == "" || !uuidPattern.MatchString(*id) {
		return fail("INVALID_FLAGS")
	}
	rb, e := readFile(*rp)
	if e != nil {
		return fail(e.Error())
	}
	mb, e := readFile(*mp)
	if e != nil {
		return fail(e.Error())
	}
	src, e := inputs(rb, mb, *root)
	if e != nil {
		return fail(e.Error())
	}
	ob, e := readFile(*op)
	if e != nil {
		return fail(e.Error())
	}
	fb, e := readFile(*fp)
	if e != nil {
		return fail(e.Error())
	}
	var o observation
	var f fileEvidence
	if decode(ob, &o, false) != nil || decode(fb, &f, true) != nil {
		return fail("INVALID_OBSERVATION_SCHEMA")
	}
	r := compare(*id, src, o, f)
	if json.NewEncoder(out).Encode(r) != nil {
		return 1
	}
	if r.Status != "passed" {
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
