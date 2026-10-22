package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestSiteMessageObservedAPIGuardDenialSurvivesRestoredAuthorization(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, false)
	// The fixture replacement retires its old "failed" site before this API
	// request. Assert the actual full event table and authenticated tombstone,
	// then preserve exact sequence, ID and raw payload through the denial.
	beforeEvents := siteMessageReadDomainRows(t, a)
	if len(beforeEvents) != 1 {
		t.Fatal("fixture did not produce exactly its one prior native deletion", beforeEvents)
	}
	siteMessageAssertDomainRows(t, a, beforeEvents)
	var tombstones int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM native_site_deletions`).Scan(&tombstones); err != nil || tombstones != 1 {
		t.Fatal("fixture deletion tombstone count", tombstones, err)
	}
	ctx := context.Background()
	run := enqueueSyntheticPluginHTTP(t, a)
	requestGuards, transactionGuards, commits := 0, 0, 0
	var observedDenial error
	originalKey := a.Settings().APIToken
	// A real claimed API invocation and production collector are used. The
	// deterministic revocation/restoration is placed in its actual hooks, not
	// injected as a premarked error. The initial collection is invoked directly
	// here rather than claiming this part ran through the SDK dispatch loop.
	hooks := &siteCollectionHooks{
		Origin: siteCollectionOrigin{Kind: "api", ID: run.ID, Instance: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision, Version: run.Version, Domain: "known.example"},
		RequestGuard: func(requestCtx context.Context) error {
			requestGuards++
			if requestGuards != 2 {
				return a.validatePluginHTTPRun(requestCtx, run)
			}
			changed := a.Settings()
			changed.APIToken = "owned-temporarily-revoked-key"
			if err := a.saveSettings(requestCtx, changed); err != nil {
				return err
			}
			observedDenial = a.validatePluginHTTPRun(requestCtx, run)
			changed = a.Settings()
			changed.APIToken = originalKey
			if err := a.saveSettings(requestCtx, changed); err != nil {
				return err
			}
			// Return the original observed error after authority has been restored.
			return observedDenial
		},
		Guard: func(tx *sql.Tx) error {
			transactionGuards++
			_, err := a.validatePluginHTTPRunTx(ctx, tx, run)
			return err
		},
		Commit: func(*sql.Tx, siteSnapshot) error { commits++; return nil },
	}
	_, err := a.collectManagedSite(allowDisabledSiteRequests(ctx), "known", run.ID+":domain", hooks)
	var api *APIError
	if observedDenial == nil || !errors.As(observedDenial, &api) || api.Code != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("real API validator did not observe temporary revocation", observedDenial)
	}
	if err == nil || siteMessageAdmissionFailureCode(err) != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("observed request guard denial was swallowed as provider failure", err)
	}
	if requestGuards != 2 || calls.Load() != 1 || transactionGuards != 1 || commits != 0 {
		t.Fatal("guard denial reached final facts/commit or extra HTTP", requestGuards, calls.Load(), transactionGuards, commits)
	}
	// Prove that the late transaction guard would now succeed. This is the
	// original regression: checking authority again cannot undo an earlier
	// definite denial or manufacture a successful no-data source return.
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, validationErr := a.validatePluginHTTPRunTx(ctx, tx, run)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if validationErr != nil {
		t.Fatal("authorization was not actually restored before final guard", validationErr)
	}
	receipt, found, err := a.pluginStore.siteMessageCollectionForRun(ctx, a.Store.DB, run)
	if err != nil || !found || receipt.Stage != "aborted" || receipt.Failure != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("restored key erased the original terminal denial", receipt.Stage, receipt.Failure, err)
	}
	// Now use the real SDK for the same invocation: it must consume the aborted
	// receipt, not try the formerly denied read again using the restored key.
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_API_AUTH_REVOKED" || calls.Load() != 1 {
		t.Fatal("SDK retried or succeeded after definite guard denial", finished.State, finished.ErrorCode, calls.Load(), err)
	}
	siteMessageAssertColdRevocationHasNoEffects(t, a, run, func() int { return int(calls.Load()) }, beforeEvents)
}

func TestSiteMessageBareTransportAuthCodeIsNotObservedGuardDenial(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, false)
	ctx := context.Background()
	run := enqueueSyntheticPluginHTTP(t, a)
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	transportCalls, guards := 0, 0
	// Entirely intercepted transport; no network request is made to any host.
	// A provider/transport can return the same APIError code, but that is not
	// evidence that our local authority guard observed a rejection.
	http.DefaultTransport = siteOutcomeRoundTrip(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return nil, bad("PLUGIN_API_AUTH_REVOKED", "untrusted transport same-code failure")
	})
	hooks := &siteCollectionHooks{
		Origin:       siteCollectionOrigin{Kind: "api", ID: run.ID, Instance: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision, Version: run.Version, Domain: "known.example"},
		RequestGuard: func(c context.Context) error { return a.validatePluginHTTPRun(c, run) },
		Guard:        func(tx *sql.Tx) error { guards++; _, err := a.validatePluginHTTPRunTx(ctx, tx, run); return err },
	}
	_, err := a.collectManagedSite(allowDisabledSiteRequests(ctx), "known", run.ID+":domain", hooks)
	if siteMessageAdmissionFailureCode(err) != "" {
		t.Fatal("transport same-code error falsely became definite local rejection", err)
	}
	receipt, found, readErr := a.pluginStore.siteMessageCollectionForRun(ctx, a.Store.DB, run)
	if readErr != nil || !found || receipt.Stage == "aborted" || receipt.Failure == "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("unmarked transport error fabricated aborted receipt", receipt.Stage, receipt.Failure, readErr)
	}
	if transportCalls == 0 || calls.Load() != 0 || guards < 2 {
		t.Fatal("negative control did not exercise transport and late valid guard", transportCalls, calls.Load(), guards)
	}
	if err := a.validatePluginHTTPRun(ctx, run); err != nil {
		t.Fatal(fmt.Sprint("negative control lost real authorization: ", err))
	}
}
