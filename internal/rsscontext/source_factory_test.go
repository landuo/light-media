package rsscontext

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"testing"
	"time"

	"light-media/internal/releasemeta"
)

func TestSourceTorrentFactoryBeforeClockAndFactor(t *testing.T) {
	t.Run("invalid_date_is_preserved", func(t *testing.T) {
		torrent, err := NewTorrentInfo(map[string]any{"freedate": "owned-invalid-date"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := torrent.ToDict()
		if err != nil || got["freedate_diff"] != "owned-invalid-date" {
			t.Fatalf("source invalid date was not retained: %v", err)
		}
	})
	t.Run("text_factor_raises", func(t *testing.T) {
		torrent, err := NewTorrentInfo(map[string]any{"uploadvolumefactor": "1", "downloadvolumefactor": 0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = torrent.ToDict(); err == nil || err.Error() != "TypeError" {
			t.Fatalf("source factor TypeError lost: %v", err)
		}
	})
}

func sourceFactoryJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Never canonicalize source IDs through float64: the owned fixture contains
// adjacent integers above2^90 whose distinct identities must be retained.
func sourceFactoryEqual(a, b any) bool {
	if aNum, ok := a.(json.Number); ok {
		bNum, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, ok := new(big.Rat).SetString(string(aNum))
		if !ok {
			return false
		}
		y, ok := new(big.Rat).SetString(string(bNum))
		return ok && x.Cmp(y) == 0
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, value := range x {
			other, present := y[key]
			if !present || !sourceFactoryEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sourceFactoryEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func TestSourceTorrentContextInstalledFactoryOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/source-torrent-context-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Header struct {
			TorrentFields []string       `json:"torrent_fields"`
			ContextFields []string       `json:"context_fields"`
			Defaults      map[string]any `json:"torrent_default"`
			Attempts      map[string]int `json:"attempts"`
		} `json:"header"`
		Cases []struct {
			Case struct {
				Name, Kind, Now                       string
				Fields, Update, Flags, Torrent, Media map[string]any
				MetaInstance                          map[string]any `json:"meta_instance"`
			} `json:"case"`
			Result           json.RawMessage `json:"result"`
			Error            string          `json:"error"`
			SourcePrintCount int             `json:"source_print_count"`
		} `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Cases) != 77 || len(oracle.Header.TorrentFields) != 25 || len(oracle.Header.ContextFields) != 10 {
		t.Fatal("installed source field/case inventory changed")
	}
	for _, n := range oracle.Header.Attempts {
		if n != 0 {
			t.Fatal("original oracle performed I/O effects")
		}
	}
	if !sourceFactoryEqual(sourceFactoryJSON(t, schema.Defaults["TorrentInfo"]), sourceFactoryJSON(t, oracle.Header.Defaults)) {
		t.Fatal("torrent defaults drifted from installed class")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Case.Name, func(t *testing.T) {
			c := row.Case
			now, err := time.Parse("2006-01-02T15:04:05.999999999", c.Now)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if c.Kind == "torrent" {
				var torrent *TorrentInfo
				torrent, err = NewTorrentInfo(c.Fields)
				if err == nil {
					torrent.FromDict(c.Update)
					var dict map[string]any
					dict, err = torrent.ToDictAt(now)
					if err == nil {
						minutes, diagnostic := torrent.PubMinutesSourceResult(now)
						kind := "int"
						if _, ok := minutes.(float64); ok {
							kind = "float"
						}
						if (diagnostic != nil) != (row.SourcePrintCount > 0) {
							t.Fatal("caught source diagnostic was lost")
						}
						got = map[string]any{"dict": dict, "pub_minutes": minutes, "pub_minutes_type": kind}
					}
				}
			} else {
				var torrent *TorrentInfo
				var media *MediaInfo
				var meta *releasemeta.MetaObject
				if c.Torrent != nil {
					torrent, err = NewTorrentInfo(c.Torrent)
				}
				if err == nil && c.Media != nil {
					media, err = NewNormalizedMediaInfo(c.Media)
				}
				if c.MetaInstance != nil {
					meta = releasemeta.EmptyMetaObject()
					for key, value := range c.MetaInstance {
						meta.Set(key, value)
					}
				}
				if err == nil {
					var context *Context
					context, err = NewContextWithFields(meta, media, torrent, c.Flags, Frame{})
					if err == nil {
						got, err = context.ToDictAt(now)
					}
				}
			}
			if row.Error != "" {
				if err == nil || err.Error() != row.Error {
					t.Fatalf("source error got%v want%s", err, row.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var want any
			d := json.NewDecoder(bytes.NewReader(row.Result))
			d.UseNumber()
			if err := d.Decode(&want); err != nil {
				t.Fatal(err)
			}
			actual := sourceFactoryJSON(t, got)
			if !sourceFactoryEqual(actual, want) {
				a, _ := json.Marshal(actual)
				b, _ := json.Marshal(want)
				t.Fatalf("source projection mismatch\ngot:%s\nwant:%s", a, b)
			}
		})
	}
}

func TestSourceCompleteContextNativeMetaAndColdState(t *testing.T) {
	frame := Frame{Revision: "owned-context", Global: releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "rust", CustomWords: []string{}}}}
	parser, err := releasemeta.NewMetaInfoParser(frame.Global)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := parser.ParseTitle("Owned.Show.S00E02.2160p.WEB-DL.H265-OWNED", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	media, err := NewNormalizedMediaInfo(map[string]any{"type": "电视剧", "title": "Owned Show", "tmdb_id": json.Number("1267650600228229401496703205377")})
	if err != nil {
		t.Fatal(err)
	}
	torrent, err := NewTorrentInfo(map[string]any{"title": "Owned.Show.S00E02.2160p.WEB-DL.H265-OWNED", "site": json.Number("1267650600228229401496703205378"), "site_cookie": "owned-only-private", "enclosure": "https://owned.invalid/download?passkey=synthetic", "freedate": "2026-10-22 12:00:00"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, allowed := range []any{nil, []any{}, []any{json.Number("4"), json.Number("2")}} {
		t.Run(func() string {
			if allowed == nil {
				return "none"
			}
			if len(allowed.([]any)) == 0 {
				return "empty"
			}
			return "nonempty"
		}(), func(t *testing.T) {
			c, err := NewContextWithFields(meta, media, torrent, map[string]any{"allowed_episodes": allowed, "resource_source": "search"}, frame)
			if err != nil {
				t.Fatal(err)
			}
			before, err := c.ToDictAt(now)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != 10 || len(before["torrent_info"].(map[string]any)) != 27 || len(before["media_info"].(map[string]any)) != len(schema.Defaults["MediaInfo"])+3 {
				t.Fatal("complete source objects reduced to compact DTOs")
			}
			if len(before["meta_info"].(map[string]any)) < 30 {
				t.Fatal("full original MetaInfo object missing")
			}
			encoded, err := c.EncodeState()
			if err != nil {
				t.Fatal(err)
			}
			cold, err := DecodeState(encoded)
			if err != nil {
				t.Fatal(err)
			}
			after, err := cold.ToDictAt(now)
			if err != nil {
				t.Fatal(err)
			}
			if !sourceFactoryEqual(sourceFactoryJSON(t, before), sourceFactoryJSON(t, after)) {
				t.Fatal("cold source context changed")
			}
		})
	}
	a, _ := NewTorrentInfo(nil)
	b, _ := NewTorrentInfo(nil)
	_ = a.Get("labels").([]any) // the default factory yields an independent actual empty list
	a.Set("labels", []any{"owned"})
	if len(b.Get("labels").([]any)) != 0 {
		t.Fatal("default lists shared across instances")
	}
	if sourceFactoryEqual(sourceFactoryJSON(t, json.Number("1267650600228229401496703205377")), sourceFactoryJSON(t, json.Number("1267650600228229401496703205378"))) {
		t.Fatal("90bit IDs compared through float64")
	}
}
