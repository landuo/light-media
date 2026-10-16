// Package cleaninvalidseed ports the installed CleanInvalidSeed 2.1 algorithm.
// Frozen source evidence lives in testdata/plugins/clean-invalid-seed-2.1.
// It has no concrete downloader, filesystem deletion or notification backend.
package cleaninvalidseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	py "light-media/internal/releasemeta/legacyanime"
)

type Object struct {
	Attrs    map[string]any                  `json:"$object"`
	Faults   map[string]string               `json:"$faults,omitempty"`
	Text     *string                         `json:"$text,omitempty"`
	Mapping  bool                            `json:"$mapping,omitempty"`
	Accessor func(string) (any, bool, error) `json:"-"`
}
type SourceError struct{ Type, Operation string }

func (e *SourceError) Error() string { return e.Type + ": " + e.Operation }
func source(kind, op string) error   { return &SourceError{kind, op} }
func ErrorType(e error) any {
	if e == nil {
		return nil
	}
	var s *SourceError
	if errors.As(e, &s) {
		return s.Type
	}
	return "RuntimeError"
}
func Convert(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if a, ok := x["$object"].(map[string]any); ok {
			o := &Object{Attrs: map[string]any{}, Faults: map[string]string{}}
			for k, v := range a {
				o.Attrs[k] = Convert(v)
			}
			if f, ok := x["$faults"].(map[string]any); ok {
				for k, v := range f {
					o.Faults[k] = fmt.Sprint(v)
				}
			}
			if s, ok := x["$text"].(string); ok {
				o.Text = &s
			}
			o.Mapping, _ = x["$mapping"].(bool)
			return o
		}
		m := map[string]any{}
		for k, v := range x {
			m[k] = Convert(v)
		}
		return m
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = Convert(v)
		}
		return r
	}
	return v
}
func attr(o any, k string, d any) (any, error) {
	if x, ok := o.(*Object); ok {
		if x.Accessor != nil {
			v, present, err := x.Accessor(k)
			if err != nil {
				if ErrorType(err) == "AttributeError" {
					return d, nil
				}
				return nil, err
			}
			if present {
				return v, nil
			}
		}
		if kind := x.Faults[k]; kind != "" {
			if kind == "raise" {
				kind = "RuntimeError"
			}
			if kind == "AttributeError" {
				return d, nil
			}
			return nil, source(kind, "attribute")
		}
		if v, ok := x.Attrs[k]; ok {
			return v, nil
		}
	}
	return d, nil
}
func has(o any, k string) (bool, error) {
	x, ok := o.(*Object)
	if !ok {
		return false, nil
	}
	if x.Accessor != nil {
		_, present, err := x.Accessor(k)
		if err != nil {
			if ErrorType(err) == "AttributeError" {
				return false, nil
			}
			return false, err
		}
		if present {
			return true, nil
		}
	}
	if kind := x.Faults[k]; kind != "" {
		if kind == "raise" {
			kind = "RuntimeError"
		}
		if kind == "AttributeError" {
			return false, nil
		}
		return false, source(kind, "attribute")
	}
	_, ok = x.Attrs[k]
	return ok, nil
}
func required(o any, k string) (any, error) {
	sentinel := new(int)
	v, e := attr(o, k, sentinel)
	if e != nil {
		return nil, e
	}
	if v == sentinel {
		return nil, source("AttributeError", k)
	}
	return v, nil
}

type safeAttributeResult struct {
	value any
	err   error
}

