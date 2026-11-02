package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/systemcommands"
	"light-media/internal/systemversion"
	"light-media/web"
)

func attachOwnedNativeFinish(t *testing.T, a *App, finish func(context.Context) error) {
	t.Helper()
	if err := a.attachOriginalSystemCommandHandlers(context.Background(), originalSystemCommandHandlers{
		version: func(context.Context, originalSystemCommandInput) error { return nil }, finish: finish,
	}, false); err != nil {
		t.Fatal(err)
	}
}
func awaitOwnedNativeFinish(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("owned finish did not terminate")
		return nil
	}
}
func TestOriginalNativeStartupDoubleReadiness(t *testing.T) {
	t.Run("listener_alone_does_not_finish", func(t *testing.T) {
		a := newTestApp(t)
		var calls atomic.Int32
		attachOwnedNativeFinish(t, a, func(context.Context) error { calls.Add(1); return nil })
		if err := a.MarkSelfServiceListenerReady(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 0 {
			t.Fatal("listener bypassed startup")
		}
		if err := a.finishOriginalSystemStartup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := a.finishOriginalSystemStartup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := a.MarkSelfServiceListenerReady(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatal("duplicate finish", calls.Load())
		}
	})
	t.Run("startup_waits_for_listener", func(t *testing.T) {
		a := newTestApp(t)
		var calls atomic.Int32
		attachOwnedNativeFinish(t, a, func(context.Context) error { calls.Add(1); return nil })
		done := make(chan error, 1)
		go func() { done <- a.finishOriginalSystemStartup(context.Background()) }()
		select {
		case err := <-done:
			t.Fatal("finish passed unready listener", err)
		default:
		}
		if calls.Load() != 0 {
			t.Fatal("notice before listener")
		}
		if err := a.MarkSelfServiceListenerReady(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := awaitOwnedNativeFinish(t, done); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
	})
	t.Run("close_cancels_pending_finish", func(t *testing.T) {
		a := newTestApp(t)
		var calls atomic.Int32
		attachOwnedNativeFinish(t, a, func(context.Context) error { calls.Add(1); return nil })
		done := make(chan error, 1)
		go func() { done <- a.finishOriginalSystemStartup(context.Background()) }()
		a.closeOriginalSystemCommands()
		if err := awaitOwnedNativeFinish(t, done); err == nil {
			t.Fatal("closed wait succeeded")
		}
		if err := a.MarkSelfServiceListenerReady(context.Background()); err == nil {
			t.Fatal("closed listener admitted")
		}
		a.wg.Wait()
		if calls.Load() != 0 {
			t.Fatal("closed generation notified")
		}
	})
	t.Run("safe_submission_returns_before_listener", func(t *testing.T) {
		a := newTestApp(t)
		called := make(chan struct{}, 1)
		attachOwnedNativeFinish(t, a, func(context.Context) error { called <- struct{}{}; return nil })
		a.startOriginalSystemSafeFinish(context.Background())
		select {
		case <-called:
			t.Fatal("safe finish before listener")
		default:
		}
		if err := a.MarkSelfServiceListenerReady(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-called:
		case <-time.After(3 * time.Second):
			t.Fatal("safe finish missing")
		}
		a.wg.Wait()
	})
}

func TestOriginalNativeSuccessfulEmptySyncReleasesOperationLock(t *testing.T) {
	a := newTestApp(t) // Never Start; no listener, executable, market or network.
	s := a.Settings()
	empty := ""
	s.PluginServerURL = &empty
	s.PluginMarkets = []string{}
	s.PluginLocalRepoPaths = []string{}
	if err := a.saveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(a.Settings().PluginMarkets) != 0 || len(a.Settings().PluginLocalRepoPaths) != 0 || *a.Settings().PluginServerURL != "" {
		t.Fatal("owned empty source isolation")
	}
	calls := 0
	unlocked := false
	attachOwnedNativeFinish(t, a, func(context.Context) error {
		calls++
		unlocked = a.pluginOriginalOperationMu.TryLock()
		if unlocked {
			a.pluginOriginalOperationMu.Unlock()
		}
		return nil
	})
	if err := a.MarkSelfServiceListenerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if calls != 1 || !unlocked {
		t.Fatal("successful no-change sync/lock order", calls, unlocked)
	}
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	if calls != 1 {
		t.Fatal("startup completion repeated")
	}
	// A cancelled startup is not a successful condition, even with a listener.
	b := newTestApp(t)
	s = b.Settings()
	s.PluginServerURL = &empty
	s.PluginMarkets = []string{}
	s.PluginLocalRepoPaths = []string{}
	if err := b.saveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	attachOwnedNativeFinish(t, b, func(context.Context) error { t.Error("cancelled startup notified"); return nil })
	if err := b.MarkSelfServiceListenerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.synchronizeOriginalPluginsAtStartup(ctx)
	b.pluginOriginalSystemCommands.mu.Lock()
	claimed := b.pluginOriginalSystemCommands.finishClaimed
	b.pluginOriginalSystemCommands.mu.Unlock()
	if claimed {
		t.Fatal("failed startup claimed finish")
	}
}

func TestOriginalNativeNamedVersionAndPersistentFinishAssembly(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	h := &originalSystemCommandAdapters{app: a}
	remote := "v0.4.20"
	queries := 0
	frontend, err := web.FrontendVersion()
	if err != nil {
		t.Fatal(err)
	}
	notices := []systemcommands.Notice{}
	native, err := systemcommands.NewNativeVersion(originalSystemRestartMarkerKey, originalSystemChannels[:], systemcommands.NativeVersionPorts{
		Latest:        func(context.Context) (*string, error) { queries++; return &remote, nil },
		LocalBackend:  func(context.Context) (string, error) { return appVersion, nil },
		LocalFrontend: func(context.Context) (string, error) { return frontend, nil },
		Notify:        func(_ context.Context, n systemcommands.Notice) error { notices = append(notices, n); return nil },
		Cache:         h, Codec: h, Log: func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.attachOriginalSystemCommandHandlers(context.Background(), a.originalNativeSystemHandlers(native, false), false); err != nil {
		t.Fatal(err)
	}
	if w := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/version"}`, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	w := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/version ignored","channel":"Telegram","source":"forged","user":"forged"}`, cookie, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	if queries != 1 || len(notices) != 1 {
		t.Fatal(queries, notices)
	}
	n := notices[0]
	if n.Channel == nil || n.Channel.Value != "Web" || n.UserID != "admin" || n.Source == nil || *n.Source != "admin" || !n.IncludeSource || n.Title != systemversion.NativeMessage(&remote, appVersion, frontend) {
		t.Fatal("native trusted web routing", n)
	}
	raw, err := h.Encode(context.Background(), map[string]any{"channel": "Web", "userid": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Set(context.Background(), originalSystemRestartMarkerKey, raw); err != nil {
		t.Fatal(err)
	}
	if err = a.MarkSelfServiceListenerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = a.finishOriginalSystemStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if queries != 2 || len(notices) != 2 {
		t.Fatal(queries, notices)
	}
	n = notices[1]
	if n.IncludeSource || n.Source != nil || n.Channel == nil || n.Channel.Value != "Web" || n.UserID != "admin" || n.Title != "系统已重启完成！\n"+systemversion.NativeMessage(&remote, appVersion, frontend) {
		t.Fatal("finish retained source route", n)
	}
	if _, found, err := h.Get(context.Background(), originalSystemRestartMarkerKey); err != nil || found {
		t.Fatal("finish did not delete persisted marker", found, err)
	}
	if a.originalSystemRestartIntents() != nil {
		t.Fatal("test acquired process authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = a.finishOriginalSystemStartup(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel boundary", err)
	}
}
