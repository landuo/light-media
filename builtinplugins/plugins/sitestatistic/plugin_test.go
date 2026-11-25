package sitestatistic

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

// These are synthetic typed resources, not real App/site/network acceptance.
type sdkFixture struct {
	mu        sync.Mutex
	p         *Plugin
	latest    []Data
	history   map[string][]Data
	active    []string
	store     map[string]json.RawMessage
	calls     []string
	logs      []plugins.LogEntryV1
	notices   []map[string]any
	updates   []map[string]any
	submitted []struct {
		Method  string
		Payload json.RawMessage
	}
	indexer                                    *plugins.SiteSourceIndexerV1
	refresh                                    json.RawMessage
	refreshCalls, allCalls                     int
	failSave, failPost, queryError, failSubmit bool
	autoPrivate                                bool
	dateHook                                   func(string)
	errors                                     chan error
}

func newFixture() *sdkFixture {
	id := "owned-site"
	return &sdkFixture{history: map[string][]Data{}, active: []string{"owned.invalid"}, store: map[string]json.RawMessage{}, errors: make(chan error, 1000), indexer: &plugins.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(`{"id":7,"name":"Owned","domain":"owned.invalid","url":"https://owned.invalid/","is_active":true}`)}, refresh: json.RawMessage(`null`)}
}
func fixtureRow(day string, upload float64) Data {
	return Data{"id": 1, "name": "Owned", "domain": "owned.invalid", "updated_day": day, "username": "owned", "user_level": "User", "upload": upload, "download": 100.0, "ratio": 2.0, "bonus": 1.0, "seeding": 2.0, "seeding_size": 1024.0, "err_msg": ""}
}
func (s *sdkFixture) host(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method)
	switch method {
	case plugins.SitesSourceAccountsCapability:
		if s.queryError {
			return nil, errors.New("owned account guard failure")
		}
		var in plugins.SiteAccountHostRequestV1
		_ = json.Unmarshal(raw, &in)
		rows := s.latest
		if !in.Latest {
			if s.dateHook != nil {
				s.dateHook(in.Date)
			}
			rows = s.history[in.Date]
		}
		items := []plugins.SiteAccountHostRowV1{}
		for _, row := range rows {
			fields, _ := json.Marshal(row)
			items = append(items, plugins.SiteAccountHostRowV1{SiteID: "owned-site", Fields: fields})
		}
		return json.Marshal(plugins.SiteAccountHostResultV1{SchemaVersion: 1, Items: items})
	case plugins.SitesReadCapability:
		items := []plugins.SiteHostRowV1{}
		for _, domain := range s.active {
			items = append(items, plugins.SiteHostRowV1{ID: "owned-site", SourceID: 7, Domain: domain, Name: "Owned", Enabled: true})
		}
		return json.Marshal(plugins.SiteHostResultV1{SchemaVersion: 1, Items: items})
	case plugins.BaseGetData:
		var in struct{ Key string }
		_ = json.Unmarshal(raw, &in)
		if value, ok := s.store[in.Key]; ok {
			return value, nil
		}
		return json.RawMessage(`null`), nil
	case plugins.BaseSaveData:
		if s.failSave {
			return nil, errors.New("owned save failure")
		}
		var in struct {
			Key   string
			Value json.RawMessage
		}
		_ = json.Unmarshal(raw, &in)
		s.store[in.Key] = append(json.RawMessage{}, in.Value...)
		return json.RawMessage(`null`), nil
	case plugins.BaseUpdateConfig:
		var in struct{ Config map[string]any }
		_ = json.Unmarshal(raw, &in)
		s.updates = append(s.updates, in.Config)
		return json.RawMessage(`false`), nil
	case plugins.LogWriteCapability:
		var in plugins.LogEntryV1
		_ = json.Unmarshal(raw, &in)
		s.logs = append(s.logs, in)
		return json.RawMessage(`null`), nil
	case plugins.PostMessageCapability:
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		s.notices = append(s.notices, in)
		if s.failPost {
			return nil, errors.New("owned post failure")
		}
		return json.Marshal(plugins.NotificationDispatch{})
	case plugins.SitesSourceIndexerCapability:
		return json.Marshal(plugins.SiteSourceIndexerResultV1{SchemaVersion: 1, Indexer: s.indexer})
	case plugins.SitesSourceRefreshCapability:
		s.refreshCalls++
		return json.Marshal(plugins.SiteSourceRefreshResultV1{SchemaVersion: 1, Data: s.refresh})
	case plugins.SitesSourceRefreshAllCapability:
		s.allCalls++
		return json.Marshal(plugins.SiteSourceRefreshResultV1{SchemaVersion: 1, Data: json.RawMessage(`{}`)})
	case plugins.BackgroundSubmitCapability:
		if s.failSubmit {
			return nil, errors.New("owned admission failure")
		}
		var in struct {
			Method  string
			Payload json.RawMessage
		}
		_ = json.Unmarshal(raw, &in)
		s.submitted = append(s.submitted, in)
		if in.Method == "data_continue" || s.autoPrivate {
			go func() {
				_, e := s.p.Handle(context.Background(), in.Method, in.Payload, s.host)
				if e != nil {
					s.errors <- e
				}
			}()
		}
		return json.RawMessage(`{"accepted":true}`), nil
	}
	return nil, errors.New("unexpected synthetic resource: " + method)
}
func invoke(t *testing.T, p *Plugin, s *sdkFixture, method string, input any) json.RawMessage {
	t.Helper()
	s.p = p
	raw, e := json.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	value, e := p.Handle(context.Background(), method, raw, s.host)
	if e != nil {
		t.Fatal(method, e)
	}
	return value
}
func fixedPlugin() *Plugin {
	p := New()
	at, _ := time.Parse(time.RFC3339, "2026-10-16T12:34:56+08:00")
	p.now = func() time.Time { return at }
	return p
}

