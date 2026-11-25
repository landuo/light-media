package autosignin

import (
	"context"
	"encoding/hex"
	"errors"

	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type fixedPages struct {
	site      Object
	responses []any
	calls     []any
	steps     *[]any
}

func (f *fixedPages) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	proxy := any(nil)
	if Truth(f.site["proxy"]) {
		proxy = Object{"https": "synthetic-proxy-reference"}
	}
	call := Object{"method": r.Method, "proxies": proxy, "timeout": r.Timeout, "url": r.URL}
	if r.HeadersMode == HeaderBaseCookie {
		call["headers"] = Object{"User-Agent": f.site["ua"], "Cookie": f.site["cookie"]}
	} else {
		if r.Headers != nil {
			call["headers"] = r.Headers
		} else {
			call["ua"] = f.site["ua"]
		}
		if !r.NoCookies {
			cookie := f.site["cookie"]
			if r.CookieOverride != nil {
				cookie = *r.CookieOverride
			}
			call["cookies"] = cookie
		}
	}
	if r.AcceptType != "" {
		call["accept_type"] = r.AcceptType
	}
	if r.Referer != "" {
		call["referer"] = r.Referer
	}

	if r.Method == "POST" {
		if r.JSON != nil {
			call["json"] = r.JSON
		} else {
			call["data"] = r.Form
		}
	}
	f.calls = append(f.calls, call)
	if f.steps != nil {
		*f.steps = append(*f.steps, Object{"op": "request", "index": len(f.calls) - 1})
	}
	if len(f.responses) == 0 {
		return nil, nil
	}
	raw := f.responses[0]
	f.responses = f.responses[1:]
	if raw == nil {
		return nil, nil
	}
	row := raw.(Object)
	if msg, ok := row["error"].(string); ok {
		return nil, errors.New(msg)
	}
	status := 200
	if n := row["status"]; n != nil {
		integer, _ := sourceInteger(n)
		status = int(integer.Int64())
	}
	text := textValue(row["text"])
	cookies := []CookieValue{}
	for _, row := range asList(row["cookies"]) {
		pair := asList(row)
		cookies = append(cookies, CookieValue{textValue(pair[0]), textValue(pair[1])})
	}
	content := []byte(text)
	if raw, ok := row["raw_hex"].(string); ok {
		content, _ = hex.DecodeString(raw)
	}
	return &PageResponse{Status: status, Text: text, Content: content, Cookies: cookies}, nil
}
func (f *fixedPages) Page(ctx context.Context, r PageRequest) (string, error) {
	proxy := any(nil)
	if Truth(f.site["proxy"]) {
		proxy = "synthetic-proxy-reference"
	}
	f.calls = append(f.calls, Object{"method": "BROWSER", "url": r.URL, "cookies": f.site["cookie"], "ua": f.site["ua"], "proxies": proxy, "timeout": r.Timeout})
	if f.steps != nil {
		*f.steps = append(*f.steps, Object{"op": "browser", "index": len(f.calls) - 1})
	}
	if len(f.responses) == 0 {
		return "", nil
	}
	raw := f.responses[0]
	f.responses = f.responses[1:]
	if raw == nil {
		return "", nil
	}
	row := raw.(Object)
	if msg, ok := row["error"].(string); ok {
		return "", errors.New(msg)
	}
	return textValue(row["text"]), nil
}
func fixedEngine(site Object) *Engine {
	e := New(Inventory{Configured: []Object{{"id": 1, "name": "Synthetic"}}, Indexers: []Object{site}}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	if err := e.Init(Object{"enabled": true, "sign_sites": []any{1}, "login_sites": []any{}, "queue_cnt": 1, "notify": false, "retry_keyword": "Cookie已失效", "clean": false}); err != nil {
		panic(err)
	}
	e.Trace = []Object{}
	return e
}

func checkInstalledAdapterReference(t *testing.T, path string, count int) {
	raw, err := readAutoSignInFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Name, Kind                      string
			Adapter                         FixedAdapter
			Input, Expected, Error, Partial Object
			Entropy, Steps                  []any
		}
	}
	if err = Decode(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.Cases) != count {
		t.Fatal("reference count changed")
	}
	for _, c := range ref.Cases {
		t.Run(c.Name, func(t *testing.T) {
			policy := fixedPolicies[c.Adapter]
			if c.Adapter == "MTorrent" {
				policy.SiteURL = "m-team"
			}
			site := Object{"id": 1, "name": "Synthetic", "url": "https://" + policy.SiteURL + "/", "cookie": "synthetic-cookie=not-a-credential", "ua": "synthetic-agent"}
			for k, v := range asObject(c.Input["site"]) {
				site[k] = v
			}
			pages := &fixedPages{site: site, calls: []any{}, responses: asList(clone(c.Input["responses"]))}
			stats := []any{}
			steps := []any{}
			if c.Steps != nil {
				pages.steps = &steps
			}
			entropy := []any{}
			h := SiteVisitHooks{HTTP: pages, Browser: pages, Now: func() time.Time { return oracleLocalTime(c.Input) }, Stats: func(_ context.Context, domain string, ok bool, seconds int) error {
				row := Object{"domain": domain, "success": ok}
				if ok {
					row["seconds"] = seconds
				}
				stats = append(stats, row)
				return nil
			}}
			h.Choose = oracleChoose(c.Input, &entropy)
			oracleOCRHooks(&h, site, c.Input, &steps)
			if c.Adapter == AdapterTjupt {
				h.Tjupt = &TjuptDependencies{Cache: oracleAnswerCache{c.Input, &steps}, Public: oraclePublicPages{pages}}
			}
			adapter, _ := FixedSiteAdapter(c.Adapter)
			if c.Adapter == "MTorrent" {
				adapter = MTorrentAdapter()
				h.MTeam = oracleMTeamTransport{pages}
			}
			visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: h}
			e := fixedEngine(site)
			e.Now = h.Now
			if history, ok := c.Input["history"].(Object); ok {
				e.Data = clone(history).(Object)
			}
			var result any
			var runErr error
			switch c.Kind {
			case "adapter":
				r, err := adapter.Signin(context.Background(), site, h)
				runErr = err
				result = []any{r.State, r.Message}
			case "login-adapter":
				r, err := adapter.Login(context.Background(), site, h)
				runErr = err
				result = []any{r.State, r.Message}
			case "wrapper":
				r, err := visitor.Visit(context.Background(), site, "签到", true)
				runErr = err
				result = normalizedResult(r)
			case "do":
				keys := []string{}
				for k := range e.Data {
					keys = append(keys, k)
				}
				runErr = e.Do(context.Background(), e.Now(), "签到", []any{1}, nil, keys, RunHooks{Visit: visitor.Visit, Emit: func(context.Context, string, Object) error { return nil }, Notify: func(context.Context, Object) error { return nil }})
			}
			if c.Steps != nil && !reflect.DeepEqual(canonical(steps), canonical(c.Steps)) {
				t.Errorf("steps got %v want %v", steps, c.Steps)
			}
			if c.Entropy != nil && !reflect.DeepEqual(canonical(entropy), canonical(c.Entropy)) {
				t.Errorf("RNG calls got %v want %v", entropy, c.Entropy)
			}
			got := Object{"result": result, "calls": pages.calls, "stats": stats, "data": e.Data, "config": e.Config, "trace": e.Trace}
			if c.Error != nil {
				if runErr == nil || runErr.Error() != c.Error["message"] {
					t.Fatalf("error got %v want %v", runErr, c.Error)
				}
				delete(got, "result")
				if !reflect.DeepEqual(canonical(effectiveConstructorCalls(got)), canonical(effectiveConstructorCalls(c.Partial))) {
					t.Errorf("partial got %#v\nwant %#v", canonical(got), canonical(c.Partial))
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if !reflect.DeepEqual(canonical(effectiveConstructorCalls(got)), canonical(effectiveConstructorCalls(c.Expected))) {
				t.Errorf("got %#v\nwant %#v", canonical(got), canonical(c.Expected))
			}
		})
	}
}

