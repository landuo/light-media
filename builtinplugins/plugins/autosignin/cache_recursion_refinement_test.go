package autosignin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cacheRecursionRoot = "testdata/source-oracles/autosignin-cache-recursion-refinement-20261106/"

type recursionCache struct {
	path   string
	writes int
	events *[]string
}

func (c *recursionCache) Ensure(context.Context) error {
	return os.MkdirAll(filepath.Dir(c.path), 0700)
}
func (c *recursionCache) Read(context.Context) ([]byte, error) { return os.ReadFile(c.path) }
func (c *recursionCache) Write(_ context.Context, b []byte) error {
	c.writes++
	*c.events = append(*c.events, "WRITE")
	return os.WriteFile(c.path, b, 0600)
}

type recursionHTTP struct {
	events *[]string
	login  bool
}

func (h recursionHTTP) Do(_ context.Context, r PageRequest) (*PageResponse, error) {
	if r.Method == "POST" {
		*h.events = append(*h.events, "POST")
		return &PageResponse{Status: 200, Text: "这是您的首次签到，本次签到获得1个魔力值。"}, nil
	}
	*h.events = append(*h.events, "GET:"+r.URL)
	if strings.Contains(r.URL, "attendance.php") {
		return &PageResponse{Status: 200, Text: `<html><body><table class="captcha"><tr><td><img src="/owned-cap.png"></td></tr></table><input name="ban_robot" value="owned-answer">Owned Answer</body></html>`}, nil
	}
	if strings.HasSuffix(r.URL, "owned-cap.png") {
		return &PageResponse{Status: 200, Content: []byte("owned-image")}, nil
	}
	text := `<html><body><input type="password"></body></html>`
	if h.login {
		text = `<html><body><a href="logout.php">Logout</a></body></html>`
	}
	return &PageResponse{Status: 200, Text: text}, nil
}

type recursionPublic struct{ events *[]string }