func safeAttribute(o any, timeout time.Duration, fn func() (any, error)) (any, bool) {
	if x, ok := o.(*Object); ok && x.Accessor == nil {
		v, e := fn()
		return v, e == nil
	}
	ch := make(chan safeAttributeResult, 1)
	go func() {
		r := safeAttributeResult{}
		defer func() {
			if recover() != nil {
				r.err = source("RuntimeError", "attribute panic")
			}
			ch <- r
		}()
		r.value, r.err = fn()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.value, r.err == nil
	case <-timer.C:
		return nil, false
	}
}
func SafeHasTimeout(o any, k string, timeout time.Duration) bool {
	v, ok := safeAttribute(o, timeout, func() (any, error) { return has(o, k) })
	return ok && Truth(v)
}
func SafeGetTimeout(o any, k string, d any, timeout time.Duration) any {
	v, ok := safeAttribute(o, timeout, func() (any, error) {
		present, e := has(o, k)
		if e != nil {
			return nil, e
		}
		if !present {
			return d, nil
		}
		return required(o, k)
	})
	if !ok {
		return d
	}
	return v
}
func SafeHas(o any, k string) bool       { return SafeHasTimeout(o, k, 3*time.Second) }
func SafeGet(o any, k string, d any) any { return SafeGetTimeout(o, k, d, 5*time.Second) }
func Truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		return !ok || r.Sign() != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
func number(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		return r, ok
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case int64:
		return new(big.Rat).SetInt64(x), true
	case bool:
		if x {
			return new(big.Rat).SetInt64(1), true
		}
		return new(big.Rat), true
	case float64:
		return new(big.Rat).SetFloat64(x), true
	}
	return nil, false
}
func eq(a, b any) bool {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return x.Cmp(y) == 0
		}
	}
	return reflect.DeepEqual(a, b)
}
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case *Object:
		if x.Text != nil {
			return *x.Text
		}
		return "<synthetic-object>"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, _ := strconv.ParseFloat(string(x), 64)
			return pyFloat(f)
		}
		return string(x)
	case float64:
		return pyFloat(x)
	case []any:
		p := []string{}
		for _, v := range x {
			if s, ok := v.(string); ok {
				p = append(p, strconv.Quote(s))
			} else {
				p = append(p, str(v))
			}
		}
		return "[" + strings.Join(p, ", ") + "]"
	}
	return fmt.Sprint(v)
}
func stringValue(v any, op string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", source("AttributeError", op)
	}
	return s, nil
}
func listValue(v any) ([]any, error) {
	if a, ok := v.([]any); ok {
		return a, nil
	}
	if s, ok := v.([]string); ok {
		out := []any{}
		for _, v := range s {
			out = append(out, v)
		}
		return out, nil
	}
	if v == nil {
		return nil, source("TypeError", "iterate None")
	}
	return nil, source("TypeError", "iterate")
}
func dictGet(v any, k string, d any) (any, error) {
	if x, ok := v.(map[string]any); ok {
		if v, ok := x[k]; ok {
			return v, nil
		}
		return d, nil
	}
	return nil, source("AttributeError", "get")
}
func lines(v any) ([]string, error) {
	if !Truth(v) {
		return []string{}, nil
	}
	s, e := stringValue(v, "split")
	if e != nil {
		return nil, e
	}
	return strings.Split(s, "\n"), nil
}
func contains(list []string, v any) bool {
	for _, s := range list {
		if eq(v, s) {
			return true
		}
	}
	return false
}
func integer(v any) (*big.Int, error) {
	switch x := v.(type) {
	case nil:
		return new(big.Int), nil
	case string:
		n, e := py.Integer(x)
		if e != nil {
			return nil, source("ValueError", "integer")
		}
		return n, nil
	case bool:
		if x {
			return big.NewInt(1), nil
		}
		return new(big.Int), nil
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			n, ok := new(big.Int).SetString(string(x), 10)
			if ok {
				return n, nil
			}
		}
		f, e := strconv.ParseFloat(string(x), 64)
		if math.IsInf(f, 0) {
			return nil, source("OverflowError", "integer")
		}
		if e == nil {
			n, _ := new(big.Float).SetFloat64(f).Int(nil)
			return n, nil
		}
	case float64:
		if math.IsInf(x, 0) {
			return nil, source("OverflowError", "integer")
		}
		if math.IsNaN(x) {
			return nil, source("ValueError", "integer")
		}
		n, _ := new(big.Float).SetFloat64(x).Int(nil)
		return n, nil
	case int:
		return big.NewInt(int64(x)), nil
	case int64:
		return big.NewInt(x), nil
	}
	return nil, source("ValueError", "integer")
}

type Service struct {
	Name, Kind              string
	Inactive, FalseInstance bool
}
type FileStat struct {
	Size  int64
	MTime float64
}
type Host interface {
	Services(context.Context, any) ([]Service, error)
	Torrents(context.Context, Service) ([]*Object, bool, error)
	SetTags(context.Context, Service, any, []any) (any, error)
	DeleteTorrent(context.Context, Service, any, bool) (any, error)
	Exists(string) bool
	IsFile(string) bool
	IsDir(string) bool
	Stat(string) (FileStat, error)
	Children(string) ([]string, error)
	Descendants(string) ([]string, error)
	Unlink(string) error
	RemoveTree(string) error
	Notice(context.Context, map[string]any) error
	SaveConfig(context.Context, map[string]any) error
}
type Job struct {
	Trigger string  `json:"trigger"`
	RunAt   float64 `json:"run_at"`
	Name    string  `json:"name"`
}
type Scheduler struct {
	Running bool  `json:"running"`
	Jobs    []Job `json:"jobs"`
}
type Machine struct {
	Config     map[string]any
	Host       Host
	Now        func() time.Time
	Timezone   string
	Schedulers []*Scheduler
	scheduler  *Scheduler
}

func New(h Host) *Machine {
	return &Machine{Host: h, Now: time.Now, Timezone: "UTC", Config: map[string]any{"enabled": false, "cron": nil, "notify": false, "onlyonce": false, "detect_invalid_files": false, "delete_invalid_files": false, "delete_invalid_torrents": false, "notify_all": false, "label_only": false, "label": "", "download_dirs": "", "exclude_keywords": "", "exclude_categories": "", "exclude_labels": "", "more_logs": false, "min_seeding_days": json.Number("0"), "custom_error_msg": ""}, Schedulers: []*Scheduler{}}
}

