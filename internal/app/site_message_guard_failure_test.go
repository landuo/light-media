package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// Called only after the original API tests' assertions, without weakening or
// changing their synchronous failure checks. Their owned HTTP fixtures remain
// open throughout the cold recovery check. The MTeam fixture is an explicitly
// intercepted RoundTripper, not a request to the public host.
func siteMessageAssertColdRevocationHasNoEffects(t *testing.T, a *App, run PluginRun, requests func() int, expectedEvents ...[]siteMessageDomainRow) {
	t.Helper()
	ctx := context.Background()
	var events []siteMessageDomainRow
	if len(expectedEvents) > 1 {
		t.Fatal("only one explicit pre-request event snapshot is supported")
	}
	if len(expectedEvents) == 1 {
		events = expectedEvents[0]
	}
	siteMessageAssertDomainRows(t, a, events)
	collection, found, err := a.pluginStore.siteMessageCollectionForRun(ctx, a.Store.DB, run)
	if err != nil || !found || collection.Stage != "aborted" || collection.Failure != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("definite observed guard denial lacks authenticated terminal admission", collection.Stage, collection.Failure, found, err)
	}
	count := requests()
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.resumePluginNoticeRoutes(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if err = cold.drainPluginMessages(ctx, 200); err != nil {
		t.Fatal(err)
	}
	finished, err := cold.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "failed" || finished.ErrorCode != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("cold recovery changed definite denial into retry/success", finished.State, finished.ErrorCode, err)
	}
	if requests() != count {
		t.Fatal("cold denial repeated collect/read/mark-read/seeding HTTP", count, requests())
	}
	collection, found, err = cold.pluginStore.siteMessageCollectionForRun(ctx, cold.Store.DB, run)
	if err != nil || !found || collection.Stage != "aborted" || collection.Failure != "PLUGIN_API_AUTH_REVOKED" {
		t.Fatal("cold recovery lost authenticated terminal reason", collection.Stage, collection.Failure, err)
	}
	var effects int
	if err = cold.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM records WHERE kind IN ('site_connection','site_stats_latest','site_stats_history','notification'))+(SELECT count(*) FROM plugin_message_queue)+(SELECT count(*) FROM plugin_site_statistic_operations)+(SELECT count(*) FROM plugin_notice_publications)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatal("denied admission published account/event/notification/queue/API success", effects, err)
	}
	siteMessageAssertDomainRows(t, cold, events)
}

type siteMessageDomainRow struct {
	Sequence int64
	ID       string
	Payload  []byte
}

func siteMessageReadDomainRows(t *testing.T, a *App) []siteMessageDomainRow {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT seq,event_id,payload FROM domain_events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []siteMessageDomainRow
	for rows.Next() {
		var row siteMessageDomainRow
		if err = rows.Scan(&row.Sequence, &row.ID, &row.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func siteMessageAssertDomainRows(t *testing.T, a *App, want []siteMessageDomainRow) {
	t.Helper()
	got := siteMessageReadDomainRows(t, a)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("denied admission added, removed, or altered a domain event", got, want)
	}
	// Only an explicitly captured fixture deletion can be exempted. Authenticate
	// its tombstone again after reopening, not merely its public event reference.
	for _, row := range got {
		var event DomainEvent
		if json.Unmarshal(row.Payload, &event) != nil || event.Type != "site.deleted" || event.SiteID != "failed" || event.EventID != domainEventID("site.deleted", "failed") || row.ID != event.EventID {
			t.Fatal("unexpected event exemption", row)
		}
		if _, err := a.Store.siteDeletedSourceData(context.Background(), a.Store.DB, event); err != nil {
			t.Fatal("fixture deletion lacks authenticated native tombstone", err)
		}
	}
}

func TestSiteMessageDefiniteGuardFailureWhitelist(t *testing.T) {
	revoked := bad("PLUGIN_API_AUTH_REVOKED", "owned synchronous guard denial")
	marked := siteMessageDefiniteGuardError(revoked)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"observed_guard", marked, "PLUGIN_API_AUTH_REVOKED"},
		{"wrapped_observed_guard", fmt.Errorf("observed hook: %w", marked), "PLUGIN_API_AUTH_REVOKED"},
		{"unmarked_same_code", revoked, ""},
		{"wrapped_unmarked_same_code", fmt.Errorf("transport: %w", revoked), ""},
		{"unmarked_site_change", bad("SITE_CHANGED", "owned site edit"), ""},
		{"observed_site_change", siteMessageDefiniteGuardError(bad("SITE_CHANGED", "owned site edit")), "SITE_CHANGED"},
		{"guard_other_api_error", siteMessageDefiniteGuardError(bad("OTHER_FAILURE", "owned")), ""},
		{"guard_unknown", siteMessageDefiniteGuardError(seedhost.ErrUnknown), ""},
		{"guard_cancel", siteMessageDefiniteGuardError(context.Canceled), ""},
		{"guard_sql", siteMessageDefiniteGuardError(sql.ErrTxDone), ""},
		{"matching_error_text", siteMessageDefiniteGuardError(errors.New("PLUGIN_API_AUTH_REVOKED")), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := siteMessageAdmissionFailureCode(tc.err); got != tc.want {
				t.Fatal("unknown/non-observed error misclassified as definite admission failure", got, tc.want)
			}
		})
	}
	if siteMessageDefiniteGuardError(nil) != nil {
		t.Fatal("nil guard failure gained an error")
	}
	for _, original := range []error{revoked, seedhost.ErrUnknown, context.Canceled, sql.ErrTxDone} {
		if !errors.Is(siteMessageDefiniteGuardError(original), original) {
			t.Fatal("guard marker lost original error identity", original)
		}
	}
}
