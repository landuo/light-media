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
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Service struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	RuntimeKind string `json:"runtime_kind,omitempty"`
	Ref         string `json:"ref"`
	Inactive    bool   `json:"inactive"`
}
type Torrent map[string]any
type AddSpec struct {
	Content         []byte          `json:"content"`
	SavePath        any             `json:"save_path"`
	Tags            []string        `json:"tags"`
	Category        any             `json:"category"`
	SkipChecking    any             `json:"skip_checking"`
	SkipCheckingRaw json.RawMessage `json:"-"`
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
	Scheduler      bool
	StartAt        time.Time
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
		if strings.ContainsAny(string(x), ".eE") {
			n, _ := strconv.ParseFloat(string(x), 64)
			return n != 0
		}
		n, ok := new(big.Int).SetString(string(x), 10)
		return !ok || n.Sign() != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case *big.Int:
		return x.Sign() != 0
	case []byte:
		return len(x) != 0
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

func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

// Python JSON integers retain arbitrary precision; decimal/exponent JSON
// values are binary64. Numeric equality also compares bool with 0/1.
func pyNumeric(v any) (*big.Rat, int, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewRat(1, 1), 0, true
		}
		return big.NewRat(0, 1), 0, true
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, _ := strconv.ParseFloat(string(x), 64)
			if math.IsInf(f, 1) {
				return nil, 1, true
			}
			if math.IsInf(f, -1) {
				return nil, -1, true
			}
			return new(big.Rat).SetFloat64(f), 0, true
		}
		n, ok := new(big.Int).SetString(string(x), 10)
		if !ok {
			return nil, 0, false
		}
		return new(big.Rat).SetInt(n), 0, true
	}
	return nil, 0, false
}
func pyEqual(a, b any) bool {
	if an, ai, ok := pyNumeric(a); ok {
		bn, bi, ok := pyNumeric(b)
		if !ok {
			return false
		}
		if ai != 0 || bi != 0 {
			return ai == bi
		}
		return an.Cmp(bn) == 0
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !pyEqual(v, y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !pyEqual(v, w) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func (s Service) actualKind() string {
	if s.RuntimeKind != "" {
		return s.RuntimeKind
	}
	return s.Type
}
