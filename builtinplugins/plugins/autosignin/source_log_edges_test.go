package autosignin

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActualSourceLogEdges12(t *testing.T) {
	raw, err := os.ReadFile("reference/log-edges12.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct {
			Name   string
			Logs   [][]string
			Error  Object
			Result any
		}
	}
	if err = Decode(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Cases) != 12 {
		t.Fatal(len(source.Cases))
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			logs := [][]string{}
			logger := func(level, message string) error { logs = append(logs, []string{level, message}); return nil }
			e := New(Inventory{}, func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
			e.LogLive = logger
			site := Object{"name": "Owned", "url": "https://owned.invalid/", "cookie": "owned=fixture", "render": false, "timeout": 20, "proxy": false, "ua": "Owned"}
			switch {
			case strings.HasPrefix(c.Name, "services:"):
				e.Enabled = true
				e.Cron = strings.TrimPrefix(c.Name, "services:")
				_, err = e.Services(nil, "UTC")
			case strings.HasPrefix(c.Name, "stop:"):
				e.Scheduler = &scheduler{Running: true}
				stage := strings.TrimPrefix(c.Name, "stop:")
				e.Effects = func(effect Effect) error {
					if effect.Kind == "scheduler_"+stage || stage == "shutdown" && effect.Kind == "scheduler_stop" {
						return &SourceFailure{Message: "owned " + stage + " error", Class: "ValueError"}
					}
					return nil
				}
				err = e.Stop()
				if e.Scheduler == nil {
					t.Fatal("source exception must retain scheduler")
				}
			case c.Name == "once":
				err = e.Init(Object{"enabled": false, "onlyonce": true, "sign_sites": []any{}, "login_sites": []any{}})
			case strings.HasPrefix(c.Name, "entry:"):
				if c.Name == "entry:window" {
					e.StartTime = 9
					e.EndTime = 10
				}
				err = e.SignIn(context.Background(), &EventEnvelope{Data: Object{"action": "site_signin", "channel": "owned", "user": "owned"}}, EntryHooks{Run: RunHooks{Notify: func(context.Context, Object) error { return nil }}})
			case strings.HasPrefix(c.Name, "generic:"):
				h := SiteVisitHooks{Log: logger, HTTP: pageTransportFunc(func(context.Context, PageRequest) (*PageResponse, error) {
					if strings.HasSuffix(c.Name, "error") {
						return nil, errors.New("owned request error")
					}
					return &PageResponse{Status: 200, Text: `<a href="logout.php">out</a>`}, nil
				})}
				if c.Name == "generic:login-error" {
					result, callErr := GenericLogin(context.Background(), site, h)
					err = callErr
					if callErr == nil {
						sourceCheck(t, []any{result.State, result.Message}, c.Result)
					}
				} else {
					result := GenericSignin(context.Background(), site, h)
					sourceCheck(t, []any{result.SiteName, result.Message, result.Success, result.LoggedIn}, c.Result)
				}
			case c.Name == "base:chardet-none":
				var text string
				text, err = decodeSourcePage([]byte{0x81}, func() string { return "�" }, logger)
				if err == nil {
					sourceCheck(t, text, c.Result)
				}
			default:
				t.Fatal(c.Name)
			}
			if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, logs, c.Logs)
		})
	}
}
