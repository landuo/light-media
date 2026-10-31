// Package downloadercommands preserves the fixed source downloader leaf calls.
// Ports are host-owned, fixed operations, not a public arbitrary-RPC interface.
// This component does not perform network, filesystem or downloader IO itself.
package downloadercommands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/jinja/exec"
)

// Arguments preserves omitted keys separately from explicit nil. Values are
// ordinary JSON values (numbers preferably json.Number) and content []byte.
// Authority, configured endpoints and credentials never come from this map.
type Arguments map[string]any

type SourceException struct{ Class, Message string }

func (e *SourceException) Error() string { return e.Message }

// BoundaryError is not a caught source business exception. In particular,
// authorization, cancellation, uncertain transport outcomes and pending source
// branches must not become False/None, which source consumers may ignore.
type BoundaryError struct{ Reason string }

func (e *BoundaryError) Error() string { return "downloader leaf boundary: " + e.Reason }

type LogFunc func(context.Context, string, string)

type QBAddCall struct {
	URLs, TorrentFiles, SavePath, Paused, Tags any
	AutoManagement, Sequential, FirstLastPiece any
	Cookie, Category                           any
	Extra                                      Arguments
}

type TRAddCall struct{ Content, DownloadDir, Paused, Labels, Cookies any }

// DeleteKeepFiles has no option capable of requesting deletion of data.
type QBC interface {
	Add(context.Context, QBAddCall) (any, error)
	Start(context.Context, any) (any, error)
	Recheck(context.Context, any) (any, error)
	DeleteKeepFiles(context.Context, any) (any, error)
}
type TRC interface {
	Add(context.Context, TRAddCall) (any, error)
	Start(context.Context, any) (any, error)
	Recheck(context.Context, any) (any, error)
	DeleteKeepFiles(context.Context, any) (any, error)
}

type QBConfig struct{ Category, Sequential, FirstLastPiece any }

func DefaultQBConfig() QBConfig { return QBConfig{false, false, false} }

type QBittorrent struct {
	Client QBC
	Config QBConfig
	Log    LogFunc
}
type Transmission struct {
	Client TRC
	Log    LogFunc
}

type QBAddResult struct {
	Success bool
	IDs     []string
}

// The source always returns a two-member tuple, including (False, []).
func (QBAddResult) SourceTruthy() bool { return true }
func emptyQB() QBAddResult             { return QBAddResult{IDs: []string{}} }

