package autosignin

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRefinementJSONConsumerValuePreservation(t *testing.T) {
	raw, e := os.ReadFile(edgeReviewRoot + "consumer-edge-source333.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Yema []struct {
			Text    string
			State   bool
			Message []int `json:"message_points"`
			Error   *string
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Yema {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("rawHTTP=%s source returns message; target panic=%v", c.Text, r)
				}
			}()
			peer := edgeHTTPFunction(func(context.Context, PageRequest) (*PageResponse, error) {
				return &PageResponse{Status: 200, Text: c.Text}, nil
			})
			v, e := YemaVisit(context.Background(), Object{"url": "https://yemapt.org/", "cookie": "owned", "ua": "Owned/1"}, SiteVisitHooks{HTTP: peer}, false)
			if c.Error != nil {
				t.Fatal("unexpected source fixture exception", *c.Error)
			}
			points := []int{}
			for _, r := range sourceCodepoints(v.Message) {
				points = append(points, int(r))
			}
			if e != nil || v.State != c.State || !reflect.DeepEqual(points, c.Message) {
				t.Errorf("rawHTTP=%s target=%v/%v points=%v sourcepoints=%v", c.Text, v, e, points, c.Message)
			}
		})
	}
}
func TestRefinementNestedMalformedDailyHistoryStopsBeforeVisit(t *testing.T) {
	raw, e := os.ReadFile(edgeReviewRoot + "consumer-edge-source333.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		History []struct {
			History any
			Error   string
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.History {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			engine := New(Inventory{Indexers: []Object{{"id": 1, "name": "Owned", "public": false}}}, func() time.Time { return now })
			engine.Data["签到-2026-11-06"] = c.History
			engine.RetryKeyword = "never"
			visits, puts := 0, 0
			engine.Effects = func(effect Effect) error {
				if effect.Kind == "history.put" {
					puts++
				}
				return nil
			}
			e := engine.Do(context.Background(), now, "签到", []any{"all"}, nil, nil, RunHooks{Visit: func(context.Context, Object, string, bool) (Result, error) {
				visits++
				return Result{SiteName: "Owned", Message: "签到成功", Success: true, LoggedIn: true}, nil
			}})
			want := strings.TrimPrefix(c.Error, "AttributeError:")
			if e == nil || e.Error() != want || visits != 0 || puts != 0 {
				t.Errorf("history=%s source=%s got=%v visits=%d puts=%d", fmt.Sprint(c.History), c.Error, e, visits, puts)
			}
		})
	}
}

type edgeHTTPFunction func(context.Context, PageRequest) (*PageResponse, error)

func (f edgeHTTPFunction) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	return f(ctx, r)
}