var configKeys = []string{"enabled", "cron", "notify", "onlyonce", "delete_invalid_torrents", "delete_invalid_files", "detect_invalid_files", "notify_all", "label_only", "label", "download_dirs", "exclude_keywords", "exclude_categories", "exclude_labels", "custom_error_msg", "more_logs", "downloaders"}
var errorMessages = []string{"torrent not registered with this tracker", "Torrent not registered with this tracker", "torrent banned", "err torrent banned", "Torrent not exists"}

func (m *Machine) customErrors() ([]string, error) {
	v, e := lines(m.Config["custom_error_msg"])
	if e != nil {
		return nil, e
	}
	r := append([]string{}, errorMessages...)
	for _, s := range v {
		if s = py.Strip(s); s != "" {
			r = append(r, s)
		}
	}
	return r, nil
}
func (m *Machine) StopService() {
	if m.scheduler != nil {
		m.scheduler.Jobs = []Job{}
		m.scheduler.Running = false
		m.scheduler = nil
	}
}
func (m *Machine) Init(ctx context.Context, c map[string]any) error {
	m.StopService()
	if len(c) > 0 {
		for _, k := range configKeys {
			m.Config[k] = c[k]
		}
		n, e := integer(c["min_seeding_days"])
		if e != nil {
			if ErrorType(e) != "ValueError" && ErrorType(e) != "TypeError" {
				return e
			}
			m.log(ctx, "warning", "无效的最小做种天数配置: "+str(c["min_seeding_days"])+"，使用默认值 0")
			n = new(big.Int)
		}
		m.Config["min_seeding_days"] = json.Number(n.String())
	}
	if Truth(m.Config["onlyonce"]) {
		m.log(ctx, "info", "清理无效种子服务启动，立即运行一次")
		now := m.Now()
		s := &Scheduler{Jobs: []Job{{"date", float64(now.Unix()) + float64(now.Nanosecond())/1e9 + 3, "清理无效种子"}}}
		m.Schedulers = append(m.Schedulers, s)
		m.scheduler = s
		m.Config["onlyonce"] = false
		if e := m.updateConfig(ctx); e != nil {
			return e
		}
		s.Running = true
	}
	return nil
}
func (m *Machine) updateConfig(ctx context.Context) error {
	c := map[string]any{}
	for _, k := range configKeys {
		c[k] = m.Config[k]
	}
	c["onlyonce"] = false
	c["min_seeding_days"] = m.Config["min_seeding_days"]
	return m.Host.SaveConfig(ctx, c)
}
func (m *Machine) services(ctx context.Context) ([]Service, error) {
	d, ok := m.Config["downloaders"]
	if !ok {
		return nil, source("AttributeError", "_downloaders")
	}
	if !Truth(d) {
		m.log(ctx, "warning", "尚未配置下载器，请检查配置")
		return nil, source("AttributeError", "None.values")
	}
	s, e := m.Host.Services(ctx, d)
	if e != nil {
		return nil, e
	}
	out := []Service{}
	for _, v := range s {
		if !v.Inactive && (v.Kind == "qbittorrent" || v.Kind == "transmission") {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		m.log(ctx, "warning", "没有已连接的下载器，请检查配置")
		return nil, source("AttributeError", "None.values")
	}
	return out, nil
}
func (m *Machine) notice(ctx context.Context, title string, text *string) error {
	n := map[string]any{"mtype": "SiteMessage", "title": title}
	if text != nil {
		n["text"] = *text
	}
	return m.Host.Notice(ctx, n)
}
func (m *Machine) getAll(ctx context.Context, s Service) ([]*Object, error) {
	m.log(ctx, "debug", "开始获取下载器 "+s.Name+" 的种子列表...")
	v, flag, e := m.Host.Torrents(ctx, s)
	if e != nil || flag {
		m.log(ctx, "error", "获取下载器:"+s.Name+"种子失败")
		if Truth(m.Config["notify"]) {
			txt := "获取下载器 " + s.Name + " 种子失败，请检查下载器配置"
			if e = m.notice(ctx, "❌ 【清理无效做种】", &txt); e != nil {
				return nil, e
			}
		}
		return []*Object{}, nil
	}
	if len(v) == 0 {
		m.log(ctx, "warning", "下载器:"+s.Name+" 中没有种子")
	}
	if len(v) == 0 && Truth(m.Config["notify"]) {
		txt := "下载器 " + s.Name + " 中没有种子"
		if e = m.notice(ctx, "ℹ️ 【清理无效做种】", &txt); e != nil {
			return nil, e
		}
	}
	return v, nil
}
