// Package autobackup ports the installed AutoBackup 2.1.4 source. Effects are
// explicit host dependencies; a worker-supplied configuration is never a grant.
package autobackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	py "light-media/internal/releasemeta/legacyanime"
)

type Object = map[string]any
type SourceError struct{ Type, Operation string }

func (e *SourceError) Error() string      { return e.Type + ": " + e.Operation }
func source(kind, operation string) error { return &SourceError{kind, operation} }
func ErrorType(e error) any {
	if e == nil {
		return nil
	}
	var v *SourceError
	if errors.As(e, &v) {
		return v.Type
	}
	switch {
	case errors.Is(e, fs.ErrNotExist):
		return "FileNotFoundError"
	case errors.Is(e, fs.ErrPermission):
		return "PermissionError"
	case errors.Is(e, fs.ErrExist):
		return "FileExistsError"
	case errors.Is(e, syscall.ENOTDIR):
		return "NotADirectoryError"
	case errors.Is(e, syscall.EISDIR):
		return "IsADirectoryError"
	}
	var p *fs.PathError
	if errors.As(e, &p) {
		return "OSError"
	}
	return "RuntimeError"
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
			v, _ := strconv.ParseFloat(string(x), 64)
			return v != 0
		}
		r, ok := new(big.Rat).SetString(string(x))
		return !ok || r.Sign() != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return r.Len() != 0
	}
	return true
}
func integer(v any) (*big.Int, error) {
	switch x := v.(type) {
	case string:
		n, e := py.Integer(x)
		if e != nil {
			return nil, source("ValueError", "int")
		}
		return n, nil
	case bool:
		if x {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			v, e := strconv.ParseFloat(string(x), 64)
			if math.IsInf(v, 0) {
				return nil, source("OverflowError", "int")
			}
			if math.IsNaN(v) {
				return nil, source("ValueError", "int")
			}
			if e != nil && v != 0 {
				return nil, source("ValueError", "int")
			}
			r := new(big.Rat).SetFloat64(v)
			return new(big.Int).Quo(r.Num(), r.Denom()), nil
		}
		n, e := py.Integer(string(x))
		if e != nil {
			return nil, source("ValueError", "int")
		}
		return n, nil
	case int:
		return big.NewInt(int64(x)), nil
	case int64:
		return big.NewInt(x), nil
	case float64:
		r := new(big.Rat).SetFloat64(x)
		if r == nil {
			return nil, source("ValueError", "int")
		}
		return new(big.Int).Quo(r.Num(), r.Denom()), nil
	}
	return nil, source("TypeError", "int")
}
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		return string(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return fmt.Sprint(v)
}
func stringPath(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", source("TypeError", "Path")
}
func ownedMap(m Object) Object {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out Object
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&out)
	return out
}

type Entry struct {
	Path    string
	Created float64
	Kind    string
}
type DAV interface {
	List(context.Context, string) ([]string, error)
	Upload(context.Context, string, string, func() error) error
	Check(context.Context, string) (bool, error)
	Delete(context.Context, string) error
}
type Scheduler interface {
	AddDate(context.Context, time.Time, string) error
	Jobs(context.Context) (int, error)
	Print(context.Context) error
	Start(context.Context) error
	Clear(context.Context) error
	Running() bool
	Shutdown(context.Context) error
}
type Dependencies struct {
	Now           func() time.Time
	Timezone      string
	LocalTimezone string
	Log           func(context.Context, string, string)
	DataPath      func() string
	BackupFile    func(context.Context, string) (*string, error)
	ListBackups   func(context.Context, string) ([]Entry, error)
	RemoveFile    func(context.Context, string) error
	RemoveTree    func(context.Context, string) error
	NewDAV        func(context.Context, Object) (DAV, error)
	NewScheduler  func(context.Context, string) (Scheduler, error)
	SaveConfig    func(context.Context, Object) error
	PostMessage   func(context.Context, Object) error
}
type Result struct {
	Success bool
	Message string
}
type Response struct {
	Success     bool    `json:"success"`
	Message     *string `json:"message"`
	MessageI18N *string `json:"message_i18n"`
	Data        any     `json:"data"`
}

func response(success bool, message string) Response {
	return Response{success, &message, &message, Object{}}
}
