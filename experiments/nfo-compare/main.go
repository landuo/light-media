// Offline comparison of synthetic NFO fixtures and sanitized fnOS observations.
// No network, credentials, server writes, or fixture mutation.
package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"light-media/experiments/internal/fixturelayout"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const nsPrefix = "light-media-m0-test-"

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var datePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

type actor struct {
	Name  string `xml:"name" json:"name"`
	Role  string `xml:"role" json:"role"`
	Order *int   `xml:"order" json:"order"`
	Job   string `json:"job"`
}
type nfo struct {
	XMLName   xml.Name
	Title     string  `xml:"title"`
	Year      int     `xml:"year"`
	ShowTitle string  `xml:"showtitle"`
	Season    *int    `xml:"season"`
	Episode   *int    `xml:"episode"`
	Aired     string  `xml:"aired"`
	Actors    []actor `xml:"actor"`
	IDs       []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
}
type item struct {
	GUID     string  `json:"guid"`
	Type     string  `json:"type"`
	Title    string  `json:"title"`
	TVTitle  string  `json:"tv_title"`
	Ancestor string  `json:"ancestor_guid"`
	Parent   *string `json:"parent_guid"`
	Scope    bool    `json:"scope_verified"`
	Season   *int    `json:"season_number"`
	Episode  *int    `json:"episode_number"`
	Release  string  `json:"release_date"`
	Air      string  `json:"air_date"`
	Persons  []actor `json:"persons"`
	Source   string  `json:"trim_id"`
}
type observation struct {
	Status       string `json:"status"`
	Action       string `json:"action"`
	ExperimentID string `json:"experiment_id"`
	OwnedGUID    string `json:"owned_guid"`
	Baseline     bool   `json:"baseline_unchanged"`
	Items        []item `json:"items"`
}
type check struct {
	Item     string `json:"item"`
	Field    string `json:"field"`
	Passed   bool   `json:"passed"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
}
type result struct {
	Status              string  `json:"status"`
	Code                string  `json:"code,omitempty"`
	Contract            string  `json:"contract"`
	APIReadSuccess      bool    `json:"api_read_success"`
	ProviderIDsVerified bool    `json:"provider_ids_verified"`
	Checks              []check `json:"checks"`
	Note                string  `json:"note"`
}

func readBounded(p string, limit int64) ([]byte, error) {
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("INPUT_UNAVAILABLE")
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return nil, errors.New("INPUT_UNAVAILABLE")
	}
	resolved, e := filepath.EvalSymlinks(abs)
	if e != nil || resolved != abs {
		return nil, errors.New("INPUT_SYMLINK_REFUSED")
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, errors.New("INPUT_UNAVAILABLE")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errors.New("INPUT_LIMIT")
	}
	return b, nil
}
func loadFixtures(root, id string) (map[string]nfo, error) {
	if !uuidPattern.MatchString(id) {
		return nil, errors.New("INVALID_EXPERIMENT_ID")
	}
	works, e := fixturelayout.Detect(root, id)
	if e != nil {
		return nil, e
	}
	out := map[string]nfo{}
	entries := 0
	ns := nsPrefix + id
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return errors.New("FIXTURE_UNAVAILABLE")
		}
		entries++
		if entries > 64 || d.Type()&os.ModeSymlink != 0 {
			return errors.New("FIXTURE_LIMIT_OR_SYMLINK")
		}
		if d.IsDir() || filepath.Ext(p) != ".nfo" {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return errors.New("FIXTURE_INVALID")
		}
		group := ""
		for _, candidate := range works {
			if strings.HasPrefix(rel, candidate.Dir+string(filepath.Separator)) {
				group = candidate.Group
			}
		}
		if group == "" {
			return errors.New("FIXTURE_NAMESPACE_MISMATCH")
		}
		b, e := readBounded(p, 256*1024)
		if e != nil {
			return e
		}
		var v nfo
		if xml.Unmarshal(b, &v) != nil || !strings.HasPrefix(v.Title, ns+" ") || len(v.Actors) > 20 {
			return errors.New("FIXTURE_INVALID")
		}
		switch v.XMLName.Local {
		case "movie":
			if group != "movie" {
				return errors.New("FIXTURE_TYPE_MISMATCH")
			}
		case "tvshow":
			if group == "movie" {
				return errors.New("FIXTURE_TYPE_MISMATCH")
			}
		case "episodedetails":
			if group == "movie" || v.Season == nil || v.Episode == nil || *v.Season < 0 || *v.Episode < 1 || !datePattern.MatchString(v.Aired) {
				return errors.New("FIXTURE_EPISODE_INVALID")
			}
			group += "_episode"
		default:
			return errors.New("FIXTURE_TYPE_MISMATCH")
		}
		if len(v.IDs) == 0 {
			return errors.New("FIXTURE_ID_MISSING")
		}
		for _, x := range v.IDs {
			if x.Type != "lightmedia-m0" || !strings.HasPrefix(x.Value, ns+":") {
				return errors.New("NON_SYNTHETIC_ID_REFUSED")
			}
		}
		for _, a := range v.Actors {
			if !strings.HasPrefix(a.Name, ns+"-") || !strings.HasPrefix(a.Role, ns+"-") {
				return errors.New("FIXTURE_ACTOR_NAMESPACE_MISMATCH")
			}
		}
		if _, ok := out[group]; ok {
			return errors.New("DUPLICATE_FIXTURE")
		}
		out[group] = v
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, k := range []string{"movie", "series", "series_episode", "animation", "animation_episode"} {
		if _, ok := out[k]; !ok {
			return nil, errors.New("FIXTURE_INCOMPLETE")
		}
	}
	return out, nil
}
func intValue(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func equalInt(p *int, n int) bool { return p != nil && *p == n }
func safeDate(s string) any {
	if datePattern.MatchString(s) {
		return s
	}
	return nil
}
func actorSequence(expected, actual []actor) bool {
	if len(expected) == 0 || len(actual) != len(expected) {
		return false
	}
	for _, a := range append(append([]actor{}, expected...), actual...) {
		if a.Order == nil {
			return false
		}
	}
	a := append([]actor(nil), expected...)
	b := append([]actor(nil), actual...)
	sort.Slice(a, func(i, j int) bool { return *a[i].Order < *a[j].Order })
	sort.Slice(b, func(i, j int) bool { return *b[i].Order < *b[j].Order })
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Role != b[i].Role || b[i].Job != "Actor" || (i > 0 && (*a[i].Order <= *a[i-1].Order || *b[i].Order <= *b[i-1].Order)) {
			return false
		}
	}
	return true
}
func compare(id string, fixtures map[string]nfo, o observation) result {
	r := result{Status: "failed", Contract: "synthetic_nfo_fields_v1", APIReadSuccess: o.Status == "observed", Checks: []check{}, Note: "Field contract only; provider IDs and image bytes are not verified here. TV root cast is expected empty; Season/Episode cast is compared. API read success is not field acceptance."}
	add := func(label, field string, pass bool, expected, actual any) {
		r.Checks = append(r.Checks, check{label, field, pass, expected, actual})
	}
	if o.Status != "observed" || o.Action != "observe" || o.ExperimentID != id || !o.Baseline || o.OwnedGUID == "" {
		r.Code = "OBSERVATION_SCOPE_INVALID"
		return r
	}
	add("all", "item_count", len(o.Items) == 7, 7, len(o.Items))
	seen := map[string]bool{}
	for _, v := range o.Items {
		if v.GUID == "" || seen[v.GUID] || !v.Scope || v.Ancestor != o.OwnedGUID {
			r.Code = "OBSERVATION_SCOPE_INVALID"
			return r
		}
		seen[v.GUID] = true
		add("all", "custom_source_not_adopted", v.Source == "", true, v.Source == "")
	}
	unique := func(kind, title string) (item, bool) {
		var found []item
		for _, v := range o.Items {
			if v.Type == kind && v.Title == title {
				found = append(found, v)
			}
		}
		returnVal := item{}
		if len(found) == 1 {
			returnVal = found[0]
		}
		return returnVal, len(found) == 1
	}
	for _, label := range []string{"movie", "series", "animation"} {
		f := fixtures[label]
		kind := "TV"
		if label == "movie" {
			kind = "Movie"
		}
		v, ok := unique(kind, f.Title)
		add(label, "title_and_type", ok, true, ok)
		if !ok {
			continue
		}
		add(label, "parent", v.Parent != nil && *v.Parent == "", "explicit_empty", v.Parent != nil && *v.Parent == "")
		yearDate := fmt.Sprintf("%04d-01-01", f.Year)
		add(label, "year_as_release_date", f.Year > 0 && v.Release == yearDate, yearDate, safeDate(v.Release))
		add(label, "custom_source_not_adopted", v.Source == "", true, v.Source == "")
		if label == "movie" {
			add(label, "actors_relative_order_and_roles", actorSequence(f.Actors, v.Persons), true, actorSequence(f.Actors, v.Persons))
			continue
		}
		add(label, "root_cast_semantics", len(v.Persons) == 0, "empty_root", len(v.Persons))
		ef := fixtures[label+"_episode"]
		ep, ok := unique("Episode", ef.Title)
		add(label+"_episode", "title_and_type", ok, true, ok)
		if !ok {
			continue
		}
		add(label+"_episode", "season", equalInt(ep.Season, *ef.Season), *ef.Season, intValue(ep.Season))
		add(label+"_episode", "episode", equalInt(ep.Episode, *ef.Episode), *ef.Episode, intValue(ep.Episode))
		add(label+"_episode", "air_date", ep.Air == ef.Aired, ef.Aired, safeDate(ep.Air))
		add(label+"_episode", "release_date", ep.Release == ef.Aired, ef.Aired, safeDate(ep.Release))
		add(label+"_episode", "show_title", ep.TVTitle == f.Title, true, ep.TVTitle == f.Title)
		add(label+"_episode", "actors_relative_order_and_roles", actorSequence(ef.Actors, ep.Persons), true, actorSequence(ef.Actors, ep.Persons))
		seasons := []item{}
		for _, s := range o.Items {
			if s.Type == "Season" && s.Parent != nil && *s.Parent == v.GUID && equalInt(s.Season, *ef.Season) {
				seasons = append(seasons, s)
			}
		}
		add(label+"_season", "unique_expected_season", len(seasons) == 1, 1, len(seasons))
		if len(seasons) == 1 {
			s := seasons[0]
			add(label+"_episode", "parent", ep.Parent != nil && *ep.Parent == s.GUID, true, ep.Parent != nil && *ep.Parent == s.GUID)
			add(label+"_season", "actors_relative_order_and_roles", actorSequence(f.Actors, s.Persons), true, actorSequence(f.Actors, s.Persons))
		}
	}
	r.Status = "passed"
	for _, c := range r.Checks {
		if !c.Passed {
			r.Status = "failed"
			r.Code = "FIELDS_MISMATCH"
			break
		}
	}
	return r
}
func main() {
	flags := flag.NewFlagSet("nfo-compare", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("experiment-id", "", "same synthetic UUID")
	dir := flags.String("fixture-dir", "", "local fixture directory")
	input := flags.String("observation", "", "sanitized observe JSON file")
	fail := func(code string) {
		json.NewEncoder(os.Stdout).Encode(result{Status: "failed", Code: code, Contract: "synthetic_nfo_fields_v1"})
		os.Exit(2)
	}
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 || *dir == "" || *input == "" {
		fail("INVALID_FLAGS")
	}
	fixtures, e := loadFixtures(*dir, *id)
	if e != nil {
		fail(e.Error())
	}
	b, e := readBounded(*input, 2*1024*1024)
	if e != nil {
		fail(e.Error())
	}
	var o observation
	if json.Unmarshal(b, &o) != nil {
		fail("INVALID_OBSERVATION")
	}
	r := compare(*id, fixtures, o)
	json.NewEncoder(os.Stdout).Encode(r)
	if r.Status != "passed" {
		os.Exit(1)
	}
}
