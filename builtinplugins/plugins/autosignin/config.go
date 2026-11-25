package autosignin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	legacyanime "light-media/builtinplugins/plugins/autosignin/pyvalue"
)

type Object = map[string]any

// Inventory is host-provided public metadata. It never contains credentials.
type Inventory struct {
	Configured, Indexers, Custom []Object
	CustomEnabled                any
}
type Result struct {
	SiteName any    `json:"site_name"`
	Message  string `json:"message"`
	Success  bool   `json:"success"`
	LoggedIn bool   `json:"logged_in"`
}

func (r Result) Completed() bool { return r.Success || r.LoggedIn }

type scheduler struct{ Running bool }
type Engine struct {
	shared            *sourceState
	ConfiguredLive    func() []Object
	IndexersLive      func() []Object
	CustomLive        func() []Object
	LoadInitInventory func() (Inventory, error)
	HistoryLive       func(string) (any, error)
	StopLive          func() error
	LogLive           func(string, string) error
	Effects           func(Effect) error

	Enabled, Notify, Cron, OnlyOnce, QueueCount, SignSites, LoginSites, RetryKeyword, AutoCF, Clean any
	StartTime, EndTime                                                                              any
	SchemaLoaded                                                                                    bool
	Config                                                                                          Object
	Data                                                                                            Object
	Trace                                                                                           []Object
	Inventory                                                                                       Inventory
	Now                                                                                             func() time.Time
	ScheduleTimezone                                                                                string
	ScheduleZone                                                                                    *time.Location
	Scheduler                                                                                       *scheduler
}

// New uses the synthetic UTC environment. A real host must pass its trusted
// source scheduler timezone via NewAtZone; it is not a plugin config field.
func New(inventory Inventory, now func() time.Time) *Engine {
	engine, _ := NewAtZone(inventory, now, "UTC")
	return engine
}
func NewAtZone(inventory Inventory, now func() time.Time, timezone string) (*Engine, error) {
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{Enabled: false, Notify: false, Cron: "", OnlyOnce: false, QueueCount: json.Number("5"), SignSites: []any{}, LoginSites: []any{}, AutoCF: json.Number("0"), Clean: false, Config: Object{}, Data: Object{}, Trace: []Object{}, Inventory: inventory, Now: now, ScheduleTimezone: timezone, ScheduleZone: zone}, nil
}

