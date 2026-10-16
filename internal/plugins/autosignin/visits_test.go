package autosignin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

var syntheticSite = Object{"id": 1, "name": "Synthetic", "url": "https://synthetic.invalid/library/", "cookie": "synthetic-cookie=not-a-credential", "ua": "synthetic-agent"}

type syntheticPages struct {
	site      Object
	responses []any
	calls     []any
}

func (s *syntheticPages) next() (Object, error) {
	if len(s.responses) == 0 {
		return nil, nil
	}
	raw := s.responses[0]
	s.responses = s.responses[1:]
	if raw == nil {
		return nil, nil
	}
	row := raw.(Object)
	if message, ok := row["error"].(string); ok {
		return nil, errors.New(message)
	}
	return row, nil
}
func (s *syntheticPages) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	proxy := any(nil)
	if Truth(s.site["proxy"]) {
		proxy = Object{"https": "synthetic-proxy"}
	}
	call := Object{"method": r.Method, "cookies": s.site["cookie"], "ua": s.site["ua"], "proxies": proxy, "timeout": r.Timeout, "url": r.URL}
	if r.Method == "POST" {
		call["referer"] = r.Referer
		call["data"] = r.Form
	}
	s.calls = append(s.calls, call)
	row, err := s.next()
	if row == nil || err != nil {
		return nil, err
	}
	status := 200
	if raw, ok := row["status"]; ok {
		n, _ := sourceInteger(raw)
		status = int(n.Int64())
	}
	return &PageResponse{Status: status, Text: textValue(row["text"])}, nil
}
func (s *syntheticPages) Page(ctx context.Context, r PageRequest) (string, error) {
	proxy := any(nil)
	if Truth(s.site["proxy"]) {
		proxy = "synthetic-proxy"
	}
	s.calls = append(s.calls, Object{"method": "BROWSER", "url": r.URL, "cookies": s.site["cookie"], "ua": s.site["ua"], "proxies": proxy, "timeout": r.Timeout})
	row, err := s.next()
	if row == nil || err != nil {
		return "", err
	}
	return textValue(row["text"]), nil
}
func normalizedResult(r Result) Object {
	return Object{"site_name": r.SiteName, "message": r.Message, "success": r.Success, "logged_in": r.LoggedIn}
}
func TestActualInstalledVisitAndEntryPoints(t *testing.T) {
	raw, err := readAutoSignInFixture("visit-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Name, Kind string
			Input      Object
			Expected   Object
			Error      Object
			Partial    Object
		}
	}
	if err = Decode(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.Cases) != 88 {
		t.Fatal("reference count changed")
	}
	for _, c := range ref.Cases {
		t.Run(c.Name, func(t *testing.T) {
			site := clone(syntheticSite).(Object)
			if custom, ok := c.Input["site"]; ok {
				site = custom.(Object)
			}
			pages := &syntheticPages{site: site, calls: []any{}, responses: asList(clone(c.Input["responses"]))}
			stats, messages, dos := []any{}, []any{}, []any{}
			clock := []time.Time{time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 12, 0, 3, 0, time.UTC)}
			if raw, ok := c.Input["clock"]; ok {
				clock = nil
				for _, value := range raw.([]any) {
					v, err := time.Parse("2006-01-02T15:04:05.999999", value.(string))
					if err != nil {
						t.Fatal(err)
					}
					clock = append(clock, v)
				}
			}
			now := func() time.Time {
				v := clock[0]
				if len(clock) > 1 {
					clock = clock[1:]
				}
				return v
			}
			hooks := SiteVisitHooks{HTTP: pages, Browser: pages, Now: now, Stats: func(_ context.Context, domain string, ok bool, seconds int) error {
				row := Object{"domain": domain, "success": ok}
				if ok {
					row["seconds"] = seconds
				}
				stats = append(stats, row)
				return nil
			}}
			visitor := SiteVisitor{Hooks: hooks}
			if Truth(c.Input["ptzone"]) {
				visitor.Adapters = []SiteAdapter{{Match: PTZoneMatch, Signin: PTZoneSignin}}
			}
			if schema, ok := c.Input["schema"].(Object); ok {
				execute := func(raw any) (AdapterResult, error) {
					r := raw.(Object)
					if message, ok := r["error"].(string); ok {
						return AdapterResult{}, errors.New(message)
					}
					return AdapterResult{r["state"], textValue(r["message"])}, nil
				}
				a := SiteAdapter{Match: func(string) (bool, error) { return true, nil }, SigninIsLogin: Truth(schema["signin_is_login"]), Signin: func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) { return execute(schema["signin"]) }}
				if v, ok := schema["login"]; ok {
					a.Login = func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) { return execute(v) }
				}
				visitor.Adapters = []SiteAdapter{a}
			}
			ctx := context.Background()
			var result any
			var runErr error
			switch c.Kind {
			case "generic-signin":
				result = normalizedResult(GenericSignin(ctx, site, hooks))
			case "generic-login":
				r, err := GenericLogin(ctx, site, hooks)
				runErr = err
				result = []any{r.State, r.Message}
			case "ptzone":
				r, err := PTZoneSignin(ctx, site, hooks)
				runErr = err
				result = []any{r.State, r.Message}
			case "signin", "login":
				op := "签到"
				if c.Kind == "login" {
					op = "登录"
				}
				r, err := visitor.Visit(ctx, site, op, true)
				runErr = err
				result = normalizedResult(r)
			case "api":
				address := "https://synthetic.invalid/"
				if value, ok := c.Input["url"].(string); ok {
					address = value
				}
				accepted := true
				if key, ok := c.Input["apikey"]; ok {
					accepted = key == "synthetic-api-key"
				}
				result, runErr = SigninByDomain(ctx, address, DomainAPIHooks{KeyAccepted: accepted, Lookup: func(_ context.Context, domain string) (Object, error) {
					for _, row := range asList(c.Input["indexers"]) {
						s := row.(Object)
						if s["domain"] == domain {
							return s, nil
						}
					}
					return nil, nil
				}, Visit: func(ctx context.Context, s Object) (Result, error) {
					pages.site = s
					return visitor.Visit(ctx, s, "签到", true)
				}})
			case "event":
				e := New(Inventory{}, func() time.Time { return clock[0] })
				e.StartTime = c.Input["start"]
				e.EndTime = c.Input["end"]
				e.SignSites = c.Input["sign_sites"]
				e.LoginSites = c.Input["login_sites"]
				var event *EventEnvelope
				if data, ok := c.Input["event"].(Object); ok {
					event = &EventEnvelope{Data: data}
				}
				eh := EntryHooks{HistoryKeys: func() []string { return nil }, Run: RunHooks{Notify: func(_ context.Context, row Object) error { messages = append(messages, row); return nil }}}
				runErr = e.signIn(ctx, event, eh, func(_ context.Context, today time.Time, op string, selected any, ev *SourceEvent, keys []string, h RunHooks) error {
					row := Object{"today": today.Format("2006-01-02T15:04:05"), "type_str": op, "do_sites": selected, "event": nil}
					if event != nil {
						row["event"] = event.Data
					}
					dos = append(dos, row)
					return nil
				})
			}
			got := Object{"result": result, "calls": pages.calls, "stats": stats, "messages": messages, "do": dos}
			if c.Error != nil {
				if runErr == nil {
					t.Fatalf("expected source error %v got success %v", c.Error, got)
				}
				if c.Error["class"] != "TypeError" && runErr.Error() != c.Error["message"] {
					t.Errorf("error got %v want %v", runErr, c.Error)
				}
				delete(got, "result")
				if !reflect.DeepEqual(canonical(got), canonical(c.Partial)) {
					t.Errorf("partial got %#v want %#v", canonical(got), canonical(c.Partial))
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if !reflect.DeepEqual(canonical(got), canonical(c.Expected)) {
				t.Errorf("got %#v\nwant %#v", canonical(got), canonical(c.Expected))
			}
		})
	}
}
