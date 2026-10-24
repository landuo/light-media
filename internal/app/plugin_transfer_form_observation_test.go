package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"strings"
	"testing"
)

type transferPanicTextError struct{}

func (transferPanicTextError) Error() string { panic("must not format raw error") }

func TestTransferFormObservationSafeClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "none"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
		{fmt.Errorf("synthetic-secret: %w", context.Canceled), "canceled"},
		{&plugins.Error{Code: "execution_canceled"}, "execution_canceled"},
		{&plugins.Error{Code: "secret\n\"injected\":true"}, "runner_other"},
		{&APIError{Code: "secret", Message: "secret", Status: 400}, "host_api_error"},
		{errors.New("Cookie=synthetic-secret"), "other"}, {transferPanicTextError{}, "other"},
	} {
		if got := transferFormErrorClass(tc.err); got != tc.want {
			t.Fatal("unexpected fixed error class", got, tc.want)
		}
	}
	if transferFormContextClass(nil) != "unavailable" {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if transferFormContextClass(ctx) != "canceled" {
		t.Fatal("context cancellation")
	}
	for _, id := range []string{"TorrentTransfer", "TorrentTransfermusic"} {
		if newTransferFormObservation(id) == nil {
			t.Fatal("transfer disabled")
		}
	}
	for _, id := range []string{"AutoSignIn", "IYUUAutoSeed", "TorrentTransfer-evil", ""} {
		if newTransferFormObservation(id) != nil {
			t.Fatal("unrelated plugin observed")
		}
	}
}

func TestTransferFormObservationHostPreservesValuesAndRedacts(t *testing.T) {
	d := newTransferFormObservation("TorrentTransfer")
	secret := "synthetic-secret-cookie"
	rawErr := fmt.Errorf("%s: %w", secret, &plugins.Error{Code: secret})
	original := json.RawMessage(`{"body":"synthetic-secret-cookie"}`)
	wrapped := d.wrapHost(func(context.Context, plugins.Identity, string, json.RawMessage) (json.RawMessage, error) {
		return original, rawErr
	})
	got, err := wrapped(context.Background(), plugins.Identity{}, secret, original)
	if string(got) != string(original) || err != rawErr {
		t.Fatal("callback behavior changed")
	}
	d.observe(plugins.RunObservation{Phase: plugins.PhaseInvokeBegin, ContextState: plugins.RunContextActive})
	d.observe(plugins.RunObservation{Phase: 255, ContextState: 255})
	message := d.failureMessage(context.Background(), secret, plugins.RunResult{Result: original, HostCalls: 1, SentWireBytes: 21, ReadWireBytes: 34}, rawErr)
	if strings.Contains(message, secret) || !json.Valid([]byte(message)) {
		t.Fatal("unsafe diagnostic")
	}
	var data map[string]any
	if json.Unmarshal([]byte(message), &data) != nil {
		t.Fatal("diagnostic JSON")
	}
	if data["host_started"] != float64(1) || data["host_ended"] != float64(1) || data["host_calls"] != float64(1) || data["stage"] != "unknown" {
		t.Fatal("missing counters")
	}
	a := newTestApp(t)
	// Exercise the actual log encoder, including its bounded message allowance.
	encoded := a.logs.encode(map[string]any{"level": "WARN", "event": "torrent_transfer_form_failed", "message": message}, a.Settings())
	if strings.Contains(string(encoded.Data), secret) {
		t.Fatal("secret in logger output")
	}
	var disabled *transferFormObservation
	calls := 0
	disabled.wrapHost(func(context.Context, plugins.Identity, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return original, rawErr
	})(context.Background(), plugins.Identity{}, "", nil)
	if calls != 1 || disabled.failureMessage(nil, "runner", plugins.RunResult{}, rawErr) != "" {
		t.Fatal("disabled observer changed callback")
	}
}

// Real product failure path: pluginForm -> actual Runner -> failed process
// start -> the unchanged public API error and the product diagnostic log.
