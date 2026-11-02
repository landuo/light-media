// Package systemcommands ports the selected SystemChain ordinary system-command
// algorithms. It contains no HTTP, filesystem, clock, process or persistence
// implementation. Host identity, version providers and effects are injected.
package systemcommands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/systemversion"
)

type SourceError struct{ Class, Module, Message string }

func (e *SourceError) Error() string                 { return e.Message }
func sourceError(class, message string) *SourceError { return &SourceError{class, "builtins", message} }

// BoundaryError is not an ordinary Python Exception and is never source-caught.
type BoundaryError struct {
	Message string
	Cause   error
}

func (e *BoundaryError) Error() string        { return e.Message }
func (e *BoundaryError) Unwrap() error        { return e.Cause }
func boundary(message string) error           { return &BoundaryError{Message: message} }
func ordinary(err error) (*SourceError, bool) { e, ok := err.(*SourceError); return e, ok }

type Side string

const (
	Backend  Side = "backend"
	Frontend Side = "frontend"
)

// Channel keeps enum identity separate from the persisted original value.
type Channel struct{ Name, Value string }
type Notice struct {
	Channel       *Channel
	UserID        any
	Source        *string
	IncludeSource bool
	Title         string
	SaveHistory   bool
}
type Response interface {
	Truth(context.Context) (bool, error)
	JSON(context.Context) (any, error)
}
type Reader interface {
	Read(context.Context) (string, error)
	Close() error
}
type Entry struct{ Name string }
type FileSystem interface {
	Exists(context.Context, string) (bool, error)
	OpenFrontend(context.Context, string) (Reader, error) // source: r, utf-8, errors=replace
	Mkdir(context.Context, string) error                  // source: parents=true, exist_ok=true
	Entries(context.Context, string) ([]Entry, error)
	IsDir(context.Context, string) (bool, error)
	IsFile(context.Context, string) (bool, error)
	CopyTree(context.Context, string, string) error
	CopyFile(context.Context, string, string) error
}

// Cache/Codec separate the original get-outside-try from decode-inside-try.
// The opaque value belongs to an adapter; this package never unpickles anything.
// Get.present means source content truthiness, not merely existence of a key.
type Cache interface {
	Get(context.Context, string) (value any, present bool, err error)
	Set(context.Context, string, any) error
	Delete(context.Context, string) error
}
type Codec interface {
	Encode(context.Context, any) (any, error)
	Decode(context.Context, any) (any, error)
}
type Ports struct {
	Guard            func(context.Context) error
	Remote           func(context.Context, Side) (Response, error)
	LocalBackend     func(context.Context) (string, error)
	FrontendPath     func(context.Context) (string, error)
	FrontendFallback func(context.Context) (string, error)
	IsDocker         func(context.Context) (bool, error)
	Restart          func(context.Context) (any, error)
	Notify           func(context.Context, Notice) error
	Log              func(context.Context, string, string) error
	FS               FileSystem
	Cache            Cache
	Codec            Codec
}
type Configuration struct {
	MarkerKey, PluginsDirectory, BackupDirectory string
	Channels                                     []Channel
}
type Engine struct {
	configuration Configuration
	ports         Ports
}

func New(c Configuration, p Ports) (*Engine, error) {
	if c.MarkerKey == "" || c.PluginsDirectory == "" || c.BackupDirectory == "" || p.Remote == nil || p.LocalBackend == nil || p.FrontendPath == nil || p.FrontendFallback == nil || p.IsDocker == nil || p.Restart == nil || p.Notify == nil || p.Log == nil || p.FS == nil || p.Cache == nil || p.Codec == nil {
		return nil, boundary("incomplete system-command dependencies")
	}
	c.Channels = append([]Channel(nil), c.Channels...)
	return &Engine{c, p}, nil
}
func (e *Engine) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.ports.Guard != nil {
		if err := e.ports.Guard(ctx); err != nil {
			return &BoundaryError{Message: "system-command authority rejected", Cause: err}
		}
	}
	return nil
}
func effect[T any](e *Engine, ctx context.Context, f func() (T, error)) (T, error) {
	var zero T
	if err := e.check(ctx); err != nil {
		return zero, err
	}
	v, err := f()
	if gate := e.check(ctx); gate != nil {
		return v, gate
	}
	return v, err
}
func action(e *Engine, ctx context.Context, f func() error) error {
	_, err := effect(e, ctx, func() (struct{}, error) { return struct{}{}, f() })
	return err
}
func (e *Engine) log(ctx context.Context, level, message string) error {
	return action(e, ctx, func() error { return e.ports.Log(ctx, level, message) })
}
func (e *Engine) notify(ctx context.Context, n Notice) error {
	return action(e, ctx, func() error { return e.ports.Notify(ctx, n) })
}