func TestConfigPreservesRawStateFalseyInitAndLiteralForm(t *testing.T) {
	p, s := fixedPlugin(), newFixture()
	raw := invoke(t, p, s, "get_form", nil)
	var pair []json.RawMessage
	_ = json.Unmarshal(raw, &pair)
	sameSourceJSON(t, "literal form", pair[0], form)
	sameSourceJSON(t, "literal defaults", pair[1], defaults)
	invoke(t, p, s, "init", map[string]any{"enabled": json.Number("1"), "dashboard_type": "other", "notify_type": json.Number("2")})
	for _, input := range []any{nil, map[string]any{}, false, []any{}, 0, ""} {
		invoke(t, p, s, "init", input)
		if !sourceEqual(p.currentConfig().Enabled, json.Number("1")) {
			t.Fatal("falsey init mutated members")
		}
	}
	if string(invoke(t, p, s, "get_state", nil)) != "1" {
		t.Fatal("source state coerced to bool")
	}
	value := invoke(t, p, s, "get_dashboard", map[string]any{"key": ""})
	sameSourceJSON(t, "unknown mode", json.RawMessage(value), json.RawMessage(`[{"cols":12},{},[{"component":"VRow","content":[]}]]`))
	invoke(t, p, s, "init", map[string]any{"enabled": false})
	if p.currentConfig().OnlyOnce != nil || p.currentConfig().Notify != "" || p.currentConfig().Dashboard != "today" {
		t.Fatal(p.currentConfig())
	}
	for _, m := range []string{"stop_service", "get_service", "get_command", "close"} {
		if string(invoke(t, p, s, m, nil)) != "null" {
			t.Fatal(m)
		}
	}
	var apis []map[string]any
	_ = json.Unmarshal(invoke(t, p, s, "get_api", nil), &apis)
	if len(apis) != 1 || apis[0]["summary"] != "刷新站点数据" || apis[0]["description"] != "刷新对应域名的站点数据" || apis[0]["endpoint"] != "refresh_by_domain" {
		t.Fatal("source API metadata lost", apis)
	}
	if len(s.updates) != 0 {
		t.Fatal("non-one-shot saved configuration")
	}
}
func TestSourceNotificationDedupPostSaveAndMidQueryReload(t *testing.T) {
	p, s := fixedPlugin(), newFixture()
	s.latest = []Data{fixtureRow("2026-10-16", 200)}
	s.history["2026-10-15"] = []Data{fixtureRow("2026-10-15", 100)}
	invoke(t, p, s, "init", map[string]any{"enabled": false, "notify_type": "inc"})
	invoke(t, p, s, "send_msg", nil)
	invoke(t, p, s, "send_msg", map[string]any{"type": "site.refreshed", "data": map[string]any{"site_id": 7}})
	if len(s.calls) != 0 {
		t.Fatal("ignored event reached source resources", s.calls)
	}
	event := map[string]any{"type": "site.refreshed", "data": map[string]any{"site_id": "*"}}
	invoke(t, p, s, "send_msg", event)
	if len(s.notices) != 1 || s.store["last_notify"] == nil {
		t.Fatal("notification not persisted")
	}
	invoke(t, p, s, "send_msg", event)
	if len(s.notices) != 1 {
		t.Fatal("identical body repeated")
	}
	s.store = map[string]json.RawMessage{}
	s.failSave = true
	raw, _ := json.Marshal(event)
	if _, e := p.Handle(context.Background(), "send_msg", raw, s.host); e == nil {
		t.Fatal("save failure swallowed")
	}
	if len(s.notices) != 2 || len(s.store) != 0 {
		t.Fatal("post/save order changed")
	}
	s.failSave = false
	invoke(t, p, s, "send_msg", event)
	if len(s.notices) != 3 {
		t.Fatal("failed save created dedup receipt")
	}
	s.store = map[string]json.RawMessage{}
	once := false
	s.dateHook = func(string) {
		if !once {
			once = true
			c := p.currentConfig()
			c.Notify = ""
			p.setConfig(c)
		}
	}
	invoke(t, p, s, "send_msg", event)
	if len(s.notices) != 4 || !strings.Contains(s.notices[3]["text"].(string), "上传量：200.0B") {
		t.Fatal("late falsey mode introduced a second gate", s.notices)
	}
}
func TestAPIUsesCurrentRawDTOAndCanonicalIdentity(t *testing.T) {
	p, s := fixedPlugin(), newFixture()
	invoke(t, p, s, "init", map[string]any{"enabled": false})
	request := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Query: map[string][]string{"domain": {"owned.invalid"}}, CredentialMatches: map[string]bool{"apikey": false}}
	var response plugins.HTTPResponse
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &response)
	if !strings.Contains(string(response.Body), "API密钥错误") || len(s.calls) != 0 {
		t.Fatal("wrong key reached indexer")
	}
	request.CredentialMatches["apikey"] = true
	s.indexer = nil
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &response)
	if !strings.Contains(string(response.Body), "不存在") {
		t.Fatal(string(response.Body))
	}
	id := "owned-site"
	s.indexer = &plugins.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(`{"id":7,"name":"Owned"}`)}
	s.latest = []Data{fixtureRow("2026-10-16", 999)}
	s.refresh = json.RawMessage(`null`)
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &response)
	if !strings.Contains(string(response.Body), "未获取到数据") {
		t.Fatal("old daily row became current success", string(response.Body))
	}
	s.refresh = json.RawMessage(`{"userid":1267650600228229401496703205376,"domain":null,"updated_day":null,"err_msg":"owned ordinary error","message_unread_contents":[["title",null,"body",{"z":1,"a":2}]]}`)
	_ = json.Unmarshal(invoke(t, p, s, "http", request), &response)
	var envelope struct {
		Success bool
		Data    json.RawMessage
	}
	_ = json.Unmarshal(response.Body, &envelope)
	if !envelope.Success || !reflect.DeepEqual(semanticJSON(envelope.Data), semanticJSON(s.refresh)) {
		t.Fatal("current DTO changed", string(response.Body))
	}
	s.indexer.SiteID = nil
	before := s.refreshCalls
	raw, _ := json.Marshal(request)
	if _, e := p.Handle(context.Background(), "http", raw, s.host); e == nil {
		t.Fatal("catalog-only template authorized network")
	}
	if s.refreshCalls != before {
		t.Fatal("nil identity sent refresh")
	}
}

