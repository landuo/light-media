package sitestatistic

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestActualPython312CharacterAndConversionOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/python312-character-classes.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Text       string
			Digit      bool `json:"isdigit"`
			Int, Float struct{ Type, Repr, Raised, Message string }
		} `json:"conversion_cases"`
		Dates    []struct{ Text, Parsed, Strftime, Raised, Message string } `json:"date_cases"`
		Metadata []struct {
			Field, Value string
			Page         struct {
				Value           json.RawMessage
				Raised, Message string
			}
		} `json:"metadata_cases"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		t.Run(c.Text, func(t *testing.T) {
			if sourceIsDigit(c.Text) != c.Digit {
				t.Fatal("source isdigit changed")
			}
			f, e := sourceFloat(c.Text)
			if c.Float.Raised != "" {
				var se *SourceError
				if !errors.As(e, &se) || se.Kind != c.Float.Raised || se.Message != c.Float.Message {
					t.Fatal("source float failure changed", e, c.Float)
				}
			} else if e != nil || pyFloat(f) != c.Float.Repr {
				t.Fatal("source float changed", f, e, c.Float)
			}
			n, e := sourceIntString(c.Text)
			if c.Int.Raised != "" {
				var se *SourceError
				if !errors.As(e, &se) || se.Kind != c.Int.Raised || se.Message != c.Int.Message {
					t.Fatal("source int failure changed", e, c.Int)
				}
			} else if e != nil || n.String() != c.Int.Repr {
				t.Fatal("source int changed", n, e, c.Int)
			}
		})
	}
	for _, c := range source.Dates {
		t.Run("date-"+c.Text, func(t *testing.T) {
			v, e := parseSourceDate(c.Text)
			if c.Raised != "" {
				if e == nil {
					t.Fatal("invalid source date accepted")
				}
				return
			}
			if e != nil || v.Format("2006-01-02T15:04:05") != c.Parsed || sourceDay(v) != c.Strftime {
				t.Fatal("source strptime changed", v, e, c)
			}
		})
	}
	for _, c := range source.Metadata {
		t.Run(c.Field+"-"+c.Value, func(t *testing.T) {
			current := Data{"name": "Owned", "domain": "owned.invalid", "updated_day": "2026-10-16", "username": "owned", "user_level": "User", "upload": 200.0, "download": 100.0, "ratio": 2.0, "bonus": 1.0, "seeding": 2.0, "seeding_size": 1024.0, "err_msg": ""}
			previous := Data{}
			for k, v := range current {
				previous[k] = v
			}
			previous["updated_day"] = "2026-10-15"
			previous["upload"] = 100.0
			current[c.Field], previous[c.Field] = c.Value, c.Value
			selected, e := Select(Snapshot{Latest: []Data{current}, History: []Data{previous}, ActiveDomains: []string{"owned.invalid"}})
			if e != nil {
				t.Fatal(e)
			}
			page, e := Page(selected)
			if c.Page.Raised != "" {
				var se *SourceError
				if !errors.As(e, &se) || se.Kind != c.Page.Raised || se.Message != c.Page.Message {
					t.Fatal("shared metadata failure changed", e, c.Page.Raised, c.Page.Message)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				sameSourceJSON(t, "metadata page", page, c.Page.Value)
			}
		})
	}
}