func present(args Arguments, name string, fallback any) any {
	if value, ok := args[name]; ok {
		return value
	}
	return fallback
}
func valid(value any) error {
	switch v := value.(type) {
	case nil, bool, int, int64, []byte:
		return nil
	case string:
		if utf8.ValidString(v) {
			return nil
		}
	case json.Number:
		if validJSONNumber(v) {
			return nil
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return nil
		}
	case []any:
		for _, item := range v {
			if err := valid(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, item := range v {
			if !utf8.ValidString(key) {
				return &BoundaryError{"invalid JSON object key"}
			}
			if err := valid(item); err != nil {
				return err
			}
		}
		return nil
	}
	return &BoundaryError{"outside the ordinary JSON/bytes value domain"}
}
func validateArgs(args Arguments) error {
	for key, value := range args {
		if !utf8.ValidString(key) {
			return &BoundaryError{"invalid argument name"}
		}
		if err := valid(value); err != nil {
			return err
		}
	}
	return nil
}

// Number validation and truth do not expand an exponent into a rational.
// These operations use at most the supplied textual number's storage.
func validJSONNumber(n json.Number) bool {
	s := string(n)
	if len(s) == 0 || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) || s[len(s)-1] < '0' || s[len(s)-1] > '9' {
		return false
	}
	return json.Valid([]byte(s))
}
func nonzeroMantissa(n json.Number) bool {
	for _, c := range string(n) {
		if c == 'e' || c == 'E' {
			break
		}
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

func truth(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []byte:
		return len(v) != 0
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	case json.Number:
		return validJSONNumber(v) && nonzeroMantissa(v)
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}
func nilIfFalse(v any) any {
	if !truth(v) {
		return nil
	}
	return v
}
func sourceFailure(ctx context.Context, log LogFunc, prefix string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var source *SourceException
	if !errors.As(err, &source) {
		return err
	}
	if log != nil {
		log(ctx, "ERROR", prefix+source.Message)
	}
	return nil
}

func (q QBittorrent) AddTorrent(ctx context.Context, args Arguments) (QBAddResult, error) {
	if err := ctx.Err(); err != nil {
		return emptyQB(), err
	}
	if err := validateArgs(args); err != nil {
		return emptyQB(), err
	}
	content, hasContent := args["content"]
	if !hasContent {
		return emptyQB(), &BoundaryError{"missing required source content argument"}
	}
	if q.Client == nil || !truth(content) {
		return emptyQB(), nil
	}
	for _, value := range []any{q.Config.Category, q.Config.Sequential, q.Config.FirstLastPiece} {
		if err := valid(value); err != nil {
			return emptyQB(), err
		}
	}
	call := QBAddCall{SavePath: nilIfFalse(args["download_dir"]), Paused: present(args, "is_paused", false), Tags: nilIfFalse(args["tag"]), AutoManagement: q.Config.Category, Sequential: q.Config.Sequential, FirstLastPiece: q.Config.FirstLastPiece, Cookie: args["cookie"], Category: args["category"], Extra: Arguments{}}
	if text, ok := content.(string); ok {
		call.URLs = text
	} else {
		call.TorrentFiles = content
	}
	if !truth(present(args, "ignore_category_check", true)) {
		if truth(call.Category) && truth(q.Config.Category) {
			call.AutoManagement = true
		} else {
			call.AutoManagement = false
			call.Category = nil
		}
	}
	for key, value := range args {
		switch key {
		case "content", "is_paused", "download_dir", "tag", "category", "cookie", "ignore_category_check":
		default:
			call.Extra[key] = value
		}
	}
	for _, key := range []string{"urls", "torrent_files", "save_path", "tags", "use_auto_torrent_management", "is_sequential_download", "is_first_last_piece_priority"} {
		if _, exists := call.Extra[key]; exists {
			return emptyQB(), &BoundaryError{"duplicate vendor keyword source TypeError detail is pending: " + key}
		}
	}
	if truth(call.URLs) && truth(call.Cookie) {
		return emptyQB(), &BoundaryError{"string URL/cookie StringUtils/sync/Version branch is pending"}
	}
	reply, err := q.Client.Add(ctx, call)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return emptyQB(), sourceFailure(ctx, q.Log, "添加种子出错：", err)
	}
	result, err := parseQBReply(reply)
	if err != nil {
		return emptyQB(), sourceFailure(ctx, q.Log, "添加种子出错：", err)
	}
	return result, nil
}

// containsOKInJSONRepr is exact for the ASCII sentinel in Python repr of an
// ordinary JSON value: delimiters and numeric/null/bool renderings cannot create
// "Ok", while quoting does not remove those two printable ASCII characters.
// This avoids inventing a Go map iteration order for Python dictionary repr.
func containsOKInJSONRepr(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "Ok")
	case []any:
		for _, item := range v {
			if containsOKInJSONRepr(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range v {
			if strings.Contains(key, "Ok") || containsOKInJSONRepr(item) {
				return true
			}
		}
	}
	return false
}
func scalarString(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "None", nil
	case string:
		return v, nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") && validJSONNumber(v) {
			if v == "-0" {
				return "0", nil
			}
			return string(v), nil
		}
		n, err := strconv.ParseFloat(string(v), 64)
		if err == nil {
			return exec.AsValue(n).String(), nil
		}
	case float64:
		return exec.AsValue(v).String(), nil
	}
	return "", &BoundaryError{"non-scalar qB added ID string representation has not been bound"}
}
func parseQBReply(reply any) (QBAddResult, error) {
	if err := valid(reply); err != nil {
		return emptyQB(), err
	}
	if !truth(reply) {
		return emptyQB(), nil
	}
	if text, ok := reply.(string); ok {
		return QBAddResult{strings.Contains(text, "Ok"), []string{}}, nil
	}
	mapping, _ := reply.(map[string]any)
	ids := mapping["added_torrent_ids"]
	var values []any
	if truth(ids) {
		switch v := ids.(type) {
		case []any:
			values = v
		case string:
			for _, point := range v {
				values = append(values, string(point))
			}
		case map[string]any:
			return emptyQB(), &BoundaryError{"ordered mapping-as-ID iterable has not been bound"}
		default:
			name := "int"
			if _, ok := v.(bool); ok {
				name = "bool"
			}
			if _, ok := v.(float64); ok {
				name = "float"
			}
			if n, ok := v.(json.Number); ok && strings.ContainsAny(string(n), ".eE") {
				name = "float"
			}
			return emptyQB(), &SourceException{"TypeError", fmt.Sprintf("'%s' object is not iterable", name)}
		}
	}
	out := emptyQB()
	for _, id := range values {
		if !truth(id) {
			continue
		}
		text, err := scalarString(id)
		if err != nil {
			return emptyQB(), err
		}
		out.IDs = append(out.IDs, text)
	}
	if len(out.IDs) != 0 || truth(mapping["success_count"]) || truth(mapping["pending_count"]) {
		out.Success = true
		return out, nil
	}
	out.Success = containsOKInJSONRepr(reply)
	return out, nil
}

