package brushflow

import (
	"context"
	"encoding/json"
	"fmt"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"math"
	"regexp"
	"strings"
)

var numericTaskFields = strings.Fields("site_ratio_target disksize maxupspeed maxdlspeed maxdlcount timezone_offset seed_time hr_seed_time seed_ratio seed_size download_time seed_avgspeed seed_inactivetime up_speed dl_speed auto_archive_days promo_max_hours")
var textTaskFields = strings.Fields("cron active_time_range include exclude size seeder pubtime delete_size_range save_path delete_except_tags qb_category")

func value(r Row, k string, def any) any {
	v, ok := r[k]
	if !ok {
		return def
	}
	return v
}
func NormalizeTask(r Row, id func() string) Task {
	out := Row{}
	out["id"] = text(r["id"])
	if !truth(r["id"]) {
		out["id"] = id()
	}
	out["name"] = strings.TrimSpace(text(fallback(r["name"], "刷流任务")))
	for _, k := range taskFields {
		out[k] = nil
	}
	for _, k := range []string{"enabled", "notify", "except_subscribe"} {
		out[k] = truth(value(r, k, true))
	}
	for _, k := range []string{"site_ratio_control", "proxy_delete", "del_no_free", "site_hr_active", "site_skip_tips", "rss_support"} {
		out[k] = truth(value(r, k, false))
	}
	out["site_id"] = int(number(fallback(r["site_id"], 0)))
	out["downloader"] = strings.TrimSpace(text(fallback(r["downloader"], "")))
	for k, d := range map[string]float64{"brush_interval": 10, "check_interval": 5} {
		n := number(parseNumber(r[k]))
		if n == 0 {
			n = d
		}
		out[k] = int(math.Max(math.Trunc(n), 1))
	}
	for _, k := range numericTaskFields {
		out[k] = parseNumber(r[k])
	}
	out["timezone_offset"] = number(out["timezone_offset"])
	for _, k := range textTaskFields {
		out[k] = clean(r[k])
	}
	out["freeleech"] = value(r, "freeleech", "free")
	out["hr"] = value(r, "hr", "yes")
	if out["freeleech"] != "free" && out["freeleech"] != "2xfree" {
		out["del_no_free"] = false
	}
	return Task{out}
}
func NormalizeConfig(ctx context.Context, raw Row, h Host) (Config, error) {
	c := Config{Settings: Row{"enabled": truth(value(raw, "enabled", false)), "show_sidebar_nav": truth(value(raw, "show_sidebar_nav", true)), "global_proxy_delete": false, "global_delete_size_range": nil}}
	_, hasTasks := raw["tasks"].([]any)
	if _, ok := raw["tasks"].([]Row); ok {
		hasTasks = true
	}
	legacy := !hasTasks && truth(raw["brushsites"])
	for _, k := range globalLimitFields {
		v := raw[k]
		if v == nil && legacy {
			v = raw[strings.TrimPrefix(k, "global_")]
		}
		n := parseNumber(v)
		if !(number(n) > 0) {
			n = nil
		}
		c.Settings[k] = n
	}
	enabled := value(raw, "global_proxy_delete", false)
	dr := raw["global_delete_size_range"]
	if legacy {
		enabled = value(raw, "global_proxy_delete", value(raw, "proxy_delete", false))
		dr = value(raw, "global_delete_size_range", raw["delete_size_range"])
	}
	settings, e := ValidateSettings(Row{"global_proxy_delete": enabled, "global_delete_size_range": dr})
	if e != nil {
		h.Log(ctx, "warning", "全局动态删种配置无效，已自动关闭："+e.Error())
	} else {
		for _, k := range globalDeleteFields {
			c.Settings[k] = settings[k]
		}
	}
	taskRows := list(raw["tasks"])
	if legacy {
		taskRows, e = migrateLegacyConfig(ctx, raw, h)
		if e != nil {
			return c, e
		}
		c.LegacyOnce = truth(raw["onlyonce"]) && c.Enabled()
	}
	sites, e := h.Sites(ctx)
	if e != nil {
		return c, e
	}
	ds, e := h.Downloaders(ctx)
	if e != nil {
		return c, e
	}
	for _, v := range taskRows {
		r, ok := v.(map[string]any)
		if !ok {
			if x, ok := v.(Row); ok {
				r = x
			} else {
				continue
			}
		}
		t := NormalizeTask(Row(r), h.NewID)
		if !validReference(t, sites, ds) {
			t.Row["enabled"] = false
		}
		found := -1
		for i, old := range c.Tasks {
			if old.ID() == t.ID() {
				found = i
				break
			}
		}
		if found >= 0 {
			c.Tasks[found] = t
		} else {
			c.Tasks = append(c.Tasks, t)
		}
	}
	return c, nil
}
func validReference(t Task, sites []Site, ds []Downloader) bool {
	hasSite, hasDS := false, false
	for _, s := range sites {
		if s.ID == int(t.N("site_id")) && !s.Public {
			hasSite = true
		}
	}
	for _, d := range ds {
		if d.Name == t.S("downloader") && d.Name != "" {
			hasDS = true
		}
	}
	return hasSite && hasDS
}
func migrateLegacyConfig(ctx context.Context, raw Row, h Host) ([]any, error) {
	sites, e := h.Sites(ctx)
	if e != nil {
		return nil, e
	}
	overrides := map[string]Row{}
	if truth(raw["enable_site_config"]) && truth(raw["site_config"]) {
		content := regexp.MustCompile(`(?m)//.*?(?:\n|$)`).ReplaceAllString(text(raw["site_config"]), "")
		var v any
		if e = json.Unmarshal([]byte(strings.TrimSpace(content)), &v); e != nil {
			h.Log(ctx, "error", "解析旧版站点独立配置失败："+e.Error())
		} else {
			for _, x := range list(v) {
				r := row(x)
				if !truth(r["sitename"]) {
					continue
				}
				a := Row{}
				for _, k := range legacyOverrideFields {
					if v, ok := r[k]; ok {
						a[k] = v
					}
				}
				overrides[text(r["sitename"])] = a
			}
		}
	}
	out := []any{}
	for _, sid := range list(raw["brushsites"]) {
		for _, s := range sites {
			if s.ID != int(number(sid)) || s.Public {
				continue
			}
			r := Row{}
			for _, k := range taskFields {
				if v, ok := raw[k]; ok {
					r[k] = v
				}
			}
			o := overrides[s.Name]
			for k, v := range o {
				r[k] = v
			}
			if _, ok := o["timezone_offset"]; !ok {
				r["timezone_offset"] = number(parseNumber(raw["timezone_offset"])) / 60
			}
			r["id"] = h.NewID()
			r["name"] = s.Name
			r["site_id"] = s.ID
			r["enabled"] = true
			r["brush_interval"] = 10
			r["check_interval"] = 5
			out = append(out, NormalizeTask(r, h.NewID).Row)
		}
	}
	return out, nil
}

