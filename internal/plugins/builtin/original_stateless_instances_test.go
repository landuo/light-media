package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func originalStatelessInput(t *testing.T, config string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(plugins.Invocation{Action: "init", Config: json.RawMessage(config)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOriginalCategoryRetainedMutationOrder(t *testing.T) {
	sentinel := errors.New("synthetic host failure")
	for _, fail := range []string{"category.write", "notification.post", "config.patch", ""} {
		t.Run(fail, func(t *testing.T) {
			p := NewOriginalCategoryEditor()
			calls := []string{}
			host := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				calls = append(calls, method)
				want := method != "config.patch"
				if p.State() != want {
					t.Fatalf("member state at %s=%v want=%v", method, p.State(), want)
				}
				if method == fail {
					return nil, sentinel
				}
				return json.RawMessage(`{}`), nil
			}
			_, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"enabled":true,"content":"synthetic"}`), host)
			if (fail != "") != errors.Is(err, sentinel) {
				t.Fatalf("err=%v", err)
			}
			want := fail == "category.write" || fail == "notification.post"
			if p.State() != want {
				t.Fatalf("retained=%v want=%v", p.State(), want)
			}
			expected := []string{"category.write"}
			if fail != "category.write" {
				expected = append(expected, "notification.post")
			}
			if fail != "category.write" && fail != "notification.post" {
				expected = append(expected, "config.patch")
			}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("calls=%v expected=%v", calls, expected)
			}
			for _, empty := range []string{`null`, `{}`} {
				before := len(calls)
				if _, err = p.Handle(context.Background(), "invoke", originalStatelessInput(t, empty), host); err != nil {
					t.Fatal(err)
				}
				if p.State() != want || len(calls) != before {
					t.Fatal("empty config reset/replayed category")
				}
			}
			if NewOriginalCategoryEditor().State() {
				t.Fatal("source/sibling state leaked")
			}
		})
	}
}

func TestOriginalMediaRetainsStateWithoutReplayingGetter(t *testing.T) {
	p := NewOriginalMediaServerRefresh()
	calls := 0
	sentinel := errors.New("synthetic event registration failure")
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "events.configure" {
			t.Fatalf("unexpected %s", method)
		}
		if !p.State() {
			t.Fatal("state not assigned before host")
		}
		return nil, sentinel
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"enabled":true,"delay":0,"mediaservers":[]}`), host); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if !p.State() {
			t.Fatal("state lost")
		}
	}
	if calls != 1 {
		t.Fatal("getter replayed events")
	}
	for _, empty := range []string{`null`, `{}`} {
		if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, empty), host); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		if !p.State() {
			t.Fatal("empty direct init reset state")
		}
	}
	c := p.Configuration()
	c.Delay[0] = '9'
	if string(p.Configuration().Delay) != "0" {
		t.Fatal("exposed mutable delay")
	}
	if NewOriginalMediaServerRefresh().State() {
		t.Fatal("sibling state leaked")
	}
}

func TestOriginalSiteStatisticOnceMemberAndNoneFailure(t *testing.T) {
	p := NewOriginalSiteStatistic()
	calls := 0
	host := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "site.statistics.configure" {
			t.Fatalf("unexpected %s", method)
		}
		if !p.State() || !p.Configuration().OnlyOnce {
			t.Fatal("actual members not set before host")
		}
		return json.RawMessage(`{"configured":true}`), nil
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"enabled":true,"onlyonce":true}`), host); err != nil {
		t.Fatal(err)
	}
	if !p.Configuration().OnlyOnce || p.Configuration().DashboardType != "today" {
		t.Fatal("normalized persistence confused with retained member")
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `null`), host); err == nil {
		t.Fatal("original None item assignment error hidden")
	}
	if calls != 1 || !p.State() || !p.Configuration().OnlyOnce {
		t.Fatal("None altered prior members/ran host")
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{}`), host); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !p.Configuration().OnlyOnce {
		t.Fatal("empty dict should preserve and reuse once member")
	}
	if NewOriginalSiteStatistic().State() {
		t.Fatal("sibling state leaked")
	}
}

func TestOriginalSiteStatisticPartialHostErrorRetainsMembers(t *testing.T) {
	p := NewOriginalSiteStatistic()
	sentinel := errors.New("synthetic configure failure")
	host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"enabled":true,"onlyonce":false}`), host); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !p.State() {
		t.Fatal("error discarded assigned member")
	}
	for _, empty := range []string{`null`, `{}`} {
		if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, empty), host); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		if !p.State() {
			t.Fatal("empty direct init reset member")
		}
	}
}

func TestOriginalMergeReplacesNullAndUsesLiveDependency(t *testing.T) {
	p := NewOriginalMergeSiteSwitch()
	calls := 0
	sentinel := errors.New("synthetic configure failure")
	host := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "sites.controls.configure" {
			t.Fatal(method)
		}
		return nil, sentinel
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"signin_follow_enable_sites":true}`), host); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if p.State(nil) || !p.State([]string{"AutoSignIn"}) || p.State([]string{"Unrelated"}) {
		t.Fatal("dependency rules")
	}
	if calls != 1 {
		t.Fatal("getter host call")
	}
	c := p.Configuration()
	c["signin_follow_enable_sites"][0] = 'f'
	if !p.State([]string{"AutoSignIn"}) {
		t.Fatal("exposed mutable config")
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `null`), host); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if p.State([]string{"AutoSignIn"}) {
		t.Fatal("None failed to replace member")
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"cross_seed_follow_enable_sites":true,"brush_flow_follow_enable_sites":true}`), host); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if p.State([]string{"CrossSeed", "BrushFlowLanduo"}) || !p.State([]string{"BrushFlow"}) {
		t.Fatal("original omission/identity changed")
	}
	if NewOriginalMergeSiteSwitch().State([]string{"AutoSignIn"}) {
		t.Fatal("sibling state leaked")
	}
}

func TestOriginalMediaEventUsesRetainedMembersNotInvocation(t *testing.T) {
	p := NewOriginalMediaServerRefresh()
	calls := []string{}
	host := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		return json.RawMessage(`{}`), nil
	}
	if _, err := p.Handle(context.Background(), "invoke", originalStatelessInput(t, `{"enabled":true,"delay":0,"mediaservers":["synthetic"]}`), host); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plugins.Invocation{Action: "event", Config: json.RawMessage(`{"enabled":false}`), Input: json.RawMessage(`{"batch_id":"synthetic"}`)})
	if _, err := p.Handle(context.Background(), "invoke", raw, host); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"events.configure", "media.refresh"}) || !p.State() {
		t.Fatalf("calls=%v state=%v", calls, p.State())
	}
	// Legacy API still uses the supplied disabled config and skips the host.
	calls = nil
	if _, err := HandleMediaServerRefresh(context.Background(), "invoke", raw, host); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatal("legacy single-call behavior changed")
	}
}
