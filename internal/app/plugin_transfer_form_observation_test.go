package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
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
func TestTransferFormObservationActualProductFailure(t *testing.T) {
	for _, pluginID := range []string{"TorrentTransfer", "TorrentTransfermusic", "AutoSignIn"} {
		t.Run(pluginID, func(t *testing.T) {
			a := newTestApp(t)
			const secret = "synthetic-private-command"
			a.pluginCommand = func(string) (plugins.Command, error) {
				return plugins.Command{Path: filepath.Join(t.TempDir(), secret)}, nil
			}
			manifest, _, ok := builtin.Lookup(pluginID)
			if !ok {
				t.Fatal("manifest missing")
			}
			_, err := a.pluginForm(context.Background(), PluginInstallation{PluginID: pluginID, InstanceID: pluginID, Version: manifest.Version})
			var api *APIError
			if !errors.As(err, &api) || api.Code != "PLUGIN_FORM_FAILED" || api.Message != "加载插件配置表单失败" {
				t.Fatal("public error changed")
			}
			encoded, _ := json.Marshal(api)
			if strings.Contains(string(encoded), secret) {
				t.Fatal("API leaked command")
			}
			flushTestLogs(t, a)
			entries, _, _, _ := a.logs.snapshot(0)
			found := false
			for _, entry := range entries {
				if strings.Contains(string(entry.Data), secret) {
					t.Fatal("log leaked command")
				}
				var row map[string]any
				_ = json.Unmarshal(entry.Data, &row)
				if row["event"] != "torrent_transfer_form_failed" {
					continue
				}
				found = true
				if row["plugin_id"] != pluginID || row["plugin_instance_id"] != pluginID || row["plugin_version"] != manifest.Version {
					t.Fatal("installation attribution lost or crossed")
				}
				var diagnostic map[string]any
				message, _ := row["message"].(string)
				if json.Unmarshal([]byte(message), &diagnostic) != nil {
					t.Fatal("diagnostic was truncated")
				}
				if diagnostic["error_class"] != "worker_start_failed" || diagnostic["runner_context"] != "active" {
					t.Fatal("wrong product failure evidence")
				}
				phases := diagnostic["phases_ms"].(map[string]any)
				if _, ok := phases["start_end"]; !ok {
					t.Fatal("missing actual start boundary")
				}
				if _, ok := phases["invoke_begin"]; ok {
					t.Fatal("invented invoke boundary")
				}
			}
			if found != isTorrentTransfer(pluginID) {
				t.Fatal("product observation identity gate")
			}
		})
	}
}