// Raw original calls stay in the fixture. RequestUtils.__init__ declares
// timeout=None, so omission and explicit None have the same bound parameter.
// Compare that effective parameter rather than inventing a call difference.
func effectiveConstructorCalls(value Object) Object {
	out := clone(value).(Object)
	for _, raw := range asList(out["calls"]) {
		call := raw.(Object)
		if call["method"] != "BROWSER" {

			if Truth(call["headers"]) {
				call["ua"] = nil
			}

			if call["content_type"] == "application/x-www-form-urlencoded; charset=UTF-8" {
				delete(call, "content_type")
			}
			if _, exists := call["proxies"]; !exists {
				call["proxies"] = nil
			}
			if _, exists := call["timeout"]; !exists {
				call["timeout"] = nil
			}
		}
	}
	return out
}

func oracleLocalTime(input Object) time.Time {
	if raw, ok := input["now"].(string); ok {
		v, err := time.Parse("2006-01-02T15:04:05", raw)
		if err != nil {
			panic(err)
		}
		return v
	}
	return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
}
func oracleChoose(input Object, trace *[]any) func(int, int) (int, error) {
	return func(minimum, maximum int) (int, error) {
		row := Object{"minimum": minimum, "maximum": maximum}
		*trace = append(*trace, row)
		if maximum < minimum {
			return defaultChoose(minimum, maximum)
		}
		value := 0
		if raw := input["choice"]; raw != nil {
			n, err := sourceInteger(raw)
			if err != nil {
				return 0, err
			}
			value = int(n.Int64())
		}
		row["value"] = value
		return value, nil
	}
}

