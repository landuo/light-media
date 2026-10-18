// Package torrenttransfer ports the installed TorrentTransfer 1.10.3 source.
// TorrentTransfermusic is a second identity, not a second algorithm or shared
// owner. The caller supplies all IO; this package never opens user resources.
package torrenttransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Identity struct {
	ID, Name, Description, Prefix string
	Clone                         bool
}

var Original = Identity{"TorrentTransfer", "自动转移做种", "定期转移下载器中的做种任务到另一个下载器。", "torrenttransfer_", false}
var Music = Identity{"TorrentTransfermusic", "自动转移做种 分身", "定期转移下载器中的做种任务到另一个下载器。 (分身版本)", "torrenttransfermusic_", true}

func Lookup(id string) (Identity, bool) {
	switch id {
	case Original.ID:
		return Original, true
	case Music.ID:
		return Music, true
	}
	return Identity{}, false
}

type Service struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Ref      string `json:"ref"`
	Inactive bool   `json:"inactive"`
}
type Torrent map[string]any
type AddSpec struct {
	Content      []byte   `json:"content"`
	SavePath     any      `json:"save_path"`
	Tags         []string `json:"tags"`
	Category     any      `json:"category"`
	SkipChecking bool     `json:"skip_checking"`
}

// SourceTruthy preserves the installed qB wrapper's tuple truthiness even when
// its first bool is false. Accepted is transport evidence, never source truth.
type AddResult struct {
	Accepted, SourceTruthy bool
	Hash, Outcome          string
}
type EffectResult struct {
	Accepted bool
	Outcome  string
}
type Log struct {
	Line    int    `json:"line"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type Notice struct{ Type, Title, Text string }

type Host interface {
	Service(context.Context, string) (*Service, error)
	Exists(context.Context, string) (bool, error)
	ReadFile(context.Context, string) ([]byte, error)
	WriteTemp(context.Context, string, []byte) (string, error)
	Completed(context.Context, Service) ([]Torrent, error)
	Torrents(context.Context, Service, []string) ([]Torrent, error)
	AddPaused(context.Context, Service, AddSpec) (AddResult, error)
	FindByTag(context.Context, Service, string) (string, error)
	Recheck(context.Context, Service, []string) (EffectResult, error)
	Start(context.Context, Service, []string) (EffectResult, error)
	DeleteSource(context.Context, Service, string) (EffectResult, error)
	SaveData(context.Context, string, any) error
	SaveConfig(context.Context, map[string]any) error
	SystemMessage(context.Context, string, string) error
	Notify(context.Context, Notice) error
	RandomTag(context.Context, int) (string, error)
}
type LogSink interface{ Log(context.Context, Log) }

func logAt(ctx context.Context, h Host, line int, level, message string) {
	if s, ok := h.(LogSink); ok {
		s.Log(ctx, Log{line, level, message})
	}
}

type Job struct {
	ID, Name, Kind, Cron string
	Delay, Interval      time.Duration
}
type InitResult struct {
	StopExisting   bool
	Jobs           []Job
	SourceOnlyOnce bool
	ConfigWrites   []map[string]any
}
type Counts struct{ Total, Success, Fail, Skip, DeleteDuplicate int }
type State struct {
	Config         map[string]any      `json:"config"`
	Recheck        map[string][]string `json:"recheck"`
	RecheckRunning bool                `json:"recheck_running"`
}

func clone(v any) (any, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return decode(raw)
}
func decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing source JSON")
	}
	return v, nil
}
func cloneMap(v map[string]any) (map[string]any, error) {
	a, e := clone(v)
	if e != nil {
		return nil, e
	}
	if a == nil {
		return nil, nil
	}
	return a.(map[string]any), nil
}
func truth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return x != "0" && x != "0.0"
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	x := reflect.ValueOf(v)
	switch x.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map:
		return x.Len() != 0
	}
	return true
}
func text(v any) string {
	if v == nil {
		return "None"
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return string(x)
	case []string:
		return "['" + strings.Join(x, "', '") + "']"
	}
	return fmt.Sprint(v)
}
func asString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("source AttributeError: expected string, received %T", v)
	}
	return s, nil
}
func number(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := strconv.ParseFloat(string(x), 64)
		return n
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}
func unique(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}
func contains(v []string, s string) bool {
	for _, a := range v {
		if a == s {
			return true
		}
	}
	return false
}
