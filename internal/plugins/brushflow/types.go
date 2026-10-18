// Package brushflow ports the fixed installed BrushFlowLanduo5.2.15 workflow.
// All IO is performed through the trusted App host; the worker never supplies
// a site credential, downloader endpoint or file-system grant.
package brushflow

import (
	"context"
	"encoding/json"
	"fmt"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"math"
	"strconv"
	"strings"
	"time"
)

const PluginID = "BrushFlowLanduo"
const Version = "5.2.15"
const GlobalTag = "刷流"

var DataNames = []string{"torrents", "archived", "unmanaged", "statistic", "runs"}

type Row map[string]any
type Rows map[string]Row
type Task struct{ Row Row }

func (t Task) ID() string         { return text(t.Row["id"]) }
func (t Task) Name() string       { return text(t.Row["name"]) }
func (t Task) S(k string) string  { return text(t.Row[k]) }
func (t Task) B(k string) bool    { return truth(t.Row[k]) }
func (t Task) N(k string) float64 { return number(t.Row[k]) }
func (t Task) Tag() string {
	s := []rune(t.ID())
	if len(s) > 8 {
		s = s[:8]
	}
	return "刷流-" + string(s)
}

type Config struct {
	Settings   Row
	Tasks      []Task
	LegacyOnce bool
}

func (c Config) Enabled() bool { return truth(c.Settings["enabled"]) }
func (c Config) GlobalDynamic() bool {
	return truth(c.Settings["global_proxy_delete"]) && truth(c.Settings["global_delete_size_range"])
}
func (c Config) Map() Row {
	r := clone(c.Settings)
	ts := []any{}
	for _, t := range c.Tasks {
		ts = append(ts, clone(t.Row))
	}
	r["tasks"] = ts
	r["schema_version"] = 2
	return r
}

type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}
type Service struct {
	ID, Name, TaskID, Operation, Cron string
	Minutes                           int
}
type Site struct {
	ID                        int
	Name, Domain, URL, Schema string
	Public                    bool
	Raw                       Row
}
type Downloader struct {
	Name, Kind string
	Active     bool
}
type UserData struct {
	Domain                  string
	Ratio, Upload, Download any
	UpdatedDay, UpdatedTime string
}
type DownloadState struct {
	Kind string
	Raw  Row
}
type Transfer struct{ UploadSpeed, DownloadSpeed, UploadSize, DownloadSize float64 }
type Notice struct {
	Title, Text string
	System      bool
}
type Event struct {
	Name, Hash, Downloader string
	Data                   Row
}
type Host interface {
	Now() time.Time
	NewID() string
	RandomTag() string
	Sleep(context.Context, time.Duration) error
	Get(context.Context, string) (any, error)
	Put(context.Context, string, any) error
	Del(context.Context, string) error
	SaveConfig(context.Context, Row) error
	RefreshSchedules(context.Context, []Service) error
	Sites(context.Context) ([]Site, error)
	Downloaders(context.Context) ([]Downloader, error)
	SiteUsers(context.Context) ([]UserData, error)
	Torrents(context.Context, string) ([]DownloadState, error)
	Downloading(context.Context, string, string) ([]DownloadState, error)
	Completed(context.Context, string, []string) ([]DownloadState, error)
	TransferInfo(context.Context) ([]Transfer, error)
	Browse(context.Context, int, bool) ([]Row, error)
	SubscribeTitles(context.Context) ([]string, error)
	SiteRequest(context.Context, int, string, string, Row) (Row, error)
	Download(context.Context, Task, Row, Row, string) (string, error)
	Delete(context.Context, string, []string, bool) (bool, error)
	Pause(context.Context, string, []string) (bool, error)
	Reannounce(context.Context, string, []string) error
	Tags(context.Context, string) ([]string, error)
	DeleteTags(context.Context, string, []string) (bool, error)
	Queue(context.Context, string, string) error
	Message(context.Context, Notice) error
	Event(context.Context, Event) error
	Log(context.Context, string, string)
}

func truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case Row:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	default:
		return number(v) != 0
	}
}
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		n, _ := x.Float64()
		return n
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		n, _ := sourceFloat(x)
		return n
	}
	return 0
}

// Python float() accepts Unicode decimal digits in consumed numeric text.
// Use the pinned CPython Unicode table rather than the Go runtime version.
func sourceDecimalASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 127 {
			if n, ok := pyre.DecimalValue(r); ok {
				r = rune('0' + n)
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
func sourceFloat(s string) (float64, error) {
	return strconv.ParseFloat(sourceDecimalASCII(strings.TrimSpace(s)), 64)
}

func parseNumber(v any) any {
	if v == nil || v == "" {
		return nil
	}
	switch v.(type) {
	case string, float64, float32, int, int64, json.Number, bool:
	default:
		return nil
	}
	if s, ok := v.(string); ok {
		n, e := sourceFloat(s)
		if e != nil {
			return nil
		}
		return n
	}
	return number(v)
}
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case float64:
		if math.IsNaN(x) {
			return "nan"
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}
func clean(v any) any {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(text(v))
	if s == "" {
		return nil
	}
	return s
}
func row(v any) Row {
	switch x := v.(type) {
	case Row:
		return x
	case map[string]any:
		return Row(x)
	}
	return Row{}
}
func list(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []Row:
		r := []any{}
		for _, v := range x {
			r = append(r, v)
		}
		return r
	case []string:
		r := []any{}
		for _, v := range x {
			r = append(r, v)
		}
		return r
	}
	return nil
}
func clone(v Row) Row {
	r := Row{}
	for k, x := range v {
		r[k] = x
	}
	return r
}
func keys(r Rows) []string {
	out := []string{}
	for k := range r {
		out = append(out, k)
	}
	return out
}
func unique(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func fallback(v, w any) any {
	if truth(v) {
		return v
	}
	return w
}
func optional(v any) any {
	if truth(v) {
		return v
	}
	return nil
}
func floatptr(v any) *float64 {
	if !truth(v) {
		return nil
	}
	n := number(v)
	return &n
}
func taskKey(id, name string) string { return "task." + id + "." + name }
