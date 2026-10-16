package autosignin

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestActualInstalledFailureBoundaries(t *testing.T) {
	raw, err := readAutoSignInFixture("durable/effect-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Scene       string
			FailureKind string `json:"failure_kind"`
			Occurrence  int    `json:"failure_occurrence"`
			Expected    Object
			Error       Object
			Attempts    []any
		}
	}
	if Decode(raw, &ref) != nil || len(ref.Cases) != 23 {
		t.Fatal("reference inventory", len(ref.Cases))
	}
	for _, c := range ref.Cases {
		t.Run(c.Scene+"-"+c.FailureKind+string(rune('0'+c.Occurrence)), func(t *testing.T) {
			engine := New(fixtureInventory(Object{}), func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
			config := Object{"enabled": true, "notify": true, "sign_sites": []any{"all"}, "login_sites": []any{}, "retry_keyword": "Cookie已失效", "auto_cf": 1}
			if c.Scene != "init" {
				initial := config
				if c.Scene == "stop" {
					initial = Object{"enabled": false, "onlyonce": true}
				}
				if err := engine.Init(initial); err != nil {
					t.Fatal(err)
				}
			}
			if c.Scene == "do" {
				engine.Data = Object{"签到-2025-12-01": Object{}, "12月1日": []any{}}
			}
			engine.Trace = []Object{}
			attempts := []any{}
			counts := map[string]int{}
			boundary := func(kind string) error {
				counts[kind]++
				attempts = append(attempts, Object{"kind": kind, "occurrence": counts[kind]})
				if kind == c.FailureKind && counts[kind] == c.Occurrence {
					return &SourceFailure{Message: "synthetic source boundary failure"}
				}
				return nil
			}
			engine.Effects = func(e Effect) error { return boundary(e.Kind) }
			var err error
			switch c.Scene {
			case "init":
				err = engine.Init(Object{"enabled": false, "onlyonce": true, "sign_sites": []any{1}})
			case "stop":
				err = engine.Init(Object{"enabled": false})
			case "deleted":
				err = engine.SiteDeleted(1)
			case "do":
				err = engine.Do(context.Background(), engine.Now(), "签到", []any{"all"}, &SourceEvent{Channel: "synthetic-channel", User: "synthetic-user"}, []string{"签到-2025-12-01", "12月1日"}, RunHooks{Visit: func(_ context.Context, site Object, _ string, _ bool) (Result, error) {
					if equal(site["id"], 1) {
						return Result{SiteName: "Alpha", Message: "签到失败，Cookie已失效"}, nil
					}
					return Result{SiteName: "Beta", Message: "签到成功", Success: true, LoggedIn: true}, nil
				}, Emit: func(context.Context, string, Object) error { return boundary("emit") }, Notify: func(context.Context, Object) error { return boundary("notify") }})
			}
			if c.Error != nil {
				if err == nil || err.Error() != c.Error["message"] {
					t.Fatal("source exception boundary differs", err, c.Error)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !equivalent(canonical(engine.Snapshot()), canonical(c.Expected)) {
				t.Fatalf("source partial state differs\ngot=%s\nwant=%s", mustJSON(engine.Snapshot()), mustJSON(c.Expected))
			}
			if !reflect.DeepEqual(canonical(attempts), canonical(c.Attempts)) {
				t.Fatal("source operation attempt order", attempts, c.Attempts)
			}
		})
	}
}