func oracleOCRHooks(h *SiteVisitHooks, site, input Object, steps *[]any) {
	results := asList(clone(input["ocr"]))
	h.OCR = func(_ context.Context, address string) (any, error) {
		*steps = append(*steps, Object{"op": "ocr", "image_url": address, "cookie": site["cookie"], "ua": site["ua"]})
		if len(results) == 0 {
			return "", nil
		}
		result := results[0]
		results = results[1:]
		if row, ok := result.(Object); ok && row["error"] != nil {
			return nil, errors.New(textValue(row["error"]))
		}
		return result, nil
	}
	h.Sleep = func(_ context.Context, d time.Duration) error {
		*steps = append(*steps, Object{"op": "sleep", "seconds": int(d / time.Second)})
		return nil
	}
}

type oracleAnswerCache struct {
	input Object
	steps *[]any
}

func (c oracleAnswerCache) Ensure(context.Context) error {
	*c.steps = append(*c.steps, Object{"op": "cache_ensure"})
	if Truth(c.input["ensure_error"]) {
		return errors.New(textValue(c.input["ensure_error"]))
	}
	if exists, ok := c.input["directory_exists"].(bool); ok && !exists {
		*c.steps = append(*c.steps, Object{"op": "cache_makedirs"})
	}
	return nil
}
func (c oracleAnswerCache) Read(context.Context) ([]byte, error) {
	*c.steps = append(*c.steps, Object{"op": "cache_read"})
	if Truth(c.input["read_error"]) {
		return nil, ErrTjuptCacheIO
	}
	if value, ok := c.input["cache"].(string); ok {
		return []byte(value), nil
	}
	return nil, ErrTjuptCacheIO
}
func (c oracleAnswerCache) Write(_ context.Context, data []byte) error {
	if Truth(c.input["write_error"]) {
		*c.steps = append(*c.steps, Object{"op": "cache_write_error"})
		return ErrTjuptCacheIO
	}
	*c.steps = append(*c.steps, Object{"op": "cache_write", "data": string(data)})
	return nil
}

type oraclePublicPages struct{ pages *fixedPages }

func (p oraclePublicPages) send(ctx context.Context, request PageRequest) (*PageResponse, error) {
	before := len(p.pages.calls)
	response, err := p.pages.Do(ctx, request)
	call := p.pages.calls[before].(Object)
	delete(call, "ua")
	delete(call, "cookies")
	return response, err
}
func (p oraclePublicPages) Suggest(ctx context.Context, label string) (*PageResponse, error) {
	return p.send(ctx, PageRequest{Method: "GET", URL: "https://movie.douban.com/j/subject_suggest?q=" + label, NoCookies: true})
}
func (p oraclePublicPages) Image(ctx context.Context, address string) (*PageResponse, error) {
	return p.send(ctx, PageRequest{Method: "GET", URL: address, NoCookies: true, Binary: true, Referer: "https://movie.douban.com"})
}

type oracleMTeamTransport struct{ pages *fixedPages }

func (o oracleMTeamTransport) Post(ctx context.Context, plan RequestPlan) (*AdapterResponse, error) {
	headers := Object{}
	for k, v := range plan.Headers {
		headers[k] = v
	}
	response, err := o.pages.Do(ctx, PageRequest{Method: "POST", URL: plan.URL, Headers: headers, NoCookies: true, Form: plan.Data, Timeout: plan.Timeout})
	if err != nil || response == nil {
		return nil, err
	}
	out := &AdapterResponse{Status: response.Status}
	out.Payload, out.JSONError = parseSiteJSON(response.Text)
	return out, nil
}

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
func TestSourceGenericPTZoneVisitOracles(t *testing.T) {
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
func canonical(value any) any {
	raw, _ := json.Marshal(value)
	var out any
	Decode(raw, &out)
	return out
}