func (p recursionPublic) Suggest(_ context.Context, answer string) (*PageResponse, error) {
	*p.events = append(*p.events, "GET:https://movie.douban.com/j/subject_suggest?q="+answer)
	return &PageResponse{Status: 200, Text: `[{"img":"https://owned.invalid/answer.png"}]`}, nil
}
func (p recursionPublic) Image(_ context.Context, address string) (*PageResponse, error) {
	*p.events = append(*p.events, "GET:"+address)
	return &PageResponse{Status: 200, Content: []byte("owned-image")}, nil
}
func recursionInput(shape, leaf string, depth int) []byte {
	token := "0"
	switch leaf {
	case "float":
		token = "0.0"
	case "nan":
		token = "NaN"
	case "string":
		token = `"x"`
	case "null":
		token = "null"
	case "empty":
		token = "[]"
	}
	for n := 0; n < depth; n++ {
		if shape == "object" || shape == "mixed" && n%2 == 1 {
			token = `{"x":` + token + `}`
		} else {
			token = "[" + token + "]"
		}
	}
	return []byte(`{"old":` + token + `}`)
}
func TestCacheRecursionOriginalFullEntryStages(t *testing.T) {
	for _, file := range []string{"source-owner-r3.json", "source-shapes-r1.json", "source-leaves-r1.json", "source-leaf-boundary-r1.json", "source-fallback-r1.json"} {
		raw, e := os.ReadFile(cacheRecursionRoot + file)
		if e != nil {
			t.Fatal(e)
		}
		var f struct {
			Rows []struct {
				Mode, Shape, Leaf string
				Login             bool   `json:"fallback_login"`
				FileSHA           string `json:"file_sha256"`
				Depth             int
				Error             *struct{ Kind, Message string }
				Result            json.RawMessage
				HTTP              *struct {
					Status int
					Body   string
				}
				Events, Effects []string
				Unchanged       bool `json:"file_unchanged"`
				NewHash         bool `json:"new_hash_in_memory"`
			}
		}
		if e = json.Unmarshal(raw, &f); e != nil {
			t.Fatal(e)
		}
		for i, c := range f.Rows {
			t.Run(fmt.Sprintf("%s/%d-%s-%s-%s-%d", file, i, c.Mode, c.Shape, c.Leaf, c.Depth), func(t *testing.T) {
				ctx := context.Background()
				input := recursionInput(c.Shape, c.Leaf, c.Depth)
				path := filepath.Join(t.TempDir(), "signin", "tjupt.json")
				if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, input, 0600); e != nil {
					t.Fatal(e)
				}
				events := []string{}
				cache := &recursionCache{path: path, events: &events}
				stats := []bool{}
				clock := func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
				hooks := SiteVisitHooks{HTTP: recursionHTTP{events: &events, login: c.Login}, Now: clock, Log: func(level, msg string) error { events = append(events, level+":"+msg); return nil }, Stats: func(_ context.Context, _ string, success bool, _ int) error {
					stats = append(stats, success)
					return nil
				}, Tjupt: &TjuptDependencies{Cache: cache, Public: recursionPublic{events: &events}, Hash: func([]byte) (string, error) { return strings.Repeat("0", 100), nil }, Compare: func(string, string) float64 { return 1 }}}
				site := Object{"id": json.Number("1"), "name": "Owned", "url": "https://www.tjupt.org/", "cookie": "owned", "ua": "Owned/1", "public": false, "proxy": false, "render": false}
				adapter, e := FixedSiteAdapter(AdapterTjupt)
				if e != nil {
					t.Fatal(e)
				}
				visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: hooks}
				var got any
				var err error
				effects := []string{}
				var passedCache Object
				switch c.Mode {
				case "submit":
					value, e := parseSourceHTTPJSON(string(input))
					if e != nil {
						t.Fatal(e)
					}
					m := value.(sourceHTTPMap)
					passedCache = m.values
					got, err = tjuptSubmit(ctx, site, hooks, "owned-answer", m.values, m.keys, "new-hash")
				case "adapter":
					got, err = adapter.Signin(ctx, site, hooks)
				case "visit":
					got, err = visitor.Visit(ctx, site, "签到", true)
				case "api":
					got, err = SigninByDomain(ctx, site["url"].(string), DomainAPIHooks{KeyAccepted: true, Lookup: func(context.Context, string) (Object, error) { return site, nil }, Visit: func(call context.Context, s Object) (Result, error) { return visitor.Visit(call, s, "签到", true) }})
				case "threadpool":
					engine := New(Inventory{Indexers: []Object{site}}, clock)
					engine.SchemaLoaded = true
					engine.QueueCount = 5
					engine.RetryKeyword = "never-match"
					engine.Effects = func(effect Effect) error {
						if effect.Kind == "history.put" {
							effects = append(effects, "history.put:"+effect.Key)
						}
						if effect.Kind == "config" {
							effects = append(effects, "config.update")
						}
						return nil
					}
					err = engine.Do(ctx, clock(), "签到", []any{"all"}, nil, nil, RunHooks{Visit: visitor.Visit, Emit: func(context.Context, string, Object) error {
						effects = append(effects, "event:site_refresh")
						return nil
					}})
					got = engine.Data["11月6日"]
				default:
					t.Fatal(c.Mode)
				}
				stored, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if actual := fmt.Sprintf("%x", sha256.Sum256(stored)); c.FileSHA != "" && actual != c.FileSHA {
					t.Errorf("source exact cache file SHA=%s target=%s", c.FileSHA, actual)
				}
				posts := 0
				info := -1
				post := -1
				write := -1
				for j, x := range events {
					if x == "POST" {
						posts++
						post = j
					}
					if x == "INFO:签到成功" {
						info = j
					}
					if x == "WRITE" {
						write = j
					}
				}
				if posts != 1 || info <= post || cache.writes != countRecursionEvent(c.Events, "WRITE") || bytes.Equal(stored, input) != c.Unchanged || write >= 0 && write <= info {
					t.Errorf("source dump stages differ; POST=%d INFO=%d WRITE=%d/%d unchanged=%t sourceUnchanged=%t err=%v", posts, info, write, cache.writes, bytes.Equal(stored, input), c.Unchanged, err)
				}
				if passedCache != nil && passedCache["new-hash"] != "owned-answer" {
					t.Error("source assignment before dump lost")
				}
				if c.Error != nil {
					var source *ValueError
					if !errors.As(err, &source) || source.Kind != c.Error.Kind || source.Message != c.Error.Message {
						t.Errorf("source exception=%+v target=%v/%v", c.Error, source, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if c.Mode == "visit" {
					want := map[string]any{}
					if e := json.Unmarshal(c.Result, &want); e != nil {
						t.Fatal(e)
					}
					out := got.(Result)
					if out.Message != want["message"] || out.Success != want["success"] || out.LoggedIn != want["logged_in"] {
						t.Errorf("source captured result=%s target=%+v", c.Result, out)
					}
				}
				if c.Mode == "api" {
					var want Object
					if e := Decode([]byte(c.HTTP.Body), &want); e != nil {
						t.Fatal(e)
					}
					out := got.(Object)
					delete(want, "message_i18n")
					a, _ := json.Marshal(out)
					b, _ := json.Marshal(want)
					if !bytes.Equal(a, b) {
						t.Errorf("source HTTP result=%s targetpure=%s", b, a)
					}
				}
				if c.Mode == "threadpool" {
					a, _ := json.Marshal(effects)
					b, _ := json.Marshal(c.Effects)
					if !bytes.Equal(a, b) {
						t.Errorf("source effects=%s target=%s", b, a)
					}
					var want struct{ Daily []any }
					if e := Decode(c.Result, &want); e != nil {
						t.Fatal(e)
					}
					dailyJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want.Daily)
					if !bytes.Equal(dailyJSON, wantJSON) {
						t.Errorf("source owner daily=%s target=%s", wantJSON, dailyJSON)
					}
					if c.Unchanged && !c.Login && !strings.Contains(fmt.Sprint(got), "maximum recursion depth exceeded") {
						t.Errorf("owner lost failed result: %v", got)
					}
				}
				if c.Mode == "visit" || c.Mode == "api" || c.Mode == "threadpool" {
					if len(stats) != 1 || stats[0] != (countRecursionEvent(c.Events, "stats.success") == 1) {
						t.Errorf("source visit stats wrong: %v;dumpFailure=%t", stats, c.Unchanged)
					}
				}
			})
		}
	}
}
func countRecursionEvent(rows []string, target string) int {
	n := 0
	for _, x := range rows {
		if x == target {
			n++
		}
	}
	return n
}
