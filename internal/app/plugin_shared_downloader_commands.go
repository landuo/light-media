package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins"
	"light-media/internal/plugins/downloadercommands"
)

// Only the current host configuration selects a native endpoint/credentials.
// This metadata never crosses the wire and is not a new worker-controlled DTO.
type originalDownloaderCommandSnapshot struct {
	configured DownloaderConfig
	args       downloadercommands.Arguments
}

func cloneOriginalDownloaderCommandValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneOriginalDownloaderCommandValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneOriginalDownloaderCommandValue(item)
		}
		return out
	default:
		// Wire decoding admits only JSON scalars (including json.Number), maps
		// and lists. Uploaded []byte is attached only after the one-time claim.
		return value
	}
}

func (s *originalDownloaderCommandSnapshot) clone() *originalDownloaderCommandSnapshot {
	if s == nil {
		return nil
	}
	out := &originalDownloaderCommandSnapshot{configured: s.configured, args: make(downloadercommands.Arguments, len(s.args))}
	if s.configured.Enabled != nil {
		enabled := *s.configured.Enabled
		out.configured.Enabled = &enabled
	}
	out.configured.Mappings = append([]PathMapping(nil), s.configured.Mappings...)
	for key, value := range s.args {
		out.args[key] = cloneOriginalDownloaderCommandValue(value)
	}
	return out
}

func originalSharedDownloaderCommandCapability(method string) bool {
	_, _, ok := originalDownloaderCommandMethod(method)
	return ok
}

func originalDownloaderCommandMethod(method string) (kind, operation string, ok bool) {
	for _, prefix := range []string{"downloader.qbittorrent.", "downloader.transmission."} {
		if strings.HasPrefix(method, prefix) {
			operation = strings.TrimPrefix(method, prefix)
			switch operation {
			case "add", "start", "recheck", "delete_keep_files":
				return strings.TrimSuffix(strings.TrimPrefix(prefix, "downloader."), "."), operation, true
			}
		}
	}
	return "", "", false
}

// This framing decoder is private to the already-authorized command route.
// Only upload_append may exceed the ordinary 128KiB resource envelope. It is
// never selected by trying the generic decoder first or by accepting a method
// name from the payload. The four-field append decoder remains authoritative.
func originalDownloaderCommandFields(raw json.RawMessage) (map[string]json.RawMessage, string, error) {
	if len(raw) == 0 || len(raw) > originalDownloaderUploadAppendMaxBytes || !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, "", originalDownloaderUploadInvalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, "", originalDownloaderUploadInvalid()
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, "", originalDownloaderUploadInvalid()
		}
		if _, exists := fields[key]; exists {
			return nil, "", originalDownloaderUploadInvalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, "", originalDownloaderUploadInvalid()
		}
		fields[key] = value
	}
	closing, err := d.Token()
	var operation string
	if err != nil || closing != json.Delim('}') || !errors.Is(d.Decode(new(any)), io.EOF) || json.Unmarshal(fields["op"], &operation) != nil || operation == "" || (operation != "upload_append" && len(raw) > 128<<10) {
		return nil, "", originalDownloaderUploadInvalid()
	}
	return fields, operation, nil
}

func originalDownloaderCommandKeys(fields map[string]json.RawMessage, keys ...string) bool {
	if len(fields) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, exists := fields[key]; !exists {
			return false
		}
	}
	return true
}

func decodeOriginalDownloaderCommandValue(raw json.RawMessage) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, originalDownloaderUploadInvalid()
	}
	if !errors.Is(d.Decode(new(any)), io.EOF) {
		return nil, originalDownloaderUploadInvalid()
	}
	return value, nil
}

func decodeOriginalDownloaderCommandArgs(raw json.RawMessage, content bool) (downloadercommands.Arguments, error) {
	value, err := decodeOriginalDownloaderCommandValue(raw)
	args, ok := value.(map[string]any)
	if err != nil || !ok {
		return nil, originalDownloaderUploadInvalid()
	}
	_, present := args["content"]
	if present != content {
		return nil, originalDownloaderUploadInvalid()
	}
	return downloadercommands.Arguments(args), nil
}

func (a *App) originalDownloaderCommandSelect(ctx context.Context, scope originalBaseCallScope, kind, key string) (DownloaderConfig, error) {
	if key == "" || !utf8.ValidString(key) {
		return DownloaderConfig{}, originalDownloaderUploadInvalid()
	}
	if err := ctx.Err(); err != nil {
		return DownloaderConfig{}, err
	}
	if err := scope.validate(ctx); err != nil {
		return DownloaderConfig{}, err
	}
	configured, err := a.downloader(key) // Nonempty exact ID: no default/name fallback.
	if err != nil {
		return DownloaderConfig{}, err
	}
	if configured.ID != key || (kind == "qbittorrent" && configured.Kind != "qb" && configured.Kind != "qbittorrent") || (kind == "transmission" && configured.Kind != "tr" && configured.Kind != "transmission") {
		return DownloaderConfig{}, bad("DOWNLOADER_UNSUPPORTED", "下载器类型与当前命令不匹配")
	}
	return configured, nil
}

