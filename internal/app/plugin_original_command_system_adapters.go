package app

// Concrete, private host effects for the already accepted system-command
// algorithm. No version provider, network client, public route, process control
// or restart signal is selected or enabled by this file.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"light-media/internal/systemcommands"
)

const originalSystemRestartMarkerKey = "__system_restart__"
const originalSystemRestartMarkerKind = "internal.system-command"

// This opaque value is JSON owned by the native codec, never Python pickle.
// The unexported type prevents a generic cache caller from passing other data.
type originalSystemMarkerBytes []byte

type originalSystemCommandAdapters struct{ app *App }

var _ systemcommands.Cache = (*originalSystemCommandAdapters)(nil)
var _ systemcommands.Codec = (*originalSystemCommandAdapters)(nil)

func originalSystemAdapterBoundary(message string) error {
	return &systemcommands.BoundaryError{Message: message}
}

func (h *originalSystemCommandAdapters) acquire(ctx context.Context) (func(), error) {
	if ctx == nil || h == nil || h.app == nil || h.app.Store == nil {
		return nil, originalSystemAdapterBoundary("system-command host is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !h.app.admitOriginalBackground() {
		return nil, originalSystemAdapterBoundary("system-command host is closing")
	}
	return h.app.wg.Done, nil
}

func originalSystemMarkerKey(key string) error {
	if key != originalSystemRestartMarkerKey {
		return originalSystemAdapterBoundary("system-command marker key is not private")
	}
	return nil
}

// Only failures produced by these fixed native marker IO operations become
// ordinary source-style storage failures. Authority/context failures are never
// source-caught, and raw database/path diagnostics are not exposed in notices.
func originalSystemMarkerIOError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &systemcommands.SourceError{Class: "OSError", Module: "builtins", Message: "native restart marker storage failed"}
}

func (h *originalSystemCommandAdapters) Get(ctx context.Context, key string) (any, bool, error) {
	if err := originalSystemMarkerKey(key); err != nil {
		return nil, false, err
	}
	done, err := h.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer done()
	var raw []byte
	// Read opaque bytes here, not Store.Get's JSON decoder: the accepted source
	// loads cache outside its try, then decodes inside its separate catch site.
	err = h.app.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalSystemRestartMarkerKind, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, originalSystemMarkerIOError(ctx, err)
	}
	return originalSystemMarkerBytes(append([]byte(nil), raw...)), len(raw) != 0, nil
}

func (h *originalSystemCommandAdapters) Set(ctx context.Context, key string, value any) error {
	if err := originalSystemMarkerKey(key); err != nil {
		return err
	}
	raw, ok := value.(originalSystemMarkerBytes)
	if !ok || !utf8.Valid(raw) || !json.Valid(raw) {
		return originalSystemAdapterBoundary("restart marker requires native codec bytes")
	}
	done, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer done()
	return originalSystemMarkerIOError(ctx, h.app.Store.Put(ctx, originalSystemRestartMarkerKind, key, json.RawMessage(append([]byte(nil), raw...))))
}

func (h *originalSystemCommandAdapters) Delete(ctx context.Context, key string) error {
	if err := originalSystemMarkerKey(key); err != nil {
		return err
	}
	done, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer done()
	return originalSystemMarkerIOError(ctx, h.app.Store.Delete(ctx, originalSystemRestartMarkerKind, key))
}

func (h *originalSystemCommandAdapters) Encode(ctx context.Context, value any) (any, error) {
	if ctx == nil {
		return nil, originalSystemAdapterBoundary("missing system-command context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	marker, ok := value.(map[string]any)
	if !ok || len(marker) != 2 {
		return nil, originalSystemAdapterBoundary("restart marker requires only channel and userid")
	}
	channel, ok := marker["channel"].(string)
	if !ok || !utf8.ValidString(channel) {
		return nil, originalSystemAdapterBoundary("invalid restart marker channel")
	}
	userid, present := marker["userid"]
	if !present || !originalSystemMarkerUser(userid) {
		return nil, originalSystemAdapterBoundary("restart marker userid is outside the ordinary domain")
	}
	raw, err := json.Marshal(map[string]any{"channel": channel, "userid": userid})
	if err != nil {
		return nil, originalSystemAdapterBoundary("restart marker cannot be serialized")
	}
	return originalSystemMarkerBytes(raw), nil
}

func originalSystemMarkerUser(value any) bool {
	switch v := value.(type) {
	case nil, bool, int, int64:
		return true
	case string:
		return utf8.ValidString(v)
	case json.Number:
		_, err := v.Int64()
		return err == nil
	default:
		return false
	}
}

func (h *originalSystemCommandAdapters) Decode(ctx context.Context, value any) (any, error) {
	if ctx == nil {
		return nil, originalSystemAdapterBoundary("missing system-command context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, ok := value.(originalSystemMarkerBytes)
	if !ok {
		return nil, originalSystemAdapterBoundary("restart marker is not native codec bytes")
	}
	invalid := func() (any, error) {
		return nil, &systemcommands.SourceError{Class: "ValueError", Module: "builtins", Message: "invalid native restart marker encoding"}
	}
	if !utf8.Valid(raw) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalid()
	}
	// An old JSON-object string stays a string for the original Engine's own
	// legacy decoder; missing fields/unknown channel/falsey markers stay intact.
	return decoded, nil
}

func originalSystemNoticeFields(n systemcommands.Notice) (map[string]any, error) {
	if n.SaveHistory {
		return nil, originalSystemAdapterBoundary("system-command notices must not save outgoing history")
	}
	var channel any
	if n.Channel != nil {
		channel = n.Channel.Value
	}
	fields := map[string]any{"channel": channel, "userid": n.UserID, "title": n.Title, "save_history": false}
	if n.IncludeSource {
		if n.Source == nil {
			fields["source"] = nil
		} else {
			fields["source"] = *n.Source
		}
	}
	return fields, nil
}

func (h *originalSystemCommandAdapters) Notify(ctx context.Context, n systemcommands.Notice) error {
	fields, err := originalSystemNoticeFields(n)
	if err != nil {
		return err
	}
	done, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer done()
	_, err = h.app.postOriginalPluginMessage(ctx, fields)
	// A caught provider failure in NotificationDispatch.Failed is not a thrown
	// source exception. Preserve the real router error, not fabricated delivery.
	return err
}

func (h *originalSystemCommandAdapters) Backup(ctx context.Context) error {
	done, err := h.acquire(ctx)
	if err != nil {
		return err
	}
	defer done()
	// Delegate existing Docker/identity/no-overwrite policy. This is the future
	// narrow native backup-effect seam, not a raw FS emulator for Engine.backup.
	// Preserve its error for the later orchestrator's explicit classification.
	return h.app.backupOriginalPluginCode(ctx)
}

func (h *originalSystemCommandAdapters) UnsupportedRestart(ctx context.Context) (any, error) {
	done, err := h.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	// An unattached App must never infer process authority, send RestartRequested,
	// os.Exit, spawn or signal. The source Engine ignores this ordinary false.
	return false, nil
}