func Decode(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(dst)
}
func Truth(v any) bool {
	switch x := v.(type) {
	case *sourceOCRValue:
		return Truth(x.value)
	case sourceHTTPMap:
		return len(x.values) > 0
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			n, ok := new(big.Int).SetString(string(x), 10)
			return ok && n.Sign() != 0
		}
		f, _ := strconv.ParseFloat(string(x), 64)
		return f != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
func clone(v any) any {
	if sourceHasPrivateString(v) {
		return sourcePrivateClone(v)
	}
	raw, _ := json.Marshal(v)
	var copy any
	Decode(raw, &copy)
	return copy
}
func typeName(v any) string {
	switch n := v.(type) {
	case sourceHTTPMap:
		return "dict"
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case string:
		return "str"
	case []any:
		return "list"
	case Object:
		return "dict"
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			return "float"
		}
		return "int"
	case float64:
		return "float"
	case int:
		return "int"
	}
	return "object"
}
func numeric(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			f, _ := strconv.ParseFloat(string(n), 64)
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, false
			}
			r := new(big.Rat)
			r.SetFloat64(f)
			return r, true
		}
		r, ok := new(big.Rat).SetString(string(n))
		return r, ok
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(n) == nil {
			return nil, false
		}
		return r, true
	case bool:
		if n {
			return new(big.Rat).SetInt64(1), true
		}
		return new(big.Rat), true
	}
	return nil, false
}
func equal(a, b any) bool {
	if x, ok := numeric(a); ok {
		if y, ok := numeric(b); ok {
			return x.Cmp(y) == 0
		}
	}
	return reflect.DeepEqual(a, b)
}
func contains(container, needle any) (bool, error) {
	switch c := container.(type) {
	case sourceHTTPMap:
		return contains(c.values, needle)
	case string:
		s, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("'in <string>' requires string as left operand, not %s", typeName(needle))
		}
		return strings.Contains(c, s), nil
	case []any:
		for _, v := range c {
			if equal(v, needle) {
				return true, nil
			}
		}
		return false, nil
	case Object:
		switch needle.(type) {
		case []any, Object:
			return false, fmt.Errorf("unhashable type: '%s'", typeName(needle))
		}
		s, ok := needle.(string)
		if !ok {
			return false, nil
		}
		_, ok = c[s]
		return ok, nil
	default:
		return false, fmt.Errorf("argument of type '%s' is not iterable", typeName(container))
	}
}
func (e *Engine) trace(value Object) { e.Trace = append(e.Trace, clone(value).(Object)) }
func (e *Engine) Stop() error {
	if e.StopLive != nil {
		return e.StopLive()
	}
	if e.Scheduler != nil {
		if err := e.effect("scheduler_remove", "", nil); err != nil {
			if sourceFailure(err) {
				e.log("ERROR", "退出插件失败："+err.Error())
				return nil
			}
			return err
		}
		e.trace(Object{"op": "scheduler_remove"})
		if e.Scheduler.Running {
			if err := e.effect("scheduler_stop", "", nil); err != nil {
				if sourceFailure(err) {
					e.log("ERROR", "退出插件失败："+err.Error())
					return nil
				}
				return err
			}
			e.trace(Object{"op": "scheduler_stop"})
		}
		e.Scheduler = nil
	}
	return nil
}
func (e *Engine) replaceConfig() error {
	desired := clone(Object{"enabled": e.field("Enabled"), "notify": e.field("Notify"), "cron": e.field("Cron"), "onlyonce": e.field("OnlyOnce"), "queue_cnt": e.field("QueueCount"), "sign_sites": e.field("SignSites"), "login_sites": e.field("LoginSites"), "retry_keyword": e.field("RetryKeyword"), "auto_cf": e.field("AutoCF"), "clean": e.field("Clean")}).(Object)
	if err := e.effect("config", "", desired); err != nil {
		return err
	}
	e.Config = desired
	e.trace(Object{"op": "config", "value": e.Config})
	return nil
}
func (e *Engine) custom() []Object {
	if e.CustomLive != nil {
		return e.CustomLive()
	}
	if Truth(e.Inventory.CustomEnabled) {
		return e.Inventory.Custom
	}
	return nil
}
func (e *Engine) filter(selected any) (any, error) {
	all, err := contains(selected, "all")
	if err != nil {
		return nil, err
	}
	if all {
		return []any{"all"}, nil
	}
	retained := []any{}
	rows := append([]Object{}, e.Inventory.Configured...)
	if Truth(e.Inventory.CustomEnabled) {
		rows = append(rows, e.Inventory.Custom...)
	}
	for _, site := range rows {
		ok, err := contains(selected, site["id"])
		if err != nil {
			return nil, err
		}
		if ok {
			retained = append(retained, site["id"])
		}
	}
	return retained, nil
}

// Init retains instance fields on a falsy configuration like Manager.init_plugin.
// A full source reload must construct New, rather than calling Init on this object.
func (e *Engine) Init(value any) error {
	if err := e.Stop(); err != nil {
		return err
	}
	if Truth(value) {
		c, ok := value.(Object)
		if !ok {
			return fmt.Errorf("'%s' object has no attribute 'get'", typeName(value))
		}
		e.setField("Enabled", c["enabled"])
		e.setField("Notify", c["notify"])
		e.setField("Cron", c["cron"])
		e.setField("OnlyOnce", c["onlyonce"])
		e.setField("QueueCount", c["queue_cnt"])
		if !Truth(e.field("QueueCount")) {
			e.setField("QueueCount", json.Number("5"))
		}
		e.setField("SignSites", c["sign_sites"])
		e.setField("LoginSites", c["login_sites"])
		if !Truth(e.field("SignSites")) {
			e.setField("SignSites", []any{})
		}
		if !Truth(e.field("LoginSites")) {
			e.setField("LoginSites", []any{})
		}
		e.setField("RetryKeyword", c["retry_keyword"])
		e.setField("AutoCF", c["auto_cf"])
		e.setField("Clean", c["clean"])
		if e.LoadInitInventory != nil {
			inventory, loadErr := e.LoadInitInventory()
			if loadErr != nil {
				return loadErr
			}
			e.Inventory = inventory
		}
		var err error
		sourceField7506_0, err := e.filter(e.field("SignSites"))
		if err != nil {
			return err
		}
		e.setField("SignSites", sourceField7506_0)
		sourceField7585_0, err := e.filter(e.field("LoginSites"))
		if err != nil {
			return err
		}
		e.setField("LoginSites", sourceField7585_0)
		if err := e.replaceConfig(); err != nil {
			return err
		}
	}
	if Truth(e.field("Enabled")) || Truth(e.field("OnlyOnce")) {
		if err := e.effect("load_modules", "", nil); err != nil {
			return err
		}
		e.setField("SchemaLoaded", true)
		e.trace(Object{"op": "load_modules"})
		if Truth(e.field("OnlyOnce")) {
			if err := e.effect("scheduler_new", "", Object{"timezone": e.ScheduleTimezone}); err != nil {
				return err
			}
			e.Scheduler = &scheduler{}
			e.trace(Object{"op": "scheduler_new", "timezone": e.ScheduleTimezone})
			e.log("INFO", "站点自动签到服务启动，立即运行一次")
			job := Object{"op": "scheduler_job", "func": Object{"callable": "sign_in"}, "trigger": "date", "run_date": e.Now().In(e.ScheduleZone).Add(3 * time.Second).Format("2006-01-02T15:04:05-07:00"), "name": "站点自动签到"}
			if err := e.effect("scheduler_job", "", job); err != nil {
				return err
			}
			e.trace(job)
			e.setField("OnlyOnce", false)
			if err := e.replaceConfig(); err != nil {
				return err
			}
			if err := e.effect("scheduler_print", "", nil); err != nil {
				return err
			}
			e.trace(Object{"op": "scheduler_print"})
			if err := e.effect("scheduler_start", "", nil); err != nil {
				return err
			}
			e.trace(Object{"op": "scheduler_start"})
			e.Scheduler.Running = true
		}
	}
	return nil
}
func (e *Engine) Snapshot() Object {
	return Object{"enabled": e.field("Enabled"), "notify": e.field("Notify"), "cron": e.field("Cron"), "onlyonce": e.field("OnlyOnce"), "queue_cnt": e.field("QueueCount"), "sign_sites": e.field("SignSites"), "login_sites": e.field("LoginSites"), "retry_keyword": e.field("RetryKeyword"), "auto_cf": e.field("AutoCF"), "clean": e.field("Clean"), "start_time": e.field("StartTime"), "end_time": e.field("EndTime"), "schema_loaded": e.schemaLoaded(), "config": e.Config, "data": e.Data, "trace": e.Trace}
}

