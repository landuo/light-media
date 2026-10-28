package app

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"strconv"
	"testing"
	"time"
)

func mediaSearchExpectedFloat(t *testing.T, value string) float64 {
	t.Helper()
	switch value {
	case "nan":
		return math.NaN()
	case "inf":
		return math.Inf(1)
	case "-inf":
		return math.Inf(-1)
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOriginalMediaSearchConfigurationFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-search-configuration-source-oracle.json")
	var reference struct {
		Cases []struct {
			Field, Raw string
			Names      string `json:"max_names"`
			Multiple   bool
			Interval   string
			Normalized bool
		}
	}
	if err != nil || json.Unmarshal(raw, &reference) != nil || len(reference.Cases) != 24 {
		t.Fatal("fixed ordinary media Settings replay missing", err)
	}
	for _, sample := range reference.Cases {
		actual, normalized := MediaSearchEnvironment(func(name string) (string, bool) { return sample.Raw, name == sample.Field })
		want := mediaSearchExpectedFloat(t, sample.Interval)
		if actual.MaxNames.String() != sample.Names || actual.MultipleNames != sample.Multiple || normalized != sample.Normalized || (!math.IsNaN(want) && math.Float64bits(actual.KeywordInterval) != math.Float64bits(want)) || math.IsNaN(want) != math.IsNaN(actual.KeywordInterval) {
			t.Fatal("actual source generic settings conversion/default/normalization differs", sample.Field, sample.Raw, actual, normalized, sample)
		}
	}
}

func TestOriginalMediaSearchIntervalReservationFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-resource-modules/media-search-configuration-source-oracle.json")
	var reference struct {
		Reservations []struct {
			Interval string
			Ticks    []float64
			Waits    []string
			Error    *string
		}
	}
	if err != nil || json.Unmarshal(raw, &reference) != nil || len(reference.Reservations) != 7 {
		t.Fatal("fixed MinIntervalLimiter replay missing", err)
	}
	for _, sample := range reference.Reservations {
		a := &App{}
		for index, expected := range sample.Waits {
			wait, err := a.reserveOriginalMediaSearchKeyword(time.Unix(int64(sample.Ticks[index]), 0), mediaSearchExpectedFloat(t, sample.Interval))
			if sample.Error != nil && index == len(sample.Waits)-1 {
				if err == nil {
					t.Fatal("source overflow belongs to a later wait, not initial reservation")
				}
				continue
			}
			if err != nil || wait.Seconds() != mediaSearchExpectedFloat(t, expected) {
				t.Fatal("fixed monotonic reservation differs", sample.Interval, index, wait, err)
			}
		}
	}
}

func TestOriginalMediaSearchTrustedOptionsFrozen(t *testing.T) {
	t.Setenv("MAX_SEARCH_NAME_LIMIT", "-2")
	t.Setenv("SEARCH_MULTIPLE_NAME", "YES")
	t.Setenv("SEARCH_KEYWORD_MIN_INTERVAL", "0")
	a := newTestApp(t)
	t.Setenv("MAX_SEARCH_NAME_LIMIT", "9")
	if a.mediaSearchConfiguration.MaxNames.String() != "-2" || !a.mediaSearchConfiguration.MultipleNames || a.mediaSearchConfiguration.KeywordInterval != 0 {
		t.Fatal("ordinary settings were reread during app lifetime")
	}
	input := &MediaSearchConfiguration{MaxNames: big.NewInt(0), MultipleNames: true, KeywordInterval: 0}
	copy := cloneMediaSearchConfiguration(input)
	input.MaxNames.SetInt64(99)
	if copy.MaxNames.Sign() != 0 {
		t.Fatal("trusted startup integer pointer was shared")
	}
}
