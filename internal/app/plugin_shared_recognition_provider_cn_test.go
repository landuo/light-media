package app

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
)

//go:embed testdata/source-provider-cn05.json
var sourceProviderCNOracle05 []byte

// This calls the actual App provider function and unchanged helpers, with the
// exact independently captured source inputs. It creates no App, HTTP peer,
// worker, database, provider or synthetic expected model.
func TestSourceTMDBProviderInfoCNSource05(t *testing.T) {
	h := sha256.Sum256(sourceProviderCNOracle05)
	if hex.EncodeToString(h[:]) != "285ad4a28c5b5bc03533baff22ff1bb43da490fa27be99222d3de1aa2f86dffa" {
		t.Fatal("original provider source05 bytes changed")
	}
	var doc struct {
		Count int               `json:"case_count"`
		Drift []json.RawMessage `json:"input_drift"`
		Cases []struct {
			ID    string `json:"id"`
			Input struct {
				MType    string          `json:"mtype"`
				Language string          `json:"language"`
				Detail   json.RawMessage `json:"detail"`
			} `json:"input"`
			Outcome struct {
				Kind  string          `json:"kind"`
				Value json.RawMessage `json:"value"`
				Class string          `json:"class"`
			} `json:"outcome"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(sourceProviderCNOracle05, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Count != 5 || len(doc.Cases) != 5 || len(doc.Drift) != 0 {
		t.Fatal("original source05 count/drift")
	}
	decode := func(raw []byte) any {
		t.Helper()
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var value any
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	returned, raised := 0, 0
	for _, c := range doc.Cases {
		kind := "movie"
		switch c.Input.MType {
		case "MOVIE":
		case "TV":
			kind = "tv"
		default:
			t.Fatalf("%s unexpected source type", c.ID)
		}
		if c.Input.Language != "zh-CN" {
			t.Fatalf("%s unbound language", c.ID)
		}
		inputBefore := append([]byte(nil), c.Input.Detail...)
		got, err := sourceTMDBProviderInfo(c.Input.Detail, kind)
		if !bytes.Equal(inputBefore, c.Input.Detail) {
			t.Fatalf("%s raw input changed", c.ID)
		}
		switch c.Outcome.Kind {
		case "return":
			returned++
			if err != nil {
				t.Errorf("%s unexpected error: %v", c.ID, err)
				continue
			}
			want, ok := decode(c.Outcome.Value).(map[string]any)
			if !ok {
				t.Fatalf("%s expected whole source dict", c.ID)
			}
			// Sole permitted representation mapping: this exact top-level
			// source MediaType enum to its actual .value. No other field,
			// number, sequence or absent/null distinction is normalized.
			enum, ok := want["media_type"].(map[string]any)
			if !ok || len(enum) != 3 || enum["enum"] != "MediaType" || enum["name"] != c.Input.MType {
				t.Fatalf("%s source enum shape", c.ID)
			}
			value, ok := enum["value"].(string)
			if !ok || value != "电影" && value != "电视剧" {
				t.Fatalf("%s source enum value", c.ID)
			}
			want["media_type"] = value
			actual := decode(got)
			if !reflect.DeepEqual(actual, want) {
				wantJSON, _ := json.Marshal(want)
				t.Errorf("%s whole provider mismatch\ngot=%s\nsource=%s", c.ID, got, wantJSON)
			}
		case "uncaught_exception":
			raised++
			// Direct only, exactly as the existing host's source-error gate.
			sourceErr, direct := err.(*originalMediaRecognitionSourceError)
			if got != nil || !direct || sourceErr == nil || sourceErr.Class != c.Outcome.Class || sourceErr.Class != "TypeError" {
				t.Errorf("%s want direct source %s, got %s err=%v", c.ID, c.Outcome.Class, got, err)
			}
		default:
			t.Fatalf("%s unexpected original outcome %q", c.ID, c.Outcome.Kind)
		}
	}
	if returned != 4 || raised != 1 {
		t.Fatalf("source coverage returned=%d raised=%d", returned, raised)
	}
	t.Log("whole 4 provider dicts + 1 direct TypeError; source partial detail mutation retained in raw, not claimed as target caller-object mutation")
}
