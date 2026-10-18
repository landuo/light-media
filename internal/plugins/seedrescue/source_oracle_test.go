package seedrescue

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Op      string
			Arg     json.RawMessage
			Value   json.RawMessage
			Error   string
			Message string
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for i, c := range fixture.Cases {
		t.Run(c.Op+"/"+string(rune(i+256)), func(t *testing.T) {
			var arg any
			d := json.NewDecoder(bytes.NewReader(c.Arg))
			d.UseNumber()
			if e = d.Decode(&arg); e != nil {
				t.Fatal(e)
			}
			var value any
			var err error
			switch c.Op {
			case "promotion":
				value = PromotionKind(arg)
			case "optional":
				value = deref(OptionalLimit(arg, 7))
			case "positive":
				value = PositiveInt(arg, 7)
			case "config":
				obj, _ := arg.(map[string]any)
				value = ParseConfig(obj).Current()
			case "keywords":
				value = SearchKeywords(arg.(string))
			case "extract":
				value = nullable(ExtractTorrentID(arg.(string)))
			case "safe":
				value = nullable(SafePageURL(arg.(string)))
			case "hash":
				b, e := hex.DecodeString(arg.(string))
				if e != nil {
					t.Fatal(e)
				}
				value, err = InfoHashFromTorrent(b)
			case "torrent":
				obj := arg.(map[string]any)
				state, reason := TargetTorrentStatus(obj)
				value = Object{"complete": TorrentIsComplete(obj), "status": []any{state, deref(reason)}}
			case "detail_url":
				value = nullable(DetailURL(arg.(map[string]any), "42"))
			case "mteam":
				value = ParseMTeam(arg, "42")
			case "nexus":
				value, err = ParseNexus(arg.(string), "https://site.test/details.php?id=42", "42")
			case "job_view":
				value = JobView(arg.(map[string]any))
			default:
				t.Fatalf("unhandled operation %s", c.Op)
			}
			if c.Error != "" {
				if err == nil || ErrorClass(err) != c.Error || err.Error() != c.Message {
					t.Fatalf("got %v (%s), want %s %s", err, ErrorClass(err), c.Error, c.Message)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(value)
			var g, w any
			_ = json.Unmarshal(got, &g)
			_ = json.Unmarshal(c.Value, &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("got %s\nwant %s", got, c.Value)
			}
		})
	}
}

func TestActualSourceSubmissionFlows(t *testing.T) {
	raw, e := os.ReadFile("reference/flow-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Mode  string
			Value json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, entry := range fixture.Cases {
		t.Run(entry.Mode, func(t *testing.T) {
			ctx := context.Background()
			h := newFake()
			m := newMachine(t, h)
			cfg := baseConfig()
			cfg["notify"] = false
			if e = m.Init(ctx, cfg); e != nil {
				t.Fatal(e)
			}
			mode := entry.Mode
			target := strings.HasPrefix(mode, "target_")
			actual, _ := InfoHashFromTorrent(fixtureTorrent)
			hash := strings.Repeat("a", 40)
			site := 2
			if target {
				hash = actual
				site = 1
			}
			job := Object{"target_hash": hash, "target_site_id": 1, "title": "Fixture", "state": "AWAITING_SELECTION", "probes": []Object{}, "candidates": []Object{}}
			runtime := Object{"title": "Source", "page_url": "https://source.test/details.php?id=7&passkey=fixture", "enclosure": "https://source.test/download.php?id=7&passkey=fixture", "site_cookie": "fixture-cookie", "site_ua": "FixtureUA", "site_proxy": false}
			if mode == "missing_link" {
				delete(runtime, "enclosure")
			}
			expectedHash := actual
			if mode == "bad_hash" {
				expectedHash = strings.Repeat("b", 40)
			}
			candidate := Candidate{Match: Match{2, "7", expectedHash}, SiteID: site, SiteName: "Source", Seeders: ptr(3), DownloadFactor: ptr(0.0), Priority: 0, Torrent: runtime, MetadataStatus: "ready"}
			calls := []Object{}
			h.add = func(req AddRequest) (bool, error) {
				saved := loadFixture(t, m, hash)
				calls = append(calls, Object{"kind": "add", "intent": saved, "category": req.Category, "tag": req.Tags, "download_dir": deref(req.SavePath), "content_hex": hex.EncodeToString(req.Content)})
				if mode == "error" {
					return false, errors.New("synthetic lost ack")
				}
				return mode != "false", nil
			}
			if mode == "metadata_missing" {
				h.metadata = func(Object) ([]byte, string, error) { return nil, "fixture metadata error", nil }
			}
			h.query = func([]string) ([]Object, error) {
				if mode == "existing_complete" || mode == "target_seeding" || mode == "target_paused" {
					state := "uploading"
					if mode == "target_paused" {
						state = "pausedUP"
					}
					return []Object{{"state": state, "progress": 1, "amount_left": 0}}, nil
				}
				if mode == "existing_incomplete" {
					return []Object{{"state": "downloading", "progress": .4, "amount_left": 8}}, nil
				}
				return nil, nil
			}
			ok, reason, err := m.startSource(ctx, candidate, job)
			got := Object{"ok": ok, "reason": reason, "job": job, "calls": calls}
			if errors.Is(err, ErrDownloadSubmissionUncertain) {
				delete(got, "ok")
				delete(got, "reason")
				got["error"] = "DownloadSubmissionUncertain"
			} else if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(got)
			var g, w any
			_ = json.Unmarshal(b, &g)
			_ = json.Unmarshal(entry.Value, &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("got %s\nwant %s", b, entry.Value)
			}
		})
	}
}

func TestActualSourceFullScanFlows(t *testing.T) {
	raw, e := os.ReadFile("reference/scan-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Scenario string
			Value    json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Scenario, func(t *testing.T) {
			ctx := context.Background()
			h := newFake()
			m := newMachine(t, h)
			scenario := tc.Scenario
			cfg := baseConfig()
			cfg["notify"] = false
			if scenario == "auto" || scenario == "capacity" {
				cfg["selection_mode"] = "auto"
			}
			if scenario == "hybrid" {
				cfg["selection_mode"] = "hybrid"
			}
			if scenario == "capacity" {
				cfg["max_download_count"] = 1
				h.downloading = []Object{{}}
			}
			if scenario == "initial_limit" {
				cfg["initial_limit"] = 1
			}
			if e = m.Init(ctx, cfg); e != nil {
				t.Fatal(e)
			}
			rss := []Object{{"title": "Film.2026.WEB-DL", "link": "https://target.test/details.php?id=42", "enclosure": "https://target.test/download.php?id=42&passkey=fixture-secret", "size": 1024}}
			if scenario == "initial_limit" || scenario == "batch" {
				rss = append(rss, Object{"title": "Other.2026.WEB-DL", "link": "https://target.test/details.php?id=43", "enclosure": "https://target.test/download.php?id=43&passkey=fixture-secret", "size": 2048})
			}
			if scenario == "duplicate" {
				rss = append(rss, rss...)
			}
			if scenario == "rss_empty" {
				rss = []Object{}
			}
			if scenario == "missing_fields" {
				rss = []Object{{}}
			}
			h.rss = func(context.Context, string, Object) ([]Object, bool, error) {
				return clone(rss), scenario != "rss_failure", nil
			}
			h.metadata = func(obj Object) ([]byte, string, error) {
				if scenario == "metadata_failure" {
					return nil, "fixture metadata error", nil
				}
				enclosure := strOr(obj["enclosure"], "")
				if strings.Contains(enclosure, "source.test") {
					return []byte("d4:infod4:name1:yee"), "", nil
				}
				if strings.Contains(enclosure, "id=43") {
					return []byte("d4:infod4:name1:zee"), "", nil
				}
				return fixtureTorrent, "", nil
			}
			sourceHash, _ := InfoHashFromTorrent([]byte("d4:infod4:name1:yee"))
			h.seed = func(_ context.Context, hashes []string) (any, string, error) {
				out := Object{}
				for _, hash := range hashes {
					out[hash] = Object{"torrent": []Object{{"sid": 2, "torrent_id": "7", "info_hash": sourceHash}}}
				}
				return out, "", nil
			}
			h.search = func(_ context.Context, d, kw string, page int) ([]Object, error) {
				tid := "42"
				if strings.HasPrefix(kw, "Other") {
					tid = "43"
				}
				return []Object{{"page_url": "https://target.test/details.php?id=" + tid, "enclosure": "https://target.test/download.php?id=" + tid, "seeders": 2, "peers": 1, "grabs": 5, "size": 1024, "downloadvolumefactor": 0}}, nil
			}
			adds := []Object{}
			h.add = func(req AddRequest) (bool, error) {
				adds = append(adds, Object{"content_hex": hex.EncodeToString(req.Content), "download_dir": deref(req.SavePath), "category": req.Category, "tag": req.Tags})
				return true, nil
			}
			if e = m.Scan(ctx); e != nil {
				t.Fatal(e)
			}
			data := Object{}
			for k, b := range h.data {
				var v any
				_ = json.Unmarshal(b, &v)
				data[k] = v
			}
			got := Object{"data": data, "adds": adds}
			b, _ := json.Marshal(got)
			var g, w any
			_ = json.Unmarshal(b, &g)
			_ = json.Unmarshal(tc.Value, &w)
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("got %s\nwant %s", b, tc.Value)
			}
		})
	}
}
