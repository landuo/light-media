package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	seed "light-media/internal/plugins/cleaninvalidseed"
)

func TestPluginCleanInvalidSeedMutationPhaseActualSDKReceiptsContinuationAndColdRecovery(t *testing.T) {
	for _, mode := range []string{"known_missing", "known_ambiguous", "known_read_transport", "known_reply_loss", "known_receipt_sql_failure", "unknown_post500", "unknown_disconnect"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			var firstWrites, secondWrites atomic.Int32
			first, second, good := strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("b", 40)
			row := func(hash string) map[string]any {
				return map[string]any{"hash": hash, "name": "owned-" + hash[:1], "content_path": "/unused-owned-keep-data-" + hash[:1], "category": "movies", "tags": "", "state": "uploading", "size": 4, "total_size": 4, "completed": 4, "progress": 1, "ratio": 1}
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
				switch r.URL.Path {
				case "/api/v2/auth/login":
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-session", Path: "/"})
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					h := r.URL.Query().Get("hashes")
					if h == "" {
						reply([]any{row(first), row(second), row(good)})
						return
					}
					if h == first && !strings.HasPrefix(mode, "unknown_") {
						if mode == "known_ambiguous" {
							reply([]any{row(first), row(first)})
							return
						}
						if mode == "known_read_transport" {
							conn, _, _ := w.(http.Hijacker).Hijack()
							_ = conn.Close()
							return
						}
						reply([]any{})
						return
					}
					reply([]any{row(h)})
				case "/api/v2/torrents/trackers":
					status, msg := 4, "torrent not registered with this tracker"
					if r.URL.Query().Get("hash") == good {
						status, msg = 2, ""
					}
					reply([]any{map[string]any{"url": "https://tracker.fixture/announce?passkey=synthetic-private", "status": status, "msg": msg, "tier": 0}})
				case "/api/v2/torrents/delete":
					_ = r.ParseForm()
					if r.Form.Get("deleteFiles") != "false" {
						t.Error("owned data deletion refused")
						http.Error(w, "forbidden", 400)
						return
					}
					h := r.Form.Get("hashes")
					if h == first {
						firstWrites.Add(1)
						if mode == "unknown_post500" {
							http.Error(w, "synthetic lost ACK", 500)
							return
						}
						if mode == "unknown_disconnect" {
							conn, _, _ := w.(http.Hijacker).Hijack()
							_ = conn.Close()
							return
						}
					} else if h == second {
						secondWrites.Add(1)
					} else {
						t.Error("unexpected deletion selector")
					}
					io.WriteString(w, "Ok.")
				default:
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			a.config = Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: s.URL, Username: "owned", Password: "synthetic-private"}, Name: "Owned qB"}}}
			sealedSettings, err := a.Store.Seal(a.Settings())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealedSettings, now()); err != nil {
				t.Fatal(err)
			}
			initial := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), cleanSeedOwnerConfig(map[string]any{"delete_invalid_torrents": true, "cron": "", "notify": true, "notify_all": true, "exclude_categories": "not-owned", "exclude_labels": "not-owned"}), true)
			if got := cleanSeedRuntimeExecute(t, a, initial); got.State != "succeeded" {
				t.Fatal("actual compiled SDK init", got.State, got.ErrorCode)
			}
			if _, err = a.pluginStore.EnqueueRun(context.Background(), initial.InstanceID, "run", nil); err != nil {
				t.Fatal(err)
			}
			run := cleanSeedOwnerClaim(t, a, initial.InstanceID)
			j := a.pluginCleanInvalidSeed.owners[initial.InstanceID].j
			if mode == "known_receipt_sql_failure" {
				token := cleanSeedReceiptToken(j, run.ID+":native:1")
				if _, err = a.Store.DB.Exec(fmt.Sprintf(`CREATE TRIGGER fail_known_rejection_receipt BEFORE INSERT ON plugin_clean_seed_core_receipts WHEN NEW.token='%s' BEGIN SELECT RAISE(ABORT,'synthetic known rejection receipt failure'); END`, token)); err != nil {
					t.Fatal(err)
				}
			}
			got := cleanSeedRuntimeExecute(t, a, run)
			unknown := strings.HasPrefix(mode, "unknown_") || mode == "known_receipt_sql_failure"
			wantState, wantCode := "failed", "CLEAN_SEED_HOST_DENIED"
			if unknown {
				wantState, wantCode = "reconcile_pending", "PLUGIN_DEFERRED_UNCERTAIN"
			}
			if got.State != wantState || got.ErrorCode != wantCode {
				t.Fatal("actual SDK/deferred terminal boundary", got.State, got.ErrorCode, wantState, wantCode)
			}
			wantFirst := int32(0)
			if strings.HasPrefix(mode, "unknown_") {
				wantFirst = 1
			}
			if firstWrites.Load() != wantFirst || secondWrites.Load() != 1 {
				t.Fatal("known rejection changed source continuation or replayed a write", firstWrites.Load(), secondWrites.Load())
			}
			state, err := j.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var sourceResult struct {
				SourceError string `json:"source_error"`
				HostError   string `json:"host_error"`
			}
			if json.Unmarshal(state.Runs[run.ID].Result, &sourceResult) != nil || sourceResult.SourceError != "" {
				t.Fatal("fixture introduced an unrelated source exception", sourceResult.SourceError)
			}
			wantHost := "HOST_ACTION_DENIED"
			if unknown {
				wantHost = "HOST_ACTION_UNCONFIRMED"
			}
			if sourceResult.HostError != wantHost {
				t.Fatal("private result mislabelled exact known/unknown outcome", sourceResult.HostError, wantHost)
			}
			inv, err := cleanSeedInvocation(got)
			if err != nil {
				t.Fatal(err)
			}
			firstEffect, ok := state.Effects[run.ID+":native:1"]
			if !ok {
				t.Fatal("actual native intent missing")
			}
			tx, err := a.Store.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			receiptErr := cleanSeedReadEffectReceipt(context.Background(), tx, j, inv, firstEffect)
			_ = tx.Rollback()
			if !unknown {
				if firstEffect.Phase != "confirmed" || firstEffect.ErrorCode != "DOWNLOADER_MUTATION_NOT_STARTED" || string(firstEffect.Result) != `{"protocol_not_started":true}` || receiptErr != nil {
					t.Fatal("known local rejection not confirmed with exact Core receipt", firstEffect.Phase, receiptErr)
				}
			} else if receiptErr == nil {
				t.Fatal("unknown mutation acquired a fake completed receipt")
			}
			if mode == "known_receipt_sql_failure" && firstEffect.Phase != "started" {
				t.Fatal("receipt SQL failure escaped atomic rollback", firstEffect.Phase)
			}
			if strings.HasPrefix(mode, "unknown_") && firstEffect.Phase != "unknown" {
				t.Fatal("issued write error was not unknown", firstEffect.Phase)
			}
			// The original downloader wrappers swallow returned failure and the
			// plugin counts attempted rows. Preserve that source notice, but keep it
			// separate from the precise actual write/known-rejection receipts.
			sawOriginalCount := false
			for _, n := range state.Notices {
				var payload map[string]any
				_ = json.Unmarshal(n.Payload, &payload)
				text, _ := payload["text"].(string)
				if strings.Contains(text, "已删除 2 个失效种子") {
					sawOriginalCount = true
				}
			}
			if !sawOriginalCount {
				t.Fatal("source continuation/count notice was silently rewritten")
			}
			if mode == "known_reply_loss" {
				record, previous, err := a.pluginStore.readDeferred(context.Background(), a.Store.DB, got)
				if err != nil {
					t.Fatal(err)
				}
				record.Phase, record.ErrorCode, record.Result = "started", "", nil
				tx, err := a.Store.DB.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.pluginStore.writeDeferred(context.Background(), tx, record, previous); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if _, err = tx.Exec(`UPDATE plugin_runs SET state='reconcile_pending',error_code='PLUGIN_DEFERRED_UNCERTAIN',result=NULL WHERE id=?`, run.ID); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			data := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: data})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err = reopened.initializeNativePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, err := reopened.pluginStore.Run(context.Background(), run.ID)
			if err != nil || after.State != wantState || after.ErrorCode != wantCode {
				t.Fatal("cold recovery changed known/unknown outcome", after.State, after.ErrorCode, err)
			}
			record, _, err := reopened.pluginStore.readDeferred(context.Background(), reopened.Store.DB, after)
			if err != nil {
				t.Fatal(err)
			}
			phase := "terminal_failed"
			if unknown {
				phase = "uncertain"
			}
			if record.Phase != phase {
				t.Fatal("private deferred receipt not recovered", record.Phase, phase)
			}
			if _, err = reopened.retryPluginDeferred(context.Background(), initial.InstanceID, run.ID); err == nil {
				t.Fatal("completed/uncertain mutation was eligible for retry")
			}
			if firstWrites.Load() != wantFirst || secondWrites.Load() != 1 {
				t.Fatal("cold recovery contacted mutation again")
			}
			var pending int
			if err = reopened.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state='pending'`, initial.InstanceID).Scan(&pending); err != nil || pending != 0 {
				t.Fatal("cold recovery enqueued replacement source action", pending, err)
			}
			if mode == "known_receipt_sql_failure" {
				var missing int
				err = reopened.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_clean_seed_core_receipts WHERE token=?`, cleanSeedReceiptToken(j, run.ID+":native:1")).Scan(&missing)
				if err != nil || missing != 0 {
					t.Fatal("failed same-tx rejection receipt materialized after reopen", missing, err)
				}
			}
			if !unknown {
				result, code, found, err := reopened.pluginStore.cleanInvalidSeedDeferredOutcome(context.Background(), after, seed.ExecuteCapability)
				if err != nil || !found || code != "CLEAN_SEED_HOST_DENIED" || len(result) == 0 {
					t.Fatal("actual private historical proof missing", found, code, err)
				}
				// A known, fully receipted failure must permit an explicit corrected
				// configuration revision, without retrying the old source action.
				useBuiltInPluginProcess(t, reopened)
				cookie := setupTestAdmin(t, reopened)
				config, err := reopened.pluginStore.Config(context.Background(), initial.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(map[string]any{"revision": config.Revision, "config": map[string]any{"enabled": false, "downloaders": []string{"Owned qB"}}})
				w := callTest(t, reopened.Handler(), "PUT", "/api/plugins/CleanInvalidSeed/config", string(body), cookie, "")
				if w.Code != 200 {
					t.Fatal("known rejection left normal config permanently blocked", w.Code, w.Body.String())
				}
				fresh := cleanSeedRuntimeExecute(t, reopened, cleanSeedOwnerClaim(t, reopened, initial.InstanceID))
				if fresh.State != "succeeded" || fresh.Action != "init" || fresh.ConfigRevision != config.Revision+1 {
					t.Fatal("explicit new revision not usable", fresh.State, fresh.ErrorCode)
				}
				original, err := reopened.pluginStore.Run(context.Background(), run.ID)
				if err != nil || original.State != "failed" || original.ConfigRevision != run.ConfigRevision || firstWrites.Load() != wantFirst || secondWrites.Load() != 1 {
					t.Fatal("new configuration replayed or rewrote old mutation", original.State, err)
				}
			}
		})
	}
}
