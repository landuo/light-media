package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testID = "00000000-0000-4000-8000-000000000001"

func ip(n int) *int       { return &n }
func sp(s string) *string { return &s }
func sample() (map[string]nfo, observation) {
	ns := nsPrefix + testID
	a := []actor{{Name: ns + "-Actor1", Role: ns + "-Role1", Order: ip(0)}, {Name: ns + "-Actor2", Role: ns + "-Role2", Order: ip(1)}}
	actual := func() []actor {
		return []actor{{Name: a[0].Name, Role: a[0].Role, Order: ip(1), Job: "Actor"}, {Name: a[1].Name, Role: a[1].Role, Order: ip(2), Job: "Actor"}}
	}
	f := map[string]nfo{"movie": {Title: ns + " Movie & <NFO>", Year: 2024, Actors: a}, "series": {Title: ns + " Series", Year: 2024, Actors: a}, "animation": {Title: ns + " Animation", Year: 2024, Actors: a}, "series_episode": {Title: ns + " Episode two", Season: ip(1), Episode: ip(2), Aired: "2024-01-02", Actors: a}, "animation_episode": {Title: ns + " Animation episode", Season: ip(2), Episode: ip(1), Aired: "2024-01-02", Actors: a}}
	o := observation{Status: "observed", Action: "observe", ExperimentID: testID, OwnedGUID: "owned", Baseline: true}
	o.Items = append(o.Items, item{GUID: "movie", Type: "Movie", Title: f["movie"].Title, Ancestor: "owned", Parent: sp(""), Scope: true, Release: "2024-01-01", Persons: actual()})
	for _, label := range []string{"series", "animation"} {
		ef := f[label+"_episode"]
		o.Items = append(o.Items, item{GUID: label, Type: "TV", Title: f[label].Title, Ancestor: "owned", Parent: sp(""), Scope: true, Release: "2024-01-01", Persons: []actor{}}, item{GUID: label + "-season", Type: "Season", TVTitle: f[label].Title, Ancestor: "owned", Parent: sp(label), Scope: true, Season: ef.Season, Persons: actual()}, item{GUID: label + "-episode", Type: "Episode", Title: ef.Title, TVTitle: f[label].Title, Ancestor: "owned", Parent: sp(label + "-season"), Scope: true, Season: ef.Season, Episode: ef.Episode, Air: ef.Aired, Release: ef.Aired, Persons: actual()})
	}
	return f, o
}
func TestReadSuccessDoesNotHideWrongEpisode(t *testing.T) {
	f, o := sample()
	r := compare(testID, f, o)
	if r.Status != "passed" || r.ProviderIDsVerified {
		t.Fatalf("valid synthetic contract failed %+v", r)
	}
	for i := range o.Items {
		if o.Items[i].GUID == "series-episode" {
			o.Items[i].Episode = ip(1)
		}
	}
	r = compare(testID, f, o)
	if !r.APIReadSuccess || r.Status != "failed" || r.Code != "FIELDS_MISMATCH" {
		t.Fatalf("wrong episode passed %+v", r)
	}
	found := false
	for _, c := range r.Checks {
		if c.Item == "series_episode" && c.Field == "episode" {
			found = true
			if c.Passed || c.Expected != 2 || c.Actual != 1 {
				t.Fatalf("wrong actual evidence %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing episode assertion")
	}
}
func TestMismatchedFieldsFail(t *testing.T) {
	for _, field := range []string{"title", "year", "role", "order", "missing_order", "parent", "ancestor", "scope", "source"} {
		t.Run(field, func(t *testing.T) {
			f, o := sample()
			v := &o.Items[0]
			switch field {
			case "title":
				v.Title = "Wrong title"
			case "year":
				v.Release = "2025-01-01"
			case "role":
				v.Persons[0].Role = "Wrong role"
			case "order":
				v.Persons[0].Order = ip(3)
			case "missing_order":
				v.Persons[0].Order = nil
			case "parent":
				v.Parent = nil
			case "ancestor":
				v.Ancestor = "foreign"
			case "scope":
				v.Scope = false
			case "source":
				v.Source = "tm-real-source"
			}
			if compare(testID, f, o).Status != "failed" {
				t.Fatalf("%s mismatch passed", field)
			}
		})
	}
}
func TestBasenameDoesNotAlterMetadataExpectations(t *testing.T) {
	f, _ := sample()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ns := nsPrefix + testID
	for _, label := range []string{"movie", "series", "animation", "series_episode", "animation_episode"} {
		v := f[label]
		suffix, name, tag := "-Movie (2024)", "Synthetic Movie.nfo", "movie"
		switch label {
		case "series":
			suffix, name, tag = "-Series", "tvshow.nfo", "tvshow"
		case "animation":
			suffix, name, tag = "-Animation", "tvshow.nfo", "tvshow"
		case "series_episode":
			suffix, name, tag = "-Series", "Season 1/Synthetic Series S01E02.nfo", "episodedetails"
		case "animation_episode":
			suffix, name, tag = "-Animation", "Season 2/Synthetic Animation S02E01.nfo", "episodedetails"
		}
		v.XMLName = xml.Name{Local: tag}
		v.IDs = append(v.IDs, struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		}{Type: "lightmedia-m0", Value: ns + ":synthetic"})
		b, e := xml.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		p := filepath.Join(dir, ns+suffix, name)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, b, 0600)
	}
	loaded, e := loadFixtures(dir, testID)
	if e != nil {
		t.Fatal(e)
	}
	if *loaded["series_episode"].Episode != 2 || !strings.Contains(loaded["movie"].Title, "& <NFO>") {
		t.Fatal("basename or escaping changed expectation")
	}

	for _, pair := range [][2]string{{ns + "-Movie (2024)", "合成电影 (2024)"}, {ns + "-Series", "合成普通剧"}, {ns + "-Animation", "合成国漫"}} {
		if e := os.Rename(filepath.Join(dir, pair[0]), filepath.Join(dir, pair[1])); e != nil {
			t.Fatal(e)
		}
	}
	canonical, e := loadFixtures(dir, testID)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(loaded, canonical) {
		t.Fatal("layout changed XML expectations")
	}
	_, o := sample()
	for i := range o.Items {
		if o.Items[i].GUID == "series-episode" {
			o.Items[i].Episode = ip(1)
		}
	}
	r := compare(testID, canonical, o)
	if r.Status != "failed" {
		t.Fatal("canonical layout hid E02 to E01 mismatch")
	}
	found := false
	for _, c := range r.Checks {
		if c.Item == "series_episode" && c.Field == "episode" && !c.Passed && c.Expected == 2 && c.Actual == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("canonical layout lost actual episode failure")
	}
}
