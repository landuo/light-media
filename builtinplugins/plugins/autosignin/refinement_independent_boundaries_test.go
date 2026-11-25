package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const edgeReviewRoot = "testdata/source-oracles/independent-autosignin-source-edge-review-20261106/"

func edgeFixture(t *testing.T) struct {
	Dates []struct {
		Date      string
		Items     any
		ItemsRaw  string `json:"items_raw"`
		Result    []string
		Error     *string
		ErrorType *string `json:"error_type"`
	}
	JSON []struct {
		Text   string
		Stdlib struct {
			Value any
			Error *string
		}
		Hares *struct {
			Result []any
			Error  *string
		}
	}
} {
	t.Helper()
	var f struct {
		Dates []struct {
			Date      string
			Items     any
			ItemsRaw  string `json:"items_raw"`
			Result    []string
			Error     *string
			ErrorType *string `json:"error_type"`
		}
		JSON []struct {
			Text   string
			Stdlib struct {
				Value any
				Error *string
			}
			Hares *struct {
				Result []any
				Error  *string
			}
		}
	}
	b, e := os.ReadFile(edgeReviewRoot + "source-boundaries333.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = Decode(b, &f); e != nil {
		t.Fatal(e)
	}
	return f
}
func edgeTyped(v any) any {
	switch x := v.(type) {
	case sourceHTTPMap:
		return edgeTyped(x.values)
	case nil:
		return Object{"type": "NoneType"}
	case bool:
		return Object{"type": "bool", "value": x}
	case string:
		points := []int{}
		for _, r := range sourceCodepoints(x) {
			points = append(points, int(r))
		}
		return Object{"type": "str", "value": points}
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return Object{"type": "int", "value": string(x)}
		}
		n, _ := strconv.ParseFloat(string(x), 64)
		return edgeTyped(n)
	case float64:
		s := fmt.Sprintf("%016x", math.Float64bits(x))
		if math.IsNaN(x) {
			s = "nan"
		} else if math.IsInf(x, 1) {
			s = "inf"
		} else if math.IsInf(x, -1) {
			s = "-inf"
		}
		return Object{"type": "float", "value": s}
	case []any:
		rows := []any{}
		for _, v := range x {
			rows = append(rows, edgeTyped(v))
		}
		return Object{"type": "list", "value": rows}
	case Object:
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rows := []any{}
		for _, k := range keys {
			rows = append(rows, []any{edgeTyped(k), edgeTyped(x[k])})
		}
		return Object{"type": "dict", "value": rows}
	}
	return fmt.Sprintf("unexpected%T", v)
}
func TestRefinementExtendedHTTPJSONRawBoundaries(t *testing.T) {
	for i, c := range edgeFixture(t).JSON {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			v, e := parseSiteJSON(c.Text)
			if c.Stdlib.Error != nil {
				if e == nil || e.Error() != *c.Stdlib.Error {
					t.Fatalf("rawHTTPlen=%d got=%v source=%s", len(c.Text), e, *c.Stdlib.Error)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if len(c.Text) >= 256 {
				return
			}
			got, _ := json.Marshal(edgeTyped(v))
			want, _ := json.Marshal(c.Stdlib.Value)
			if string(got) != string(want) {
				t.Fatalf("rawHTTP=%q GoTyped=%s sourceTyped=%s", c.Text, got, want)
			}
		})
	}
}

type edgeHaresPeer struct {
	text  string
	calls int
}

func (h *edgeHaresPeer) Do(context.Context, PageRequest) (*PageResponse, error) {
	h.calls++
	if h.calls == 1 {
		return &PageResponse{Status: 200, Text: "owned source HTML"}, nil
	}
	return &PageResponse{Status: 200, Text: h.text}, nil
}
func TestRefinementExtendedHTTPJSONHaresEffects(t *testing.T) {
	for i, c := range edgeFixture(t).JSON {
		if c.Hares == nil {
			continue
		}
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			p := &edgeHaresPeer{text: c.Text}
			v, e := JSONSignin(AdapterHares, context.Background(), Object{"name": "Owned", "cookie": "owned", "ua": "Owned/1", "timeout": 20, "proxy": false, "render": false}, SiteVisitHooks{HTTP: p})
			if c.Hares.Error != nil {
				if e == nil || e.Error() != *c.Hares.Error {
					t.Fatalf("len=%d target=%v state=%v sourceerror=%s actualCalls=%d", len(c.Text), e, v.State, *c.Hares.Error, p.calls)
				}
				return
			}
			if e != nil || !reflect.DeepEqual([]any{v.State, v.Message}, c.Hares.Result) {
				t.Fatalf("len=%d target=%v/%v source=%v actualCalls=%d", len(c.Text), v, e, c.Hares.Result, p.calls)
			}
		})
	}
}
func TestRefinementDateCIntAndPersistedFloatOverflow(t *testing.T) {
	for i, c := range edgeFixture(t).Dates {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			items := c.Items
			if c.ItemsRaw != "" {
				var e error
				items, e = parseSiteJSON(c.ItemsRaw)
				if e != nil {
					t.Fatal(e)
				}
			}
			got, e := ParseRandomSchedule(c.Date, items)
			if c.Error != nil {
				if e == nil || e.Error() != *c.Error {
					t.Fatalf("date=%s raw=%s targeterr=%v source=%s/%s", c.Date, c.ItemsRaw, e, *c.ErrorType, *c.Error)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			rows := []string{}
			for _, v := range got {
				rows = append(rows, v.Format("2006-01-02T15:04:05"))
			}
			if !reflect.DeepEqual(rows, c.Result) {
				t.Fatalf("got=%v source=%v", rows, c.Result)
			}
		})
	}
}