func (t Transmission) AddTorrent(ctx context.Context, args Arguments) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateArgs(args); err != nil {
		return nil, err
	}
	if _, exists := args["content"]; !exists {
		return nil, &BoundaryError{"missing required source content argument"}
	}
	for key := range args {
		switch key {
		case "content", "is_paused", "download_dir", "labels", "cookie":
		default:
			return nil, &BoundaryError{"unexpected source Transmission argument: " + key}
		}
	}
	if t.Client == nil {
		return nil, nil
	}
	call := TRAddCall{Content: args["content"], DownloadDir: args["download_dir"], Paused: present(args, "is_paused", false), Labels: args["labels"], Cookies: args["cookie"]}
	reply, err := t.Client.Add(ctx, call)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, sourceFailure(ctx, t.Log, "添加种子出错：", err)
	}
	if err := valid(reply); err != nil {
		return nil, err
	}
	return reply, nil // Do not clone/coerce: preserve the recording JSON object identity.
}

func effect(ctx context.Context, ids any, present bool, emptyGuard bool, invoke func(context.Context, any) (any, error), log LogFunc, prefix string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := valid(ids); err != nil {
		return false, err
	}
	if !present || (emptyGuard && !truth(ids)) {
		return false, nil
	}
	_, err := invoke(ctx, ids)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, sourceFailure(ctx, log, prefix, err)
	}
	return true, nil // Source ignores the vendor's return value.
}
func (q QBittorrent) StartTorrents(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, q.Client != nil, false, func(c context.Context, v any) (any, error) { return q.Client.Start(c, v) }, q.Log, "启动种子出错：")
}
func (q QBittorrent) RecheckTorrents(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, q.Client != nil, false, func(c context.Context, v any) (any, error) { return q.Client.Recheck(c, v) }, q.Log, "重新校验种子出错：")
}
func (q QBittorrent) DeleteTorrentsKeepFiles(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, q.Client != nil, true, func(c context.Context, v any) (any, error) { return q.Client.DeleteKeepFiles(c, v) }, q.Log, "删除种子出错：")
}
func (t Transmission) StartTorrents(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, t.Client != nil, false, func(c context.Context, v any) (any, error) { return t.Client.Start(c, v) }, t.Log, "启动种子出错：")
}
func (t Transmission) RecheckTorrents(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, t.Client != nil, false, func(c context.Context, v any) (any, error) { return t.Client.Recheck(c, v) }, t.Log, "重新校验种子出错：")
}
func (t Transmission) DeleteTorrentsKeepFiles(ctx context.Context, ids any) (bool, error) {
	return effect(ctx, ids, t.Client != nil, true, func(c context.Context, v any) (any, error) { return t.Client.DeleteKeepFiles(c, v) }, t.Log, "删除种子出错：")
}
