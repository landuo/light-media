package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRegistrationHostGuardPropagation(t *testing.T) {
	sentinel := errors.New("synthetic host denial")
	for _, loaded := range []bool{false, true} {
		for _, address := range []string{"https://example.invalid/", "https://pt.btschool.club/", "https://kp.m-team.cc/"} {
			t.Run(address+map[bool]string{false: "/generic", true: "/loaded"}[loaded], func(t *testing.T) {
				checks := 0
				registration := Registration{
					Site:          Object{"id": "test", "url": address, "token": "synthetic-token"},
					BeforeRequest: func(context.Context) error { checks++; return sentinel },
				}
				registered := NewRegisteredSites([]Registration{registration})
				hooks, err := BindRegistrationHooks(registered.rows[0], loaded)
				if err != nil {
					t.Fatal(err)
				}
				assertBound := func(bound *BoundSiteHTTP) {
					t.Helper()
					before := checks
					response, err := bound.Do(context.Background(), PageRequest{Method: "GET", URL: bound.binding.URL})
					if response != nil || !errors.Is(err, sentinel) || checks != before+1 {
						t.Fatal("host guard was lost or denial swallowed", response, err, checks)
					}
				}
				switch transport := hooks.HTTP.(type) {
				case *BoundSiteHTTP:
					assertBound(transport)
				case *FixedSiteGrant:
					if len(transport.clients) == 0 {
						t.Fatal("fixed grant has no clients")
					}
					for _, client := range transport.clients {
						assertBound(client.(*BoundSiteHTTP))
					}
				default:
					t.Fatalf("unexpected transport %T", transport)
				}
				if loaded && MTeamMatch(address) {
					grant, ok := hooks.MTeam.(*BoundMTeam)
					if !ok {
						t.Fatalf("unexpected MTeam grant %T", hooks.MTeam)
					}
					assertBound(grant.api.(*BoundSiteHTTP))
					credentials := Credentials{URL: address, Token: "synthetic-token"}
					plan, ok := MTeamPlan(credentials, 1767355200000)
					if !ok {
						t.Fatal("missing MTeam plan")
					}
					before := checks
					response, err := grant.Post(context.Background(), plan)
					if response != nil || !errors.Is(err, sentinel) || checks != before+1 {
						t.Fatal("MTeam API did not enforce host guard", response, err, checks)
					}
				}
			})
		}
	}
}

func TestSiteHostGuardIsNotSerialized(t *testing.T) {
	for _, value := range []any{
		Registration{Site: Object{"url": "https://example.invalid"}, BeforeRequest: func(context.Context) error { return nil }},
		SiteHTTPBinding{URL: "https://example.invalid", BeforeRequest: func(context.Context) error { return nil }},
	} {
		field, ok := reflect.TypeOf(value).FieldByName("BeforeRequest")
		if !ok || field.Tag.Get("json") != "-" {
			t.Fatal("host guard field lacks explicit serialization exclusion")
		}
		// Registration also contains existing function fields, so test its
		// exclusion directly; the HTTP binding can be marshaled end to end.
		if _, ok := value.(Registration); ok {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["BeforeRequest"]; ok {
			t.Fatal("host guard was serialized")
		}
	}
}
