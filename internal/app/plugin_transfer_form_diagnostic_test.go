package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Diagnostic only: one fresh owned fixture and one real Runner invocation per
// test execution. External -count controls independent samples, not retries.
// No payload, result body, credentials, worker stderr, or raw error is logged.
func transferFormDiagnosticError(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var worker *plugins.Error
	if errors.As(err, &worker) {
		switch worker.Code {
		case "execution_canceled", "worker_start_failed", "worker_failed", "stderr_limit", "handshake_mismatch", "transport_mismatch", "invalid_reply", "invalid_host_call", "identity_mismatch", "host_call_limit", "request_failed":
			return "runner_" + worker.Code
		default:
			return "runner_other"
		}
	}
	switch pluginErrorCodeInternal(err) {
	case "TORRENT_TRANSFER_OWNER_DENIED", "TORRENT_TRANSFER_REFERENCES_UNCLAIMED", "TORRENT_TRANSFER_NAME_CONFLICT":
		return pluginErrorCodeInternal(err)
	}
	var api *APIError
	if errors.As(err, &api) {
		return "api_other"
	}
	return "other"
}

func TestTransferFormDiagnosticOwned(t *testing.T) {
	// Same setup and ordering as transferReadyApp, stopping before installation.
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	_ = setupTestAdmin(t, a)
	hash, raw := transferTestMaterial()
	src := newTransferAppDaemon(t, "qbittorrent", hash, true)
	dst := newTransferAppDaemon(t, "qbittorrent", hash, false)
	a.config = Settings{Downloaders: []DownloaderConfig{
		{Config: download.Config{ID: "owned-from", Kind: "qbittorrent", BaseURL: src.server.URL, APIKey: "transfer-owned-key", Sequential: true, FirstLast: true}, Name: "source"},
		{Config: download.Config{ID: "owned-to", Kind: "qbittorrent", BaseURL: dst.server.URL, APIKey: "transfer-owned-key", Sequential: true, FirstLast: true}, Name: "destination"},
	}, AllowedRoots: []string{a.Dir}}
	if err := a.saveSettings(context.Background(), a.Settings()); err != nil {
		t.Fatal("fixture settings:", transferFormDiagnosticError(err))
	}
	a.pluginTransfer.pollSleep = func(context.Context, time.Duration) error { return nil }
	backup := filepath.Join(a.Dir, "owned-backup")
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal("fixture directory:", transferFormDiagnosticError(err))
	}
	if err := os.WriteFile(filepath.Join(backup, hash+".torrent"), raw, 0600); err != nil {
		t.Fatal("fixture material:", transferFormDiagnosticError(err))
	}

	// Match POST install's lifecycle lock and its generation-zero installation.
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	manifest, _, exists := builtin.Lookup("TorrentTransfer")
	if !exists {
		t.Fatal("compiled transfer manifest absent")
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal("manifest encoding failed")
	}
	in := PluginInstallation{PluginID: "TorrentTransfer", InstanceID: "TorrentTransfer", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: manifestRaw}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case a.pluginProcess <- struct{}{}:
		defer func() { <-a.pluginProcess }()
	case <-ctx.Done():
		t.Fatalf("form_diagnostic stage=pool_wait elapsed_us=%d context=%s", time.Since(started).Microseconds(), transferFormDiagnosticError(ctx.Err()))
	}
	poolUS := time.Since(started).Microseconds()
	// pluginForm performs this lookup after acquiring the pool slot too.
	manifest, _, exists = builtin.Lookup(in.PluginID)
	if !exists || manifest.Version != in.Version {
		t.Fatal("form_diagnostic stage=manifest mismatch")
	}
	command, err := a.pluginExecutable(in.PluginID)
	if err != nil {
		t.Fatal("form_diagnostic stage=command class=" + transferFormDiagnosticError(err))
	}
	type callbackSample struct {
		EnterUS, ExitUS        int64
		Method, Error, Context string
	}
	var callbacks []callbackSample
	runnerStartUS := time.Since(started).Microseconds()
	result, runErr := (plugins.Runner{Timeout: 10 * time.Second}).Run(ctx, command, manifest, in.InstanceID, "config_form", nil, func(hostCtx context.Context, identity plugins.Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
		sample := callbackSample{EnterUS: time.Since(started).Microseconds(), Method: "unexpected"}
		if method == "torrent_transfer.form" {
			sample.Method = "torrent_transfer.form"
		}
		out, hostErr := a.pluginHostCall(hostCtx, in, nil, identity, method, payload)
		sample.ExitUS = time.Since(started).Microseconds()
		sample.Error = transferFormDiagnosticError(hostErr)
		sample.Context = transferFormDiagnosticError(hostCtx.Err())
		callbacks = append(callbacks, sample)
		return out, hostErr
	})
	elapsedUS := time.Since(started).Microseconds()
	contextClass := transferFormDiagnosticError(ctx.Err())
	// Emit observations only after Run has returned; logging does not consume
	// its worker or callback budget. Never interpret timing alone as root cause.
	t.Logf("form_diagnostic pool_us=%d runner_start_us=%d elapsed_us=%d runner_error=%s context=%s host_calls=%d read_wire_bytes=%d sent_wire_bytes=%d stderr_bytes=%d", poolUS, runnerStartUS, elapsedUS, transferFormDiagnosticError(runErr), contextClass, result.HostCalls, result.ReadWireBytes, result.SentWireBytes, result.StderrBytes)
	for i, sample := range callbacks {
		t.Logf("form_diagnostic callback=%d method=%s enter_us=%d exit_us=%d duration_us=%d error=%s context=%s", i+1, sample.Method, sample.EnterUS, sample.ExitUS, sample.ExitUS-sample.EnterUS, sample.Error, sample.Context)
	}
	if runErr != nil {
		t.Fatal("form_diagnostic runner failed; original failure remains recorded")
	}
	var form struct {
		Conf  json.RawMessage            `json:"conf"`
		Model map[string]json.RawMessage `json:"model"`
	}
	if json.Unmarshal(result.Result, &form) != nil || form.Model == nil || !json.Valid(form.Conf) {
		t.Fatal("form_diagnostic invalid form shape")
	}
	if result.HostCalls != 1 || len(callbacks) != 1 || callbacks[0].Method != "torrent_transfer.form" || callbacks[0].Error != "none" {
		t.Fatal("form_diagnostic unexpected host callback sequence")
	}
	t.Log("form_diagnostic single sample passed; this does not erase or explain an earlier failure")
}