type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }
func invalid(k, m string) error          { return &ValidationError{k, m} }
func pydanticBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(x) {
		case "true", "1", "yes", "on", "y", "t":
			return true, true
		case "false", "0", "no", "off", "n", "f":
			return false, true
		}
	case float64, int, json.Number:
		n := number(x)
		if n == 0 || n == 1 {
			return n == 1, true
		}
	}
	return false, false
}

type sourceNumericRange struct{}

func (sourceNumericRange) MatchString(s string) bool {
	p, _ := pyre.Compile(`\A\d+(?:\.\d+)?(?:-\d+(?:\.\d+)?)?\Z`)
	ok, _ := p.Search(s)
	return ok
}

var numericRange sourceNumericRange

func ValidateTaskPayload(raw Row) (Row, error) {
	defaults := Row{"enabled": true, "notify": true, "brush_interval": 10, "check_interval": 5, "site_ratio_control": false, "freeleech": "free", "hr": "yes", "timezone_offset": 0, "except_subscribe": true, "proxy_delete": false, "del_no_free": false, "site_hr_active": false, "site_skip_tips": false, "rss_support": false}
	r := Row{}
	for _, k := range taskFields {
		r[k] = nil
	}
	for k, v := range defaults {
		r[k] = v
	}
	r["id"] = nil
	for _, k := range append(append([]string{}, taskFields...), "id", "name") {
		if v, ok := raw[k]; ok {
			r[k] = v
		}
	}
	for _, k := range []string{"name", "downloader"} {
		s, ok := r[k].(string)
		if !ok {
			return nil, invalid(k, "Input should be a valid string")
		}
		if len([]rune(s)) < 1 || len([]rune(s)) > 80 {
			return nil, invalid(k, "String length should be between 1 and 80")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, invalid(k, "字段不能为空")
		}
		r[k] = s
	}
	for _, k := range []string{"enabled", "notify", "site_ratio_control", "except_subscribe", "proxy_delete", "del_no_free", "site_hr_active", "site_skip_tips", "rss_support"} {
		v, ok := pydanticBool(r[k])
		if !ok {
			return nil, invalid(k, "Input should be a valid boolean")
		}
		r[k] = v
	}
	for _, k := range append([]string{"site_id", "brush_interval", "check_interval"}, numericTaskFields...) {
		v := r[k]
		if k != "timezone_offset" && k != "site_id" && k != "brush_interval" && k != "check_interval" && (v == nil || v == "" || parseNumber(v) != nil && number(v) == 0) {
			r[k] = nil
			continue
		}
		n := parseNumber(v)
		if n == nil || math.IsInf(number(n), 0) || math.IsNaN(number(n)) {
			return nil, invalid(k, "Input should be a valid number")
		}
		f := number(n)
		if k != "timezone_offset" && f <= 0 {
			return nil, invalid(k, "Input should be greater than 0")
		}
		if k == "site_id" || k == "brush_interval" || k == "check_interval" || k == "maxdlcount" {
			if f != math.Trunc(f) {
				return nil, invalid(k, "Input should be a valid integer")
			}
			if (k == "brush_interval" || k == "check_interval") && (f < 1 || f > 1440) {
				return nil, invalid(k, "Input should be between 1 and 1440")
			}
			r[k] = int(f)
		} else {
			r[k] = f
		}
	}
	for _, k := range textTaskFields {
		r[k] = clean(r[k])
	}
	for _, k := range []string{"size", "seeder", "pubtime", "delete_size_range"} {
		if truth(r[k]) && !numericRange.MatchString(text(r[k])) {
			return nil, invalid(k, "请输入数字或数字范围，例如 10 或 10-80")
		}
	}
	if truth(r["active_time_range"]) && !validTimeRange(text(r["active_time_range"])) {
		return nil, invalid("active_time_range", "开启时间段格式应为 HH:MM-HH:MM")
	}
	for _, k := range []string{"include", "exclude"} {
		if truth(r[k]) {
			if _, e := pyre.Compile(text(r[k])); e != nil {
				return nil, invalid(k, e.Error())
			}
		}
	}
	if r["freeleech"] != "" && r["freeleech"] != "free" && r["freeleech"] != "2xfree" {
		return nil, invalid("freeleech", "Input should be '', 'free' or '2xfree'")
	}
	if r["hr"] != "yes" && r["hr"] != "no" {
		return nil, invalid("hr", "Input should be 'yes' or 'no'")
	}
	if truth(r["site_ratio_control"]) && r["site_ratio_target"] == nil {
		return nil, invalid("site_ratio_target", "启用站点分享率控制时必须设置目标分享率")
	}
	return r, nil
}
func ValidateSettings(raw Row) (Row, error) {
	r := Row{"enabled": true, "show_sidebar_nav": true, "global_proxy_delete": false, "global_delete_size_range": nil}
	for _, k := range globalLimitFields {
		r[k] = nil
	}
	for k := range r {
		if v, ok := raw[k]; ok {
			r[k] = v
		}
	}
	// This is the source before-validator exact disabled set, before Pydantic bool.
	switch v := r["global_proxy_delete"].(type) {
	case nil:
		r["global_delete_size_range"] = nil
	case bool:
		if !v {
			r["global_delete_size_range"] = nil
		}
	case int, float64:
		if number(v) == 0 {
			r["global_delete_size_range"] = nil
		}
	case string:
		if v == "" || v == "0" || v == "false" || v == "False" {
			r["global_delete_size_range"] = nil
		}
	}
	for _, k := range []string{"enabled", "show_sidebar_nav", "global_proxy_delete"} {
		v, ok := pydanticBool(r[k])
		if !ok {
			return nil, invalid(k, "Input should be a valid boolean")
		}
		r[k] = v
	}
	for _, k := range globalLimitFields {
		v := r[k]
		if v == nil || v == "" || parseNumber(v) != nil && number(v) == 0 {
			r[k] = nil
			continue
		}
		n := parseNumber(v)
		if n == nil || number(n) <= 0 || math.IsNaN(number(n)) || math.IsInf(number(n), 0) {
			return nil, invalid(k, "Input should be greater than 0")
		}
		if k == "global_maxdlcount" {
			if number(n) != math.Trunc(number(n)) {
				return nil, invalid(k, "Input should be a valid integer")
			}
			r[k] = int(number(n))
		} else {
			r[k] = n
		}
	}
	r["global_delete_size_range"] = clean(r["global_delete_size_range"])
	if truth(r["global_delete_size_range"]) {
		s := text(r["global_delete_size_range"])
		if !numericRange.MatchString(s) {
			return nil, invalid("global_delete_size_range", "请输入数字或数字范围，例如 100 或 50-100")
		}
		v := ranges(s, 1)
		if v[0] <= 0 || len(v) > 1 && (v[1] <= 0 || v[0] >= v[1]) {
			return nil, invalid("global_delete_size_range", "动态删种区间下限必须小于上限")
		}
	}
	if truth(r["global_proxy_delete"]) && !truth(r["global_delete_size_range"]) {
		return nil, invalid("global_delete_size_range", "启用全局动态删种时必须设置动态删种阈值")
	}
	return r, nil
}
func validTimeRange(s string) bool {
	p, _ := pyre.Compile(`\A\d{2}:\d{2}-\d{2}:\d{2}\Z`)
	ok, _ := p.Search(s)
	if !ok {
		return false
	}
	// CPython _strptime's %H/%M patterns have ASCII leading digits,
	// but their trailing \d uses the fixed Unicode decimal table.
	for _, v := range strings.Split(s, "-") {
		rs := []rune(v)
		if len(rs) != 5 {
			return false
		}
		_, hd := pyre.DecimalValue(rs[1])
		_, md := pyre.DecimalValue(rs[4])
		if !hd || !md || !(rs[0] == '0' || rs[0] == '1' || rs[0] == '2' && rs[1] >= '0' && rs[1] <= '3') || rs[3] < '0' || rs[3] > '5' {
			return false
		}
	}
	return true
}

func ranges(s string, factor float64) []float64 {
	r := []float64{}
	for _, p := range strings.Split(s, "-") {
		r = append(r, number(p)*factor)
	}
	return r
}
func configError(s string) error { return fmt.Errorf("%s", s) }

// LoadApplied reads a normalized private owner snapshot without initializing,
// rewriting KV, checking connectivity or replaying legacy single-run flags.
func LoadApplied(raw Row) (Config, error) {
	c := Config{Settings: Row{}}
	for _, k := range append(append([]string{"enabled", "show_sidebar_nav"}, globalLimitFields...), globalDeleteFields...) {
		c.Settings[k] = raw[k]
	}
	for _, v := range list(raw["tasks"]) {
		r := row(v)
		if text(r["id"]) == "" {
			return c, configError("applied task has no identity")
		}
		c.Tasks = append(c.Tasks, Task{clone(r)})
	}
	return c, nil
}

func ValidateTaskState(raw Row) (bool, error) {
	v, ok := raw["enabled"]
	if !ok {
		return false, invalid("enabled", "Field required")
	}
	b, ok := pydanticBool(v)
	if !ok {
		return false, invalid("enabled", "Input should be a valid boolean")
	}
	return b, nil
}