func TestSourceOneShotNoopStopRetainedMemberAndReinit(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	p, s := fixedPlugin(), newFixture()
	s.autoPrivate = true
	s.p = p
	var clockMu sync.Mutex
	clock := p.now()
	p.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	p.wait = func(ctx context.Context, _ time.Time) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	invoke(t, p, s, "init", map[string]any{"enabled": false, "onlyonce": true, "untouched": "owned"})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("private callback not admitted")
	}
	if !truth(p.currentConfig().OnlyOnce) || len(s.updates) != 1 || s.updates[0]["onlyonce"] != false || s.updates[0]["untouched"] != "owned" {
		t.Fatal("source onlyonce member/input/save changed", p.currentConfig(), s.updates)
	}
	invoke(t, p, s, "stop_service", nil)
	s.mu.Lock()
	before := s.allCalls
	s.mu.Unlock()
	if before != 0 {
		t.Fatal("date job ran early")
	}
	if _, e := p.Handle(context.Background(), "init", json.RawMessage(`null`), s.host); e == nil {
		t.Fatal("retained true onlyonce accepted None input")
	}
	for _, c := range []struct{ raw, kind string }{{"false", "bool"}, {"0", "int"}, {"0.0", "float"}, {`""`, "str"}, {"[]", "list"}} {
		_, e := p.Handle(context.Background(), "init", json.RawMessage(c.raw), s.host)
		if e == nil || e.Error() != "TypeError: '"+c.kind+"' object does not support item assignment" {
			t.Fatal("falsey input type failure changed", c.raw, e)
		}
	}
	invoke(t, p, s, "init", map[string]any{})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("empty dict did not schedule retained onlyonce")
	}
	clockMu.Lock()
	clock = clock.Add(3 * time.Second)
	clockMu.Unlock()
	release <- struct{}{}
	release <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		count := s.allCalls
		s.mu.Unlock()
		if count == 2 {
			return
		}
		select {
		case e := <-s.errors:
			t.Fatal(e)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("source stop/reinit cancelled one-shot jobs")
}
func TestSourceOnlyOnceTimezoneAndSaveFailureOrder(t *testing.T) {
	t.Setenv("TZ", "")
	s := newFixture()
	c := NewConfig()
	input := map[string]any{"onlyonce": true}
	scheduled := false
	e := initialize(context.Background(), input, &c, sdkHost{caller: s.host}, time.Now, func(time.Time) error { scheduled = true; return nil }, nil)
	if e == nil || input["onlyonce"] != false || !truth(c.OnlyOnce) || scheduled || len(s.updates) != 0 {
		t.Fatal("source zone failure ordering", e, input, c)
	}
}
