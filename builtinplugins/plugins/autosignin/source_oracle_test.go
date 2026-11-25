package autosignin

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceExpected(t *testing.T) map[string]any {
	t.Helper()
	raw, e := os.ReadFile("reference/source55.json")
	if e != nil {
		t.Fatal(e)
	}
	var root struct {
		Cases []struct {
			Case      string `json:"case"`
			Value     any    `json:"value"`
			Exception string `json:"exception"`
			Message   string `json:"message"`
		} `json:"cases"`
	}
	if Decode(raw, &root) != nil || len(root.Cases) != 55 {
		t.Fatal("actual333 source55 fixture")
	}
	out := map[string]any{}
	for _, c := range root.Cases {
		if c.Exception != "" {
			out[c.Case] = Object{"exception": c.Exception, "message": c.Message}
		} else {
			out[c.Case] = c.Value
		}
	}
	return out
}
func normalize(v any) any {
	raw, e := json.Marshal(v)
	if e != nil {
		return e.Error()
	}
	var x any
	_ = Decode(raw, &x)
	return x
}
func sourceCheck(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(normalize(got), normalize(want)) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		t.Fatalf("source mismatch\ngot: %s\nwant:%s", a, b)
	}
}
func sourceInventory() Inventory {
	return Inventory{Configured: []Object{{"id": 2, "name": "Same name"}, {"id": 1, "name": "Same name"}}, Indexers: []Object{{"id": 2, "name": "Same name", "url": "https://owned.invalid/", "public": false}, {"id": 1, "name": "Same name", "url": "https://owned.invalid/", "public": false}}, CustomEnabled: true, Custom: []Object{{"id": "custom-3", "name": "Owned custom", "url": "https://owned.invalid/"}}}
}
func sourceEngine() *Engine {
	return New(sourceInventory(), func() time.Time { return time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC) })
}
func TestIndependentSource55(t *testing.T) {
	want := sourceExpected(t)
	htmls := map[string]string{"logout_only": `<a href="logout.php">退出</a>`, "visible_done": `<a href="logout.php">out</a><div>签到成功</div>`, "hidden_template_success": `<a href="logout.php">out</a><template>签到成功</template>`, "script_success": `<a href="logout.php">out</a><script>签到成功</script>`, "hidden_attr": `<a href="logout.php">out</a><div hidden>签到成功</div>`, "style_display": `<a href="logout.php">out</a><div style="DISPLAY : none">签到成功</div>`, "password_upper": `<a href="logout.php">out</a><input type="PASSWORD"><span>签到成功</span>`, "password_lower": `<a href="logout.php">out</a><input type="password"><span>签到成功</span>`, "form_submit": `<a href="logout.php">out</a><span>签到成功</span><button>立即签到</button>`, "not_done": `<a href="logout.php">out</a><span>今日未签到</span>`, "failure_priority": `<span>签到成功</span><span>签到失败</span>`, "reward": `<span>今天签到您获得10点魔力值</span>`, "already": `<input type="button" value="已签到">`, "cumulative_only": `<span>累计签到100天</span>`, "hidden_tail_preserved": `<script>bad</script>签到成功`, "style_tail_removed": `<div style="display:none">bad</div>签到成功`, "empty": ""}
	for name, html := range htmls {
		t.Run("evidence/"+name, func(t *testing.T) {
			visible, e := VisibleHTML(html)
			if e != nil {
				t.Fatal(e)
			}
			login, e := HasLoginEvidence(html)
			if e != nil {
				t.Fatal(e)
			}
			form, e := HasLoginForm(html)
			if e != nil {
				t.Fatal(e)
			}
			signed, e := HasSigninEvidence(html)
			if e != nil {
				t.Fatal(e)
			}
			sourceCheck(t, Object{"visible": visible, "login": login, "login_form": form, "signin": signed}, want["evidence/"+name])
		})
	}
	for _, c := range []struct {
		name    string
		state   any
		message string
	}{{"bool_true", true, "签到成功"}, {"int_one", 1, "签到成功"}, {"str_true", "true", "签到成功"}, {"empty", true, ""}, {"login_only", true, "登录成功"}, {"false_success_words", false, "签到成功"}, {"false_failure", false, "签到失败"}} {
		t.Run("result/"+c.name, func(t *testing.T) {
			sourceCheck(t, SiteResult("Owned", c.state, c.message, "签到"), want["result/"+c.name])
		})
	}
	for _, text := range []string{"Cookie已失效", "签到失败", "重试", "仿真签到成功", "登录成功（未执行签到）", "签到成功", "已签到", "whatever", ""} {
		t.Run("status/"+text, func(t *testing.T) { sourceCheck(t, StatusMeta(text), want["status/"+text]) })
	}
	for _, c := range []struct {
		name  string
		times any
	}{{"dedup_sort", []any{Object{"hour": 20, "minute": 30}, Object{"hour": 9, "minute": 1}, Object{"hour": 20, "minute": 30}}}, {"falsey", []any{Object{"hour": false, "minute": false}}}, {"fraction", []any{Object{"hour": 9.9, "minute": 10.5}}}, {"invalid", []any{Object{"hour": 24, "minute": 0}, Object{"hour": "bad", "minute": 1}, Object{}}}, {"infinite", []any{Object{"hour": math.Inf(1), "minute": 1}}}} {
		t.Run("random_parse/"+c.name, func(t *testing.T) {
			got, e := ParseRandomSchedule("2026-11-5", c.times)
			if e != nil {
				sourceCheck(t, Object{"exception": "OverflowError", "message": e.Error()}, want["random_parse/"+c.name])
				return
			}
			values := []any{}
			for _, d := range got {
				values = append(values, d.Format("2006-01-02T15:04:05"))
			}
			sourceCheck(t, values, want["random_parse/"+c.name])
		})
	}
	for _, c := range []struct {
		name     string
		selected any
	}{{"all", []any{"all"}}, {"no_all", []any{2, 1}}} {
		t.Run("selection/"+c.name, func(t *testing.T) {
			got, e := sourceEngine().resolvedIDs(c.selected)
			if e != nil {
				t.Fatal(e)
			}
			sourceCheck(t, got, want["selection/"+c.name])
		})
	}
	t.Run("name/duplicate", func(t *testing.T) {
		info := map[string]siteInfoEntry{"number:1": {1, "Same"}, "number:2": {2, "Same"}, "str:1": {"1", "Same"}, "str:2": {"2", "Same"}}
		sourceCheck(t, displayName(2, info), want["name/duplicate"])
	})
	t.Run("name/string_int_alias", func(t *testing.T) {
		info := map[string]siteInfoEntry{"number:2": {2, "Only"}, "str:2": {"2", "Only"}}
		sourceCheck(t, displayName("2", info), want["name/string_int_alias"])
	})
	for _, c := range []struct {
		name         string
		selected, id any
	}{{"all", []any{"all"}, 2}, {"remove", []any{1, 2}, 2}, {"clear", []any{1, 2}, nil}, {"bad_numeric", []any{"custom-3"}, 2}} {
		t.Run("delete/"+c.name, func(t *testing.T) {
			v, e := sourceEngine().RemoveSite(c.selected, c.id)
			if e != nil {
				if !strings.Contains(e.Error(), "invalid literal") {
					t.Fatal(e)
				}
				sourceCheck(t, Object{"exception": "ValueError", "message": "invalid literal for int() with base 10: 'custom-3'"}, want["delete/"+c.name])
				return
			}
			sourceCheck(t, v, want["delete/"+c.name])
		})
	}
	for _, c := range []struct {
		name        string
		data        Object
		sign, login []any
	}{{"empty", Object{}, []any{}, []any{}}, {"legacy", Object{"11月5日": []any{Object{"site_id": 2, "site": "old", "status": "签到成功"}}}, []any{2}, nil}, {"done_without_result", Object{"签到-2026-11-05": Object{"do": []any{2}, "retry": []any{}}}, []any{2}, nil}, {"login_only_result", Object{"签到-2026-11-05": Object{"do": []any{2}, "retry": []any{}, "results": Object{"2": Object{"success": false, "logged_in": true, "message": "登录成功，签到未确认"}}}}, []any{2}, []any{2}}} {
		t.Run("page/"+c.name, func(t *testing.T) {
			e := sourceEngine()
			e.Data = c.data
			e.SignSites = c.sign
			e.LoginSites = c.login
			got, err := e.Page()
			if err != nil {
				t.Fatal(err)
			}
			sourceCheck(t, got, want["page/"+c.name])
		})
	}
	t.Run("form/default", func(t *testing.T) {
		f, e := Form(sourceInventory())
		if e != nil {
			t.Fatal(e)
		}
		sourceCheck(t, []any{f["conf"], f["model"]}, want["form/default"])
	})
	for _, c := range []struct {
		name, url string
		key       bool
		result    Result
	}{{"wrong_key", "https://owned.invalid/", false, Result{}}, {"unknown", "https://unknown.invalid/", true, Result{}}, {"login_not_signin", "https://owned.invalid/", true, Result{"Owned fixture", "登录成功，签到未确认", false, true}}, {"signin", "owned.invalid", true, Result{"Owned fixture", "签到成功", true, true}}} {
		t.Run("api/"+c.name, func(t *testing.T) {
			got, e := SigninByDomain(context.Background(), c.url, DomainAPIHooks{KeyAccepted: c.key, Lookup: func(_ context.Context, d string) (Object, error) {
				if d == "owned.invalid" {
					return Object{"id": 2, "name": "Owned fixture", "url": "https://owned.invalid/"}, nil
				}
				return nil, nil
			}, Visit: func(context.Context, Object) (Result, error) { return c.result, nil }})
			if e != nil {
				t.Fatal(e)
			}
			sourceCheck(t, got, want["api/"+c.name])
		})
	}
}