func (a *App) originalDownloaderCommandGuard(scope originalBaseCallScope, kind string, snapshot *originalDownloaderCommandSnapshot) func(context.Context) error {
	return func(ctx context.Context) error {
		current, err := a.originalDownloaderCommandSelect(ctx, scope, kind, snapshot.configured.ID)
		if err != nil {
			return err
		}
		if current.Config != snapshot.configured.Config {
			return bad("DOWNLOADER_CHANGED", "下载器配置已变化，请重新发起命令")
		}
		return nil
	}
}

type originalDownloaderCommandNativeFactory func(DownloaderConfig, func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error)

func (a *App) originalSharedDownloaderCommandResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	// The only production factory is fixed here. The parameterized private
	// component below permits recording-port tests, not runtime replacement.
	return a.originalDownloaderCommandResourceWithFactory(ctx, scope, method, raw, newOriginalDownloaderCommandNative)
}

func (a *App) originalDownloaderCommandResourceWithFactory(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage, factory originalDownloaderCommandNativeFactory) (json.RawMessage, error) {
	kind, action, allowed := originalDownloaderCommandMethod(method)
	if !allowed || ctx == nil || scope.validate == nil || scope.downloaderUploads == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器命令缺少实际调用范围")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	// Admission precedes claim: App closing must not consume a token without
	// admitting its callback. Existing Wait/Drained ownership remains unchanged.
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	fields, operation, err := originalDownloaderCommandFields(raw)
	if err != nil {
		return nil, err
	}
	var snapshot *originalDownloaderCommandSnapshot
	var ids any
	var guard func(context.Context) error
	respond := func(value any) (json.RawMessage, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		out, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if len(out) > plugins.MaxPayloadBytes {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器命令结果超过既有传输上限")
		}
		if err := guard(ctx); err != nil {
			return nil, err
		}
		return out, nil
	}
	switch operation {
	case "upload_material_begin":
		if action != "add" {
			return nil, originalDownloaderUploadInvalid()
		}
		return a.beginOriginalDownloaderMaterial(ctx, scope, method, kind, fields)
	case "upload_material_submit", "upload_material_abort":
		if action != "add" || !originalDownloaderCommandKeys(fields, "op", "handle") {
			return nil, originalDownloaderUploadInvalid()
		}
		if err = plugins.CheckMaterialHost(ctx, method); err != nil {
			return nil, err
		}
		var handle string
		if json.Unmarshal(fields["handle"], &handle) != nil {
			return nil, originalDownloaderUploadInvalid()
		}
		snapshot, err = scope.downloaderUploads.snapshotMaterial(ctx, method, handle)
		if err != nil {
			return nil, err
		}
		guard = a.originalDownloaderCommandGuard(scope, kind, snapshot)
		if err = guard(ctx); err != nil {
			return nil, err
		}
		if operation == "upload_material_abort" {
			if err = scope.downloaderUploads.abortMaterial(ctx, method, handle); err != nil {
				return nil, err
			}
			return respond(map[string]bool{"aborted": true})
		}
		var content []byte
		snapshot, content, err = scope.downloaderUploads.claimMaterialBound(ctx, method, handle)
		if err != nil {
			return nil, err
		}
		snapshot.args["content"] = content
		guard = a.originalDownloaderCommandGuard(scope, kind, snapshot)
	case "upload_append", "upload_submit", "upload_abort":
		if action != "add" {
			return nil, originalDownloaderUploadInvalid()
		}
		var handle string
		var chunk originalDownloaderUploadAppend
		if operation == "upload_append" {
			chunk, err = decodeOriginalDownloaderUploadAppend(raw)
			handle = chunk.Handle
		} else if !originalDownloaderCommandKeys(fields, "op", "handle") || json.Unmarshal(fields["handle"], &handle) != nil {
			err = originalDownloaderUploadInvalid()
		}
		if err != nil {
			return nil, err
		}
		snapshot, err = scope.downloaderUploads.snapshot(ctx, method, handle)
		if err != nil {
			return nil, err
		}
		guard = a.originalDownloaderCommandGuard(scope, kind, snapshot)
		if err = guard(ctx); err != nil {
			return nil, err
		}
		switch operation {
		case "upload_append":
			next, err := scope.downloaderUploads.append(ctx, method, chunk)
			if err != nil {
				return nil, err
			}
			return respond(struct {
				Handle string `json:"handle"`
				Offset int    `json:"offset"`
			}{handle, next})
		case "upload_abort":
			if err = scope.downloaderUploads.abort(ctx, method, handle); err != nil {
				return nil, err
			}
			return respond(map[string]bool{"aborted": true})
		case "upload_submit":
			var content []byte
			snapshot, content, err = scope.downloaderUploads.claimBound(ctx, method, handle)
			if err != nil {
				return nil, err
			}
			// Metadata and bytes are handed off under the same store lock. Neither
			// transport failure nor an unknown external effect restores this token.
			snapshot.args["content"] = content
			guard = a.originalDownloaderCommandGuard(scope, kind, snapshot)
		}
	case "upload_begin", "invoke":
		isBegin := operation == "upload_begin"
		if isBegin && (action != "add" || !originalDownloaderCommandKeys(fields, "op", "downloader_id", "args", "total", "sha256")) || !isBegin && action == "add" && !originalDownloaderCommandKeys(fields, "op", "downloader_id", "args") || !isBegin && action != "add" && !originalDownloaderCommandKeys(fields, "op", "downloader_id", "ids") {
			return nil, originalDownloaderUploadInvalid()
		}
		var key string
		if json.Unmarshal(fields["downloader_id"], &key) != nil {
			return nil, originalDownloaderUploadInvalid()
		}
		configured, err := a.originalDownloaderCommandSelect(ctx, scope, kind, key)
		if err != nil {
			return nil, err
		}
		snapshot = (&originalDownloaderCommandSnapshot{configured: configured}).clone()
		if action == "add" {
			snapshot.args, err = decodeOriginalDownloaderCommandArgs(fields["args"], !isBegin)
		} else {
			ids, err = decodeOriginalDownloaderCommandValue(fields["ids"])
		}
		if err != nil {
			return nil, err
		}
		guard = a.originalDownloaderCommandGuard(scope, kind, snapshot)
		if err = guard(ctx); err != nil {
			return nil, err
		}
		if isBegin {
			var total *int
			var digest string
			if json.Unmarshal(fields["total"], &total) != nil || total == nil || json.Unmarshal(fields["sha256"], &digest) != nil {
				return nil, originalDownloaderUploadInvalid()
			}
			handle, err := scope.downloaderUploads.beginBound(ctx, method, *total, digest, snapshot)
			if err != nil {
				return nil, err
			}
			out, err := respond(map[string]string{"handle": handle})
			if err != nil {
				_ = scope.downloaderUploads.abort(context.Background(), method, handle)
			}
			return out, err
		}
	default:
		return nil, originalDownloaderUploadInvalid()
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	qb, tr, closeClient, err := factory(snapshot.configured, guard)
	if closeClient != nil {
		defer closeClient()
	}
	if err != nil {
		return nil, err
	}
	if (kind == "qbittorrent" && qb == nil) || (kind == "transmission" && tr == nil) {
		return nil, &downloadercommands.BoundaryError{Reason: "configured native command client is unavailable"}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	log := func(ctx context.Context, level, message string) {
		a.logOriginalPluginContext(ctx, level, "downloader_command", message)
	}
	q := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.QBConfig{Category: snapshot.configured.AutoCategory, Sequential: snapshot.configured.Sequential, FirstLastPiece: snapshot.configured.FirstLast}, Log: log}
	t := downloadercommands.Transmission{Client: tr, Log: log}
	var result any
	if action == "add" && kind == "qbittorrent" {
		var value downloadercommands.QBAddResult
		value, err = q.AddTorrent(ctx, snapshot.args)
		result = struct {
			Success bool     `json:"success"`
			IDs     []string `json:"ids"`
		}{value.Success, value.IDs}
	} else if action == "add" {
		var value any
		value, err = t.AddTorrent(ctx, snapshot.args)
		result = map[string]any{"value": value} // Actual native JSON, never a fake Torrent instance.
	} else {
		var value bool
		switch {
		case kind == "qbittorrent" && action == "start":
			value, err = q.StartTorrents(ctx, ids)
		case kind == "qbittorrent" && action == "recheck":
			value, err = q.RecheckTorrents(ctx, ids)
		case kind == "qbittorrent" && action == "delete_keep_files":
			value, err = q.DeleteTorrentsKeepFiles(ctx, ids)
		case action == "start":
			value, err = t.StartTorrents(ctx, ids)
		case action == "recheck":
			value, err = t.RecheckTorrents(ctx, ids)
		case action == "delete_keep_files":
			value, err = t.DeleteTorrentsKeepFiles(ctx, ids)
		}
		result = map[string]bool{"value": value}
	}
	if checkErr := guard(ctx); checkErr != nil {
		return nil, checkErr
	}
	if err != nil {
		return nil, err
	}
	return respond(result)
}
