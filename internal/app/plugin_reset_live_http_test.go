package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
)

func resetLiveHTTPWait(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

// Real App, compiled SDK, native AutoSignIn owner, outbound HTTP connection,
// authenticated inbound Reset and SQLite evidence. An entered request has an
// unknown effect outcome: refusing Reset is the correct result, not a failure
// to be repaired by releasing the fixture's original HTTP response.
// Keep race-instrumented host + lightweight real SDK, and an additional normal
// cmd/server SDK. The original App-test-race first-request failure remains in
// its frozen candidate/logs; changing entry point does not explain that failure.
func TestPluginResetLiveHTTPAutoSignInCancelsConnectionAndPreservesUnknown(t *testing.T) {
	for _, server := range []bool{false, true} {
		name := "real-sdk"
		if server {
			name = "actual-server"
		}
		t.Run(name, func(t *testing.T) { resetLiveHTTPScenario(t, server) })
	}
}
func resetLiveHTTPScenario(t *testing.T, server bool) {
	ctx := context.Background()
	a := newTestApp(t)
	var executable string
	var buildErr error
	if server {
		transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
		executable, buildErr = transferServerBinary.path, transferServerBinary.err
	} else {
		executable, buildErr = buildPluginProcessFixture(false, "reset-autosignin-sdk", "./internal/app/testdata/reset-autosignin-worker")
	}
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "AutoSignIn" {
			return plugins.Command{}, fmt.Errorf("Reset SDK fixture rejects plugin")
		}
		return plugins.Command{Path: executable, Args: []string{"--plugin-worker", id}}, nil
	}
	cookie := setupTestAdmin(t, a)
	entered, connectionCanceled := make(chan struct{}), make(chan struct{})
	fixtureStop, cleanupEntered, allowCleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var stopOnce, cleanupOnce sync.Once
	var requests atomic.Int32
	var rescueUsed atomic.Bool
	siteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			select {
			case <-r.Context().Done():
				close(connectionCanceled)
			case <-fixtureStop:
				// Failure-only teardown. Never used to satisfy the assertions.
				rescueUsed.Store(true)
			}
			return // No success body or acknowledgement for the first request.
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	appServer := httptest.NewServer(a.Handler())
	firstCtx, cancelFirst := context.WithCancel(ctx)
	secondCtx, cancelSecond := context.WithCancel(ctx)
	cancelReset := func() {}
	firstDone, secondDone, resetDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	firstStarted, secondStarted, resetStarted := false, false, false
	defer func() {
		cancelReset()
		cancelFirst()
		cancelSecond()
		cleanupOnce.Do(func() { close(allowCleanup) })
		stopOnce.Do(func() { close(fixtureStop) })
		siteServer.CloseClientConnections()
		appServer.CloseClientConnections()
		for _, job := range []struct {
			started bool
			done    <-chan struct{}
			label   string
		}{{firstStarted, firstDone, "first runtime"}, {secondStarted, secondDone, "second runtime"}, {resetStarted, resetDone, "reset request"}} {
			if job.started {
				select {
				case <-job.done:
				case <-time.After(15 * time.Second):
					t.Errorf("bounded cleanup did not join %s", job.label)
				}
			}
		}
		siteServer.Close()
		appServer.Close()
	}()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(siteServer.URL)}
	signinSitesPublish(t, a, settings)
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, signinConcurrentConfig(true))
	}
	if requests.Load() != 0 {
		t.Fatal("initialization unexpectedly entered site HTTP")
	}
	beforeConfig, err := a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	beforeInstallation, err := a.pluginStore.Installation(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	first := signinConcurrentClaim(t, a, "AutoSignIn")
	firstResult := make(chan error, 1)
	firstStarted = true
	go func() {
		defer close(firstDone)
		firstResult <- a.executePluginRun(firstCtx, first)
	}()
	resetLiveHTTPWait(t, entered, "actual first site request")
	if first.executionLease == nil {
		t.Fatal("real first claim has no supervised lease")
	}
	var beforeClaim, beforeHead []byte
	if err = a.Store.DB.QueryRow(`SELECT a.claim,h.sealed FROM plugin_run_attempts a JOIN plugin_run_attempt_heads h ON h.nonce=a.nonce WHERE a.nonce=? AND a.run_id=?`, first.executionLease.claim.nonce, first.ID).Scan(&beforeClaim, &beforeHead); err != nil {
		t.Fatal(err)
	}
	sourceCount := func() int {
		var n int
		if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_acceptances WHERE instance_id='AutoSignIn'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeSource := sourceCount()
	// This gate delays only final lease cleanup after native business returned.
	// It never releases the original server request or changes its outcome.
	lease := first.executionLease
	lease.mu.Lock()
	oldCleanup := lease.cleanup
	lease.cleanup = func() {
		close(cleanupEntered)
		<-allowCleanup
		if oldCleanup != nil {
			oldCleanup()
		}
	}
	lease.mu.Unlock()
	resetCtx, requestCancel := context.WithTimeout(ctx, 30*time.Second)
	cancelReset = requestCancel
	request, err := http.NewRequestWithContext(resetCtx, http.MethodPost, appServer.URL+"/api/plugins/AutoSignIn/reset", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	type response struct {
		code int
		body []byte
		err  error
	}
	var resetResponse response
	resetStarted = true
	go func() {
		defer close(resetDone)
		result, e := appServer.Client().Do(request)
		if e != nil {
			resetResponse.err = e
			return
		}
		defer result.Body.Close()
		resetResponse.code = result.StatusCode
		resetResponse.body, resetResponse.err = io.ReadAll(result.Body)
	}()
	resetLiveHTTPWait(t, connectionCanceled, "Reset cancellation of the real outbound connection")
	resetLiveHTTPWait(t, cleanupEntered, "runtime/deferred exit into final lease cleanup")
	if rescueUsed.Load() || requests.Load() != 1 {
		t.Fatal("fixture rescued the request or canceled source sent more HTTP", requests.Load())
	}
	select {
	case <-resetDone:
		t.Fatalf("Reset returned before actual lease cleanup: %d %s %v", resetResponse.code, resetResponse.body, resetResponse.err)
	default:
	}
	select {
	case <-firstDone:
		t.Fatal("runtime exited before its final cleanup barrier")
	default:
	}
	unknown, err := a.pluginStore.Run(ctx, first.ID)
	if err != nil || unknown.State != "reconcile_pending" {
		t.Fatal("canceled native effect was not retained as unknown", unknown.State, unknown.ErrorCode, err)
	}
	// Reset waits for A, but B must still enter the actual SDK and site transport.
	second := signinConcurrentClaim(t, a, "AutoSignIn_clone")
	secondResult := make(chan error, 1)
	secondStarted = true
	go func() {
		defer close(secondDone)
		secondResult <- a.executePluginRun(secondCtx, second)
	}()
	resetLiveHTTPWait(t, secondDone, "second instance business while Reset waits for A cleanup")
	if err = <-secondResult; err != nil {
		t.Fatal("unrelated instance lifecycle failed", err)
	}
	secondFinal, err := a.pluginStore.Run(ctx, second.ID)
	if err != nil || secondFinal.State != "succeeded" || requests.Load() < 2 || len(autoSignInSDKState(t, a, second.InstanceID).History) == 0 {
		t.Fatal("other instance lost real business progress", secondFinal.State, requests.Load(), err)
	}
	select {
	case <-resetDone:
		t.Fatal("other instance completion incorrectly satisfied A's join")
	default:
	}
	cleanupOnce.Do(func() { close(allowCleanup) })
	resetLiveHTTPWait(t, firstDone, "first runtime final cleanup")
	if err = <-firstResult; err != nil {
		t.Fatal("unknown business outcome did not persist its lifecycle completion", err)
	}
	resetLiveHTTPWait(t, resetDone, "authenticated Reset rejection after cleanup")
	if resetResponse.err != nil || resetResponse.code == http.StatusOK || resetResponse.code < 400 || resetResponse.code == http.StatusUnauthorized || resetResponse.code == http.StatusForbidden {
		t.Fatalf("expected authenticated unknown-effect rejection, got %d %s %v", resetResponse.code, resetResponse.body, resetResponse.err)
	}
	if bytes.Contains(resetResponse.body, []byte(`"reset":true`)) || rescueUsed.Load() {
		t.Fatal("unknown effect fabricated successful Reset or used fixture rescue")
	}
	afterConfig, err := a.pluginStore.Config(ctx, first.InstanceID)
	if err != nil || afterConfig.Revision != beforeConfig.Revision || !bytes.Equal(afterConfig.Value, beforeConfig.Value) {
		t.Fatal("rejected Reset cleared or changed original configuration", err)
	}
	afterInstallation, err := a.pluginStore.Installation(ctx, first.InstanceID)
	if err != nil || afterInstallation.Generation != beforeInstallation.Generation || afterInstallation.DesiredEnabled != beforeInstallation.DesiredEnabled || !afterInstallation.Installed {
		t.Fatal("rejected Reset changed original installation generation", afterInstallation.Generation, err)
	}
	var afterClaim, afterHead, completion []byte
	if err = a.Store.DB.QueryRow(`SELECT a.claim,h.sealed,a.completion FROM plugin_run_attempts a JOIN plugin_run_attempt_heads h ON h.nonce=a.nonce WHERE a.nonce=? AND a.run_id=?`, lease.claim.nonce, first.ID).Scan(&afterClaim, &afterHead, &completion); err != nil || !bytes.Equal(afterClaim, beforeClaim) || !bytes.Equal(afterHead, beforeHead) || len(completion) == 0 {
		t.Fatal("rejected Reset lost exact claim/head/completion evidence", err)
	}
	lease.mu.Lock()
	expected, released := lease.expected, lease.released
	lease.mu.Unlock()
	if expected == nil || expected.State != "reconcile_pending" || !released {
		t.Fatal("Reset completed before exact unknown terminal receipt and cleanup")
	}
	if _, err = a.pluginStore.runJournal.resolveCompletion(ctx, lease.claim, *expected); err != nil {
		t.Fatal("unknown terminal receipt cannot be verified", err)
	}
	attempts, err := a.pluginStore.runSupervisor.snapshot()
	if err != nil || len(attempts) != 0 || len(a.pluginProcess) != 0 {
		t.Fatal("authenticated Reset returned with a live runtime lease or SDK slot", len(attempts), err)
	}
	state := autoSignInSDKState(t, a, first.InstanceID)
	if state.ActiveRun != first.ID || len(state.History) != 0 || sourceCount() != beforeSource {
		t.Fatal("unknown request fabricated source history or cleared native reconciliation scope")
	}
	if _, _, found, err := a.pluginStore.autoSignInDeferredOutcome(ctx, unknown, autosignin.RunCapability); err == nil && found {
		t.Fatal("unknown external effect fabricated a terminal business receipt")
	}
}