func (e *Engine) Version(ctx context.Context, channel *Channel, userid any, source *string) error {
	title, err := e.versionMessage(ctx)
	if err != nil {
		return err
	}
	return e.notify(ctx, Notice{Channel: channel, UserID: userid, Source: source, IncludeSource: true, Title: title})
}
func (e *Engine) Restart(ctx context.Context, channel *Channel, userid any, source *string) error {
	if err := e.check(ctx); err != nil {
		return err
	}
	if channel != nil {
		truth, err := userTruthy(userid)
		if err != nil {
			return err
		}
		if truth {
			if err = e.notify(ctx, Notice{Channel: channel, UserID: userid, Source: source, IncludeSource: true, Title: "系统正在重启，请耐心等候！"}); err != nil {
				return err
			}
			if err = e.save(ctx, map[string]any{"channel": channel.Value, "userid": userid}); err != nil {
				return err
			}
		}
	}
	if err := e.backup(ctx); err != nil {
		return err
	}
	_, err := effect(e, ctx, func() (any, error) { return e.ports.Restart(ctx) }) // return value intentionally ignored
	return err
}
func (e *Engine) RestartFinish(ctx context.Context) error {
	marker, err := e.load(ctx)
	if err != nil {
		return err
	}
	truth, err := markerTruthy(marker)
	if err != nil {
		return err
	}
	if !truth {
		return nil
	}
	var fields map[string]any
	if m, ok := marker.(map[string]any); ok {
		fields = m
	} else if text, ok := marker.(string); ok {
		fields, err = decodeLegacyMarker(text)
		if err != nil {
			return err
		}
	} else {
		return boundary("restart marker outside ordinary dictionary/JSON-string domain")
	}
	var channel *Channel
	for _, c := range e.configuration.Channels {
		if value, ok := fields["channel"].(string); ok && c.Value == value {
			copy := c
			channel = &copy
			break
		}
	}
	title, err := e.versionMessage(ctx)
	if err != nil {
		return err
	}
	if err = e.notify(ctx, Notice{Channel: channel, UserID: fields["userid"], Title: "系统已重启完成！\n" + title}); err != nil {
		return err
	}
	return action(e, ctx, func() error { return e.ports.Cache.Delete(ctx, e.configuration.MarkerKey) })
}
func (e *Engine) save(ctx context.Context, value any) error {
	raw, err := effect(e, ctx, func() (any, error) { return e.ports.Codec.Encode(ctx, value) })
	if err == nil {
		err = action(e, ctx, func() error { return e.ports.Cache.Set(ctx, e.configuration.MarkerKey, raw) })
	}
	if s, ok := ordinary(err); ok {
		return e.log(ctx, "error", "保存缓存 "+e.configuration.MarkerKey+" 出错："+s.Message)
	}
	return err
}
func (e *Engine) load(ctx context.Context) (any, error) {
	type found struct {
		value   any
		present bool
	}
	item, err := effect(e, ctx, func() (found, error) {
		v, p, x := e.ports.Cache.Get(ctx, e.configuration.MarkerKey)
		return found{v, p}, x
	})
	if err != nil {
		return nil, err
	}
	if !item.present {
		return nil, nil
	}
	value, err := effect(e, ctx, func() (any, error) { return e.ports.Codec.Decode(ctx, item.value) })
	if s, ok := ordinary(err); ok {
		return nil, e.log(ctx, "error", "加载缓存 "+e.configuration.MarkerKey+" 出错："+s.Message)
	}
	return value, err
}
func (e *Engine) versionMessage(ctx context.Context) (string, error) {
	server, err := e.release(ctx, Backend)
	if err != nil {
		return "", err
	}
	front, err := e.release(ctx, Frontend)
	if err != nil {
		return "", err
	}
	local, err := effect(e, ctx, func() (string, error) { return e.ports.LocalBackend(ctx) })
	if err != nil {
		return "", err
	}
	localFront, err := e.frontend(ctx)
	if err != nil {
		return "", err
	}
	return systemversion.Message(systemversion.Release{Version: server}, systemversion.Release{Version: front}, local, localFront), nil
}
func (e *Engine) release(ctx context.Context, side Side) (*string, error) {
	label := "后端"
	if side == Frontend {
		label = "前端"
	}
	v, err := e.releaseBody(ctx, side, label)
	if s, ok := ordinary(err); ok {
		return nil, e.log(ctx, "error", "获取"+label+"最新版本失败："+s.Message)
	}
	return v, err
}
func (e *Engine) releaseBody(ctx context.Context, side Side, label string) (*string, error) {
	response, err := effect(e, ctx, func() (Response, error) { return e.ports.Remote(ctx, side) })
	if err != nil {
		return nil, err
	}
	truth := false
	if response != nil {
		truth, err = effect(e, ctx, func() (bool, error) { return response.Truth(ctx) })
		if err != nil {
			return nil, err
		}
	}
	if !truth {
		return nil, e.log(ctx, "error", "无法获取"+label+"版本信息，请检查网络连接或GitHub API请求。")
	}
	data, err := effect(e, ctx, func() (any, error) { return response.JSON(ctx) })
	if err != nil {
		return nil, err
	}
	if err := validateReleaseFields(data); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, &BoundaryError{Message: "release data outside ordinary JSON", Cause: err}
	}
	selected, err := systemversion.SelectRelease(raw)
	if err != nil {
		return nil, err
	}
	if selected.Caught {
		return nil, boundary("existing selector caught an unprojected source-error family")
	}
	if selected.Version == nil {
		return nil, e.log(ctx, "warn", "获取v2"+label+"最新版本版本出错！")
	}
	if err = e.log(ctx, "info", "获取到"+label+"最新版本："+*selected.Version); err != nil {
		return nil, err
	}
	return selected.Version, nil
}