//go:embed form.json
var sourceForm []byte

func Form(inventory Inventory) (Object, error) {
	var form Object
	if Decode(sourceForm, &form) != nil {
		return nil, errors.New("invalid source signin form")
	}
	options := []any{Object{"title": "全部", "value": "all"}}
	sites := append([]Object{}, inventory.Configured...)
	if Truth(inventory.CustomEnabled) {
		sites = append(sites, inventory.Custom...)
	}
	for _, site := range sites {
		options = append(options, Object{"title": site["name"], "value": site["id"]})
	}
	var visit func(any)
	visit = func(value any) {
		switch n := value.(type) {
		case []any:
			for _, item := range n {
				visit(item)
			}
		case Object:
			if p, ok := n["props"].(Object); ok && (p["model"] == "sign_sites" || p["model"] == "login_sites") {
				p["items"] = options
			}
			visit(n["content"])
		}
	}
	visit(form["conf"])
	return form, nil
}
func SiteResult(site any, state any, message, action string) Result {
	message = legacyanime.Strip(message)
	b, exactBool := state.(bool)
	success := exactBool && b && message != ""
	if message == "" {
		message = action + "失败，未返回可确认的结果"
	} else if !success && !strings.Contains(message, "失败") {
		message = action + "失败：" + message
	}
	if success && action == "签到" && strings.Contains(message, "登录成功") {
		return Result{site, message + "（未执行签到）", false, true}
	}
	return Result{site, message, success, success}
}
func StatusMeta(value any) Object {
	status := ""
	if Truth(value) {
		status = legacyanime.Strip(pythonScalar(value))
	}
	level, color, icon, label, weight := "none", "grey", "mdi-minus-circle-outline", "未记录", 2
	switch {
	case strings.Contains(status, "Cookie已失效") || strings.Contains(status, "失效"):
		level, color, icon, label, weight = "error", "error", "mdi-cookie-off", status, 0
	case strings.Contains(status, "失败") || strings.Contains(status, "错误") || strings.Contains(status, "未成功") || strings.Contains(status, "未签到") || strings.Contains(status, "未簽到"):
		level, color, icon, label, weight = "error", "error", "mdi-alert-circle", status, 0
	case strings.Contains(status, "重试") || strings.Contains(status, "未确认") || strings.Contains(status, "未执行签到") || strings.Contains(status, "仿真签到成功"):
		level, color, icon, label, weight = "warning", "warning", "mdi-refresh", status, 1
	case strings.Contains(status, "成功") || strings.Contains(status, "已签到"):
		level, color, icon, label, weight = "success", "success", "mdi-check-circle", status, 3
	case status != "":
		icon, label = "mdi-help-circle-outline", status
	}
	return Object{"level": level, "color": color, "icon": icon, "label": label, "sort": weight}
}
