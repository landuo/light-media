package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"time"
	"unicode/utf8"
)

const (
	QBittorrentAddCapability              = "downloader.qbittorrent.add"
	QBittorrentStartCapability            = "downloader.qbittorrent.start"
	QBittorrentRecheckCapability          = "downloader.qbittorrent.recheck"
	QBittorrentDeleteKeepFilesCapability  = "downloader.qbittorrent.delete_keep_files"
	TransmissionAddCapability             = "downloader.transmission.add"
	TransmissionStartCapability           = "downloader.transmission.start"
	TransmissionRecheckCapability         = "downloader.transmission.recheck"
	TransmissionDeleteKeepFilesCapability = "downloader.transmission.delete_keep_files"
)

// DownloaderCommandArgs is a finite source argument object, not an RPC name or
// authority. Omitted keys differ from explicit nil. Only content may be []byte;
// a typed nil []byte is still empty bytes, whereas an untyped nil is source None.
// Other values are ordinary JSON, with json.Number/RawMessage preserving numbers.
// The host owns current enabled configuration, endpoints and authentication.
type DownloaderCommandArgs map[string]any

type SharedQBittorrentCommands struct {
	Host         HostCaller
	DownloaderID string
}
type SharedTransmissionCommands struct {
	Host         HostCaller
	DownloaderID string
}

type QBittorrentAddResult struct {
	Success bool
	IDs     []string
}

// SourceTruthy is tuple truth, not the first bool or confirmation of a mutation.
// As with any result value, use it only after the accompanying error is nil.
func (QBittorrentAddResult) SourceTruthy() bool { return true }

// Value is the exact ordinary JSON value supplied by the host. It is not a
// fabricated vendor Torrent/Fields object. Native full-object mapping remains
// a host contract and must be proven before claiming those fields are present.
// Source None is the nonempty RawMessage "null"; a Go error returns zero Value.
type TransmissionAddResult struct{ Value json.RawMessage }

const downloaderCommandRequestMaxBytes = 128 << 10
const downloaderCommandAppendMaxBytes = 4*((TorrentMaterialChunkBytes+2)/3) + 512

func (q SharedQBittorrentCommands) AddTorrent(ctx context.Context, args DownloaderCommandArgs) (QBittorrentAddResult, error) {
	raw, err := downloaderCommandAdd(ctx, q.Host, q.DownloaderID, QBittorrentAddCapability, args)
	if err != nil {
		return QBittorrentAddResult{}, err
	}
	fields, err := downloaderCommandObject(raw, "success", "ids")
	if err != nil {
		return QBittorrentAddResult{}, err
	}
	success, err := downloaderCommandBool(fields["success"])
	if err != nil {
		return QBittorrentAddResult{}, err
	}
	var entries []json.RawMessage
	if len(bytes.TrimSpace(fields["ids"])) == 0 || bytes.TrimSpace(fields["ids"])[0] != '[' || json.Unmarshal(fields["ids"], &entries) != nil {
		return QBittorrentAddResult{}, errors.New("invalid downloader add IDs")
	}
	ids := make([]string, len(entries))
	for i, entry := range entries {
		text := bytes.TrimSpace(entry)
		if len(text) == 0 || text[0] != '"' || ValidateSystemConfigUTF8JSON(text) != nil || json.Unmarshal(text, &ids[i]) != nil {
			return QBittorrentAddResult{}, errors.New("invalid downloader add ID")
		}
	}
	return QBittorrentAddResult{Success: success, IDs: ids}, nil
}
func (t SharedTransmissionCommands) AddTorrent(ctx context.Context, args DownloaderCommandArgs) (TransmissionAddResult, error) {
	raw, err := downloaderCommandAdd(ctx, t.Host, t.DownloaderID, TransmissionAddCapability, args)
	if err != nil {
		return TransmissionAddResult{}, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return TransmissionAddResult{}, err
	}
	return TransmissionAddResult{Value: append(json.RawMessage(nil), fields["value"]...)}, nil
}
func (q SharedQBittorrentCommands) StartTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, q.Host, q.DownloaderID, QBittorrentStartCapability, ids)
}
func (q SharedQBittorrentCommands) RecheckTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, q.Host, q.DownloaderID, QBittorrentRecheckCapability, ids)
}
func (q SharedQBittorrentCommands) DeleteTorrentsKeepFiles(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, q.Host, q.DownloaderID, QBittorrentDeleteKeepFilesCapability, ids)
}
func (t SharedTransmissionCommands) StartTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, t.Host, t.DownloaderID, TransmissionStartCapability, ids)
}
func (t SharedTransmissionCommands) RecheckTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, t.Host, t.DownloaderID, TransmissionRecheckCapability, ids)
}
func (t SharedTransmissionCommands) DeleteTorrentsKeepFiles(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, t.Host, t.DownloaderID, TransmissionDeleteKeepFilesCapability, ids)
}

