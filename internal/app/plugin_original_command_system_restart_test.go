package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/systemcommands"
)

func TestOriginalNativeRestartAuthenticatedIngressAndPrivateMarker(t *testing.T) {
	a := newTestApp(t) // Owned TempDB; never Start, no listener/worker/process.
	cookie := setupTestAdmin(t, a)
	if err := a.attachOriginalNativeRestart(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// The exact existing authenticated Handler is invoked in-process only.
	if w := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/restart"}`, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
	w := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/restart ignored extra args","channel":"Telegram","source":"forged","user":"forged"}`, cookie, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	h := &originalSystemCommandAdapters{app: a}
	raw, found, err := h.Get(context.Background(), originalSystemRestartMarkerKey)
	if err != nil || !found {
		t.Fatal("real marker missing", found, err)
	}
	marker, err := h.Decode(context.Background(), raw)
	if err != nil || !reflect.DeepEqual(marker, map[string]any{"channel": "Web", "userid": "admin"}) {
		t.Fatal(marker, err)
	}
	var histories, incoming int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&histories); err != nil || histories != 0 {
		t.Fatal("outgoing history must remain false", histories, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='message'`).Scan(&incoming); err != nil || incoming != 1 {
		t.Fatal("authenticated incoming text must persist", incoming, err)
	}
	select {
	case <-a.originalSystemRestartIntents():
	default:
		t.Fatal("post-effects intent missing")
	}
	select {
	case <-a.RestartRequested:
		t.Fatal("same-PID restart channel reached")
	default:
	}
	// No remote/latest provider was selected by implementing restart.
	w = callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/version"}`, cookie, "")
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "MESSAGE_ROUTE_UNAVAILABLE") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestOriginalNativeRestartWithoutControllerCannotRequestProcess(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	if err := a.attachOriginalNativeRestart(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	w := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/restart"}`, cookie, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if a.originalSystemRestartIntents() != nil {
		t.Fatal("standalone App acquired controller")
	}
	select {
	case <-a.RestartRequested:
		t.Fatal("legacy restart channel reached")
	default:
	}
	h := &originalSystemCommandAdapters{app: a}
	if _, found, err := h.Get(context.Background(), originalSystemRestartMarkerKey); err != nil || !found {
		t.Fatal("source helper false must not erase saved marker", found, err)
	}
}

func TestOriginalNativeRestartCoreEventRawScalars(t *testing.T) {
	a := newTestApp(t)
	if err := a.attachOriginalNativeRestart(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// Actual existing core event consumer, not a test-side reconstruction of
	// originalCommandEvent. It produces RawMessage for channel/source/user.
	a.submitOriginalCoreBroadcast(context.Background(), "command.excute", json.RawMessage(`{"cmd":"/restart ignored","channel":"Web","source":"owned-core","user":9007199254740993}`))
	a.wg.Wait()
	h := &originalSystemCommandAdapters{app: a}
	raw, found, err := h.Get(context.Background(), originalSystemRestartMarkerKey)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	marker, err := h.Decode(context.Background(), raw)
	if err != nil || !reflect.DeepEqual(marker, map[string]any{"channel": "Web", "userid": json.Number("9007199254740993")}) {
		t.Fatal("core scalar/integer changed", marker, err)
	}
	select {
	case <-a.originalSystemRestartIntents():
	default:
		t.Fatal("core event did not reach private intent")
	}
	select {
	case <-a.RestartRequested:
		t.Fatal("legacy loop reached")
	default:
	}
	if _, err := originalSystemRestartInput(originalSystemCommandInput{User: json.RawMessage(`{"not":"scalar"}`)}); err == nil {
		t.Fatal("object widened ordinary user domain")
	}
}

func TestOriginalNativeRestartNormalizationAndBackupErrorBoundary(t *testing.T) {
	channel, source, err := originalSystemRestartArguments(originalSystemCommandInput{Channel: "Web", Source: "owned", User: "owned"})
	if err != nil || channel == nil || *channel != (systemcommands.Channel{Name: "Web", Value: "Web"}) || source == nil || *source != "owned" {
		t.Fatal(channel, source, err)
	}
	for _, in := range []originalSystemCommandInput{{Channel: "not-a-channel", User: "owned"}, {Channel: "Web", Source: 3, User: "owned"}, {Channel: "Web", User: 1.5}} {
		if _, _, err := originalSystemRestartArguments(in); err == nil {
			t.Fatal("invalid native argument admitted", in)
		}
	}
	ioErr := &fs.PathError{Op: "read", Path: "owned-only", Err: fs.ErrPermission}
	guard := errors.New("owned artifact identity mismatch")
	if !originalSystemBackupOnlyIO(ioErr) || !originalSystemBackupOnlyIO(errors.Join(ioErr, ioErr)) {
		t.Fatal("typed IO leaf classification")
	}
	for _, err := range []error{guard, errors.Join(ioErr, guard), context.Canceled, errors.Join(ioErr, context.Canceled)} {
		if originalSystemBackupOnlyIO(err) {
			t.Fatal("unknown/guard/cancel became ordinary IO", err)
		}
	}
}
