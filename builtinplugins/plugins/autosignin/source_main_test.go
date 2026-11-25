package autosignin

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestActualSourceMainLog8(t *testing.T) {
	raw, err := os.ReadFile("reference/main-log8.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Cases []struct {
			Name                                   string
			Settings, Initial, Data, Config, Error Object
			Rows                                   []Object
			Messages, Events                       []any
			Logs                                   [][]string
		}
	}
	if err = Decode(raw, &original); err != nil {
		t.Fatal(err)
	}
	if len(original.Cases) != 8 {
		t.Fatal(len(original.Cases))
	}
	for _, c := range original.Cases {
		t.Run(c.Name, func(t *testing.T) {
			e := New(Inventory{Indexers: c.Rows}, func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
			e.SignSites = []any{"all"}
			e.LoginSites = []any{}
			e.Data = asObject(clone(c.Initial))
			e.RetryKeyword = c.Settings["retry"]
			e.Notify = c.Settings["notify"]
			if e.Notify == nil {
				e.Notify = false
			}
			e.Clean = c.Settings["clean"]
			if e.Clean == nil {
				e.Clean = false
			}
			if value, ok := c.Settings["queue"]; ok {
				e.QueueCount = value
			}
			if value, ok := c.Settings["auto_cf"]; ok {
				e.AutoCF = value
			}
			logs := [][]string{}
			messages := []any{}
			events := []any{}
			e.LogLive = func(level, message string) error { logs = append(logs, []string{level, message}); return nil }
			keys := []string{}
			for key := range c.Initial {
				keys = append(keys, key)
			}
			err := e.Do(context.Background(), e.Now(), "签到", []any{"all"}, nil, keys, RunHooks{Visit: func(_ context.Context, site Object, _ string, _ bool) (Result, error) {
				if Truth(c.Settings["failed"]) {
					message := "签到失败"
					if equal(site["id"], 1) {
						message = "Cookie已失效"
					}
					return Result{site["name"], message, false, false}, nil
				}
				message := "今日已签到"
				if equal(site["id"], 1) {
					message = "签到成功"
				}
				return Result{site["name"], message, true, true}, nil
			}, Emit: func(_ context.Context, _ string, data Object) error {
				events = append(events, []any{"plugin.action", clone(data)})
				return nil
			}, Notify: func(_ context.Context, data Object) error {
				fields := clone(data).(Object)
				if fields["mtype"] == "NotificationType.SiteMessage" {
					fields["mtype"] = "站点"
				}
				messages = append(messages, []any{"message", fields})
				return nil
			}})
			if c.Error == nil && err != nil {
				t.Fatal(err)
			}
			if c.Error != nil && err == nil {
				t.Fatal("source error became success")
			}
			// CPython re.error wording and Go compile-error wording differ; the
			// failure position, partial history/events and every business log do not.
			sourceCheck(t, e.Data, c.Data)
			sourceCheck(t, e.Config, c.Config)
			sourceCheck(t, messages, c.Messages)
			sourceCheck(t, events, c.Events)
			sourceCheck(t, logs, c.Logs)
		})
	}
}
func TestActualSourcePageRawName8(t *testing.T) {
	raw, err := os.ReadFile("reference/page-name8.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Cases []struct {
			Name   string
			Names  []any
			Result any
			Error  Object
		}
	}
	if err = Decode(raw, &original); err != nil {
		t.Fatal(err)
	}
	if len(original.Cases) != 8 {
		t.Fatal(len(original.Cases))
	}
	for _, c := range original.Cases {
		t.Run(c.Name, func(t *testing.T) {
			e := New(Inventory{}, func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
			records := []any{}
			for _, name := range c.Names {
				records = append(records, Object{"site": name, "status": "签到成功", "type": "签到"})
			}
			e.Data["11月5日"] = records
			page, err := e.Page()
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatalf("source error got %v want %v", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, page, c.Result)
		})
	}
}

func TestActualSourceHistoryStatus27(t *testing.T) {
	raw, err := os.ReadFile("reference/page-status27.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Record Object
			Result any
			Error  Object
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 27 {
		t.Fatal(len(source.Cases))
	}
	for i, c := range source.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e := New(Inventory{}, func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
			e.Data["11月5日"] = []any{clone(c.Record)}
			page, err := e.Page()
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatalf("got %v want actual source %v", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, page, c.Result)
		})
	}
}

func TestActualSourceQueueTypes11(t *testing.T) {
	raw, err := os.ReadFile("reference/queue-types11.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name                          string
			Settings, Data, Config, Error Object
			Rows                          []Object
			Logs                          [][]string
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 11 {
		t.Fatal(len(source.Cases))
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			e := New(Inventory{Indexers: c.Rows}, func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
			e.QueueCount = c.Settings["queue"]
			e.SignSites = []any{"all"}
			e.LoginSites = []any{}
			e.Notify = false
			e.Clean = false
			logs := [][]string{}
			e.LogLive = func(level, message string) error { logs = append(logs, []string{level, message}); return nil }
			err := e.Do(context.Background(), e.Now(), "签到", []any{"all"}, nil, nil, RunHooks{Visit: func(_ context.Context, site Object, _ string, _ bool) (Result, error) {
				return Result{site["name"], "签到成功", true, true}, nil
			}})
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatalf("got %v want actual source %v", err, c.Error)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, e.Data, c.Data)
			sourceCheck(t, e.Config, c.Config)
			sourceCheck(t, logs, c.Logs)
		})
	}
}