func downloaderCommandReady(ctx context.Context, host HostCaller, id string) error {
	if ctx == nil || host == nil {
		return errors.New("downloader command requires current context and host caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" || !utf8.ValidString(id) {
		return errors.New("downloader command requires an explicit valid downloader ID")
	}
	return nil
}
func downloaderCommandControl(ctx context.Context, host HostCaller, id, capability string, ids any) (bool, error) {
	if err := downloaderCommandReady(ctx, host, id); err != nil {
		return false, err
	}
	if err := downloaderCommandJSONValue(ids); err != nil {
		return false, err
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		IDs          any    `json:"ids"`
	}{"invoke", id, ids})
	if err != nil {
		return false, err
	}
	raw, err := downloaderCommandCall(ctx, host, capability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return false, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return false, err
	}
	return downloaderCommandBool(fields["value"])
}

func downloaderCommandAdd(ctx context.Context, host HostCaller, id, capability string, args DownloaderCommandArgs) (out json.RawMessage, err error) {
	if err = downloaderCommandReady(ctx, host, id); err != nil {
		return nil, err
	}
	content, exists := args["content"]
	if !exists {
		return nil, errors.New("downloader add requires the source content argument")
	}
	plain := make(map[string]any, len(args))
	for key, value := range args {
		if !utf8.ValidString(key) {
			return nil, errors.New("invalid downloader argument name")
		}
		if key == "content" {
			continue
		}
		if err = downloaderCommandJSONValue(value); err != nil {
			return nil, err
		}
		plain[key] = value
	}
	payload, isBytes := content.([]byte)
	if !isBytes {
		if err = downloaderCommandJSONValue(content); err != nil {
			return nil, err
		}
		plain["content"] = content
		request, marshalErr := json.Marshal(struct {
			Op           string         `json:"op"`
			DownloaderID string         `json:"downloader_id"`
			Args         map[string]any `json:"args"`
		}{"invoke", id, plain})
		if marshalErr != nil {
			return nil, marshalErr
		}
		return downloaderCommandCall(ctx, host, capability, request, downloaderCommandRequestMaxBytes)
	}
	if len(payload) > TorrentMaterialMaxBytes {
		return nil, errors.New("downloader add material exceeds the existing 16MiB bound")
	}
	// Freeze one logical input before begin. No mutable caller map or byte slice
	// is consulted after the host may have allocated an invocation-owned token.
	contentBytes := append([]byte{}, payload...)
	digest := sha256.Sum256(contentBytes)
	begin, marshalErr := json.Marshal(struct {
		Op           string         `json:"op"`
		DownloaderID string         `json:"downloader_id"`
		Args         map[string]any `json:"args"`
		Total        int            `json:"total"`
		SHA256       string         `json:"sha256"`
	}{"upload_begin", id, plain, len(contentBytes), hex.EncodeToString(digest[:])})
	if marshalErr != nil {
		return nil, marshalErr
	}
	if len(begin) > downloaderCommandRequestMaxBytes || !json.Valid(begin) {
		return nil, errors.New("downloader upload begin exceeds the existing envelope bound")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// Keep a valid begin receipt even when cancellation arrives concurrently;
	// after admitting its handle the pre-submit defer may reclaim it.
	raw, callErr := host(ctx, capability, begin)
	if callErr != nil {
		return nil, callErr
	}
	fields, decodeErr := downloaderCommandObject(raw, "handle")
	if decodeErr != nil {
		return nil, decodeErr
	}
	var handle string
	if json.Unmarshal(fields["handle"], &handle) != nil || !downloaderCommandHandle(handle) {
		return nil, errors.New("invalid downloader upload handle")
	}
	submitted := false
	defer func() {
		if submitted {
			return
		}
		// This is reclamation only before a submit attempt, never rollback.
		// The same HostCaller retains all capability/identity/scope checks.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		abort, _ := json.Marshal(struct {
			Op     string `json:"op"`
			Handle string `json:"handle"`
		}{"upload_abort", handle})
		cleanupRaw, cleanupErr := host(cleanupCtx, capability, abort)
		if cleanupErr == nil {
			receipt, e := downloaderCommandObject(cleanupRaw, "aborted")
			if e == nil {
				_, e = downloaderCommandTrue(receipt["aborted"])
			}
			cleanupErr = e
		}
		// Best effort does not replace the original failure or promise cleanup.
		_ = cleanupErr
	}()
	for offset := 0; offset < len(contentBytes); {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		next := min(offset+TorrentMaterialChunkBytes, len(contentBytes))
		appendPacket, _ := json.Marshal(struct {
			Op     string `json:"op"`
			Handle string `json:"handle"`
			Offset int    `json:"offset"`
			Data   []byte `json:"data"`
		}{"upload_append", handle, offset, contentBytes[offset:next]})
		raw, err = downloaderCommandCall(ctx, host, capability, appendPacket, downloaderCommandAppendMaxBytes)
		if err != nil {
			return nil, err
		}
		receipt, e := downloaderCommandObject(raw, "handle", "offset")
		if e != nil {
			return nil, e
		}
		var ackHandle string
		var ackOffset *int
		if json.Unmarshal(receipt["handle"], &ackHandle) != nil || ackHandle != handle || json.Unmarshal(receipt["offset"], &ackOffset) != nil || ackOffset == nil || *ackOffset != next {
			return nil, errors.New("downloader append acknowledgement mismatch")
		}
		offset = next
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	submit, _ := json.Marshal(struct {
		Op     string `json:"op"`
		Handle string `json:"handle"`
	}{"upload_submit", handle})
	// Set before calling the host: even a failed/malformed reply can follow an
	// actual effect. Never retry submit, invoke or abort after this attempt.
	submitted = true
	out, err = host(ctx, capability, submit)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func downloaderCommandCall(ctx context.Context, host HostCaller, capability string, request json.RawMessage, max int) (json.RawMessage, error) {
	if len(request) > max || !json.Valid(request) {
		return nil, errors.New("downloader command request exceeds its existing envelope bound")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := host(ctx, capability, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
func downloaderCommandHandle(s string) bool {
	if len(s) != 66 || s[:2] != "u_" {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func downloaderCommandBool(raw json.RawMessage) (bool, error) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, errors.New("downloader command expected an explicit boolean")
}
func downloaderCommandTrue(raw json.RawMessage) (bool, error) {
	v, err := downloaderCommandBool(raw)
	if err != nil || !v {
		return false, errors.New("downloader upload abort not acknowledged")
	}
	return true, nil
}

// Reject lossy Go-string marshalling and non-JSON/custom marshaler objects.
// RawMessage is an explicit JSON escape hatch, never a bytes upload.
func downloaderCommandJSONValue(value any) error {
	type visit struct {
		kind reflect.Kind
		ptr  uintptr
	}
	active := map[visit]bool{}
	var walk func(reflect.Value, int) error
	walk = func(v reflect.Value, depth int) error {
		// Match encoding/json's existing maximum nesting, not a new wire budget.
		if depth > 10000 {
			return errors.New("downloader JSON exceeds encoding/json nesting limit")
		}
		if !v.IsValid() {
			return nil
		}
		if v.CanInterface() {
			switch raw := v.Interface().(type) {
			case json.RawMessage:
				if raw == nil {
					return nil
				}
				return downloaderCommandJSON(raw)
			case json.Number:
				_, err := json.Marshal(raw)
				return err
			}
		}
		if v.CanInterface() {
			if _, custom := v.Interface().(json.Marshaler); custom {
				return errors.New("custom downloader JSON marshaler is not an ordinary value")
			}
			if _, custom := v.Interface().(encoding.TextMarshaler); custom {
				return errors.New("custom downloader text marshaler is not an ordinary value")
			}
		}
		if v.CanAddr() && v.Addr().CanInterface() {
			if _, custom := v.Addr().Interface().(json.Marshaler); custom {
				return errors.New("custom pointer downloader JSON marshaler is not an ordinary value")
			}
			if _, custom := v.Addr().Interface().(encoding.TextMarshaler); custom {
				return errors.New("custom pointer downloader text marshaler is not an ordinary value")
			}
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return nil
			}
			return walk(v.Elem(), depth+1)
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return nil
		case reflect.Float32, reflect.Float64:
			if math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) {
				return errors.New("nonfinite downloader JSON number")
			}
			return nil
		case reflect.String:
			if utf8.ValidString(v.String()) {
				return nil
			}
			return errors.New("invalid downloader Unicode text")
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return errors.New("downloader JSON requires string object keys")
			}
			if v.IsNil() {
				return nil
			}
			mark := visit{v.Kind(), v.Pointer()}
			if active[mark] {
				return errors.New("cyclic downloader JSON")
			}
			active[mark] = true
			defer delete(active, mark)
			iter := v.MapRange()
			for iter.Next() {
				if !utf8.ValidString(iter.Key().String()) {
					return errors.New("invalid downloader JSON key")
				}
				if err := walk(iter.Value(), depth+1); err != nil {
					return err
				}
			}
			return nil
		case reflect.Slice, reflect.Array:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return errors.New("bytes are only supported in the explicit content argument")
			}
			if v.Kind() == reflect.Slice {
				if v.IsNil() {
					return nil
				}
				mark := visit{v.Kind(), v.Pointer()}
				if active[mark] {
					return errors.New("cyclic downloader JSON")
				}
				active[mark] = true
				defer delete(active, mark)
			}
			for i := 0; i < v.Len(); i++ {
				if err := walk(v.Index(i), depth+1); err != nil {
					return err
				}
			}
			return nil
		default:
			return errors.New("downloader arguments must be ordinary JSON values")
		}
	}
	return walk(reflect.ValueOf(value), 0)
}

// Raw values stay raw, including numeric lexemes and escaped code points. The
// duplicate-key check canonicalizes source code points, not replacement UTF-8.
func downloaderCommandJSON(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return errors.New("invalid downloader JSON reply")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				start := d.InputOffset()
				key, err := d.Token()
				if err != nil {
					return err
				}
				if _, ok := key.(string); !ok {
					return errors.New("invalid downloader JSON object key")
				}
				keyRaw := bytes.TrimLeft(raw[start:d.InputOffset()], ", \t\r\n")
				points, err := DecodeSystemConfigJSONString(keyRaw)
				if err != nil {
					return err
				}
				canonical, err := EncodeSystemConfigJSONString(points)
				if err != nil {
					return err
				}
				if seen[string(canonical)] {
					return errors.New("duplicate downloader JSON key")
				}
				seen[string(canonical)] = true
				if err := value(); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid downloader JSON object")
			}
			return nil
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid downloader JSON array")
			}
			return nil
		default:
			return errors.New("invalid downloader JSON delimiter")
		}
	}
	if err := value(); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing downloader JSON")
	}
	return nil
}
func downloaderCommandObject(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	if err := downloaderCommandJSON(raw); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("downloader response must be an object")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(keys) {
		return nil, errors.New("unknown or missing downloader response field")
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return nil, errors.New("missing downloader response field")
		}
	}
	return fields, nil
}