// The existing selector intentionally collapses caught failures. This narrow
// read-field precheck preserves the original logger exception text without
// reimplementing filtering or numeric sorting. No ignored metadata is checked.
func validateReleaseFields(data any) error {
	rows, ok := data.([]any)
	if !ok {
		return boundary("release JSON outside finite list-of-dictionaries domain")
	}
	all := make([]any, len(rows))
	for i, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return boundary("release row outside dictionary domain")
		}
		tag, found := m["tag_name"]
		if !found {
			return sourceError("KeyError", "'tag_name'")
		}
		all[i] = tag
	}
	for _, value := range all {
		if _, ok := value.(string); !ok {
			kind, known := pythonScalarType(value)
			if !known {
				return boundary("tag type outside ordinary JSON scalar domain")
			}
			return sourceError("TypeError", "expected string or bytes-like object, got '"+kind+"'")
		}
	}
	return nil
}

func (e *Engine) frontend(ctx context.Context) (string, error) {
	path, err := effect(e, ctx, func() (string, error) { return e.ports.FrontendPath(ctx) })
	if err != nil {
		return "", err
	}
	exists, err := effect(e, ctx, func() (bool, error) { return e.ports.FS.Exists(ctx, path) })
	if err != nil {
		return "", err
	}
	if exists {
		var text string
		reader, err := effect(e, ctx, func() (Reader, error) { return e.ports.FS.OpenFrontend(ctx, path) })
		if err == nil && reader == nil {
			return "", boundary("frontend open returned nil reader")
		}
		if reader != nil {
			if err == nil {
				text, err = effect(e, ctx, func() (string, error) { return reader.Read(ctx) })
			}
			// Always release an already opened reader, including cancellation. Never
			// replace a platform/authority failure with an ordinary close failure.
			closeErr := reader.Close()
			if gate := e.check(ctx); gate != nil {
				return "", gate
			}
			_, isOrdinary := ordinary(err)
			if closeErr != nil && (err == nil || isOrdinary) {
				err = closeErr
			}
		}
		if err == nil {
			return systemversion.DecodeVersionFile([]byte(text)), nil
		}
		if s, ok := ordinary(err); ok {
			if x := e.log(ctx, "debug", "加载版本文件 "+path+" 出错："+s.Message); x != nil {
				return "", x
			}
		} else {
			return "", err
		}
	}
	return effect(e, ctx, func() (string, error) { return e.ports.FrontendFallback(ctx) })
}
func (e *Engine) backup(ctx context.Context) error {
	docker, err := effect(e, ctx, func() (bool, error) { return e.ports.IsDocker(ctx) })
	if err != nil {
		return err
	}
	if !docker {
		return nil
	}
	err = e.backupBody(ctx)
	if s, ok := ordinary(err); ok {
		return e.log(ctx, "error", "插件备份失败: "+s.Message)
	}
	return err
}
func (e *Engine) backupBody(ctx context.Context) error {
	fs := e.ports.FS
	root, dest := e.configuration.PluginsDirectory, e.configuration.BackupDirectory
	exists, err := effect(e, ctx, func() (bool, error) { return fs.Exists(ctx, root) })
	if err != nil {
		return err
	}
	if !exists {
		return e.log(ctx, "info", "插件目录不存在，跳过备份")
	}
	if err = action(e, ctx, func() error { return fs.Mkdir(ctx, dest) }); err != nil {
		return err
	}
	entries, err := effect(e, ctx, func() ([]Entry, error) { return fs.Entries(ctx, root) })
	if err != nil {
		return err
	}
	for _, item := range entries {
		if item.Name == "__init__.py" || item.Name == "__pycache__" || item.Name == ".DS_Store" {
			continue
		}
		if item.Name == "" || item.Name == "." || item.Name == ".." || strings.ContainsAny(item.Name, "/\\") {
			return boundary("backup port returned non-child entry")
		}
		from, to := strings.TrimRight(root, "/")+"/"+item.Name, strings.TrimRight(dest, "/")+"/"+item.Name
		directory, err := effect(e, ctx, func() (bool, error) { return fs.IsDir(ctx, from) })
		if err != nil {
			return err
		}
		if directory {
			found, err := effect(e, ctx, func() (bool, error) { return fs.Exists(ctx, to) })
			if err != nil {
				return err
			}
			if found {
				continue
			}
			if err = action(e, ctx, func() error { return fs.CopyTree(ctx, from, to) }); err != nil {
				return err
			}
			if err = e.log(ctx, "debug", "已备份插件目录: "+item.Name); err != nil {
				return err
			}
		} else {
			file, err := effect(e, ctx, func() (bool, error) { return fs.IsFile(ctx, from) })
			if err != nil {
				return err
			}
			if !file {
				continue
			}
			found, err := effect(e, ctx, func() (bool, error) { return fs.Exists(ctx, to) })
			if err != nil {
				return err
			}
			if found {
				continue
			}
			if err = action(e, ctx, func() error { return fs.CopyFile(ctx, from, to) }); err != nil {
				return err
			}
			if err = e.log(ctx, "info", "已备份插件文件: "+item.Name); err != nil {
				return err
			}
		}
	}
	return e.log(ctx, "info", "插件备份完成，备份位置: "+dest)
}
func pythonScalarType(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "NoneType", true
	case bool:
		return "bool", true
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return "float", true
		}
		return "int", true
	case int, int64:
		return "int", true
	case string:
		return "str", true
	}
	return "", false
}
func userTruthy(v any) (bool, error) {
	switch x := v.(type) {
	case nil:
		return false, nil
	case bool:
		return x, nil
	case string:
		return x != "", nil
	case int:
		return x != 0, nil
	case int64:
		return x != 0, nil
	case json.Number:
		if _, err := x.Int64(); err != nil {
			return false, boundary("userid outside finite signed integer domain")
		}
		return x != "0" && x != "-0", nil
	}
	return false, boundary("userid outside ordinary None/string/integer/bool domain")
}
func markerTruthy(v any) (bool, error) {
	if m, ok := v.(map[string]any); ok {
		return len(m) > 0, nil
	}
	if a, ok := v.([]any); ok {
		if len(a) == 0 {
			return false, nil
		}
		return false, boundary("nonempty list marker is outside finite marker domain")
	}
	return userTruthy(v)
}
func decodeLegacyMarker(text string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		// Precisely retain the observed Python property-name syntax error. Other
		// malformed JSON families remain explicit boundaries, not fabricated text.
		start := strings.TrimLeft(text, " \r\n\t")
		if strings.HasPrefix(start, "{") {
			prefix := len(text) - len(start) + 1
			rest := strings.TrimLeft(text[prefix:], " \r\n\t")
			pos := len(text) - len(rest)
			if len(rest) == 0 || (rest[0] != '"' && rest[0] != '}') {
				line := 1 + strings.Count(text[:pos], "\n")
				last := strings.LastIndex(text[:pos], "\n")
				column := utf8.RuneCountInString(text[last+1:pos]) + 1
				message := fmt.Sprintf("Expecting property name enclosed in double quotes: line %d column %d (char %d)", line, column, utf8.RuneCountInString(text[:pos]))
				return nil, &SourceError{Class: "JSONDecodeError", Module: "json.decoder", Message: message}
			}
		}
		return nil, &BoundaryError{Message: "legacy marker JSON syntax outside observed error family", Cause: err}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, boundary("legacy marker contains trailing JSON")
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, boundary("decoded legacy marker outside dictionary domain")
	}
	return fields, nil
}
