//go:build linux

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// Run the existing actual SDK subprocess fixture, but require lifecycle failure.
// The old successful-owner helper deliberately cannot exercise this branch.
func removeLinkInitFailedWorker(t *testing.T, a *App, run PluginRun, codes ...string) PluginRun {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginProcess = make(chan struct{}, 1)
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestPluginRemoveLinkOwnerWorkerFixture$", "--", "--remove-link-owner-worker"}}, nil
	}
	a.executePluginRun(context.Background(), run)
	got, err := a.pluginStore.Run(context.Background(), run.ID)
	code := "PLUGIN_EXECUTION_FAILED"
	if len(codes) > 0 {
		code = codes[0]
	}
	if err != nil || got.State != "reconcile_pending" || got.ErrorCode != code {
		t.Fatalf("actual SDK failed configure state=%s code=%s err=%v", got.State, got.ErrorCode, err)
	}
	if len(a.pluginProcess) != 0 {
		t.Fatal("failed SDK lifecycle leaked process slot")
	}
	return got
}

func removeLinkInitLocalNotices(t *testing.T, a *App) []Notification {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT payload FROM records WHERE kind='notification' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var raw []byte
		var n Notification
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The source systemmessage helper uses one ephemeral plugin/system FIFO.
func removeLinkInitSystemMessage(t *testing.T, a *App, want *Notification) {
	t.Helper()
	raw := a.systemMessages.get("system") // role is deliberately ignored by source get.
	if want == nil {
		if len(raw) != 0 {
			t.Fatalf("unexpected live diagnostic: %s", raw)
		}
		return
	}
	var got systemMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("missing/invalid live diagnostic %s: %v", raw, err)
	}
	if got.Type != "plugin" || got.Title == nil || *got.Title != want.Title || got.Text != want.Message || string(got.Note) != "null" {
		t.Fatalf("source systemmessage mismatch: %s", raw)
	}
	if _, err := time.Parse("2006-01-02 15:04:05", got.Date); err != nil {
		t.Fatal(err)
	}
	if extra := a.systemMessages.get("plugin"); len(extra) != 0 {
		t.Fatalf("duplicate live diagnostic: %s", extra)
	}
}

// Only test-owned SQLite and media are used. Returning a closed directory FD
// injects an actual add-watch syscall error, not real inotify resource exhaustion.
func TestRemoveLinkInitDiagnosticActualSDKFailureStaysBlocked(t *testing.T) {
	for _, action := range []string{"init", "reload"} {
		for _, mode := range []string{"hardlink", "strm"} {
			t.Run(action+"_"+mode, func(t *testing.T) {
				ctx := context.Background()
				a, item := historyRemovalFixture(t)
				source, media := a.Settings().AllowedRoots[0], a.Settings().Roots[0].Path
				cfg := map[string]any{"enabled": true, "monitor_dirs": source + "\n" + media, "notify": false, "delete_history": true, "delete_torrents": true, "delete_scrap_infos": true, "delayed_deletion": false}
				if mode == "strm" {
					cfg["monitor_dirs"] = source
					cfg["monitor_strm_deletion"] = true
					cfg["strm_path_mappings"] = media + ":" + source
				}
				var inject atomic.Bool
				inject.Store(action == "init")
				var captured *cleanuphost.WatchInitFailure
				var failedSpec removelink.Watch
				var attempts atomic.Int32
				a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
					attempts.Add(1)
					if !inject.Load() {
						return &cleanupEffectsIdleWatcher{events: make(chan cleanuphost.Event), errs: make(chan error)}, nil
					}
					if len(specs) < 2 {
						t.Fatalf("fixture needs separate first and failed spec: %v", specs)
					}
					failedSpec = specs[1]
					w, err := cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
						f, e := open(path)
						if e == nil && path == failedSpec.Path {
							e = f.Close()
						}
						return f, e
					})
					if !errors.As(err, &captured) {
						t.Errorf("injected native syscall did not yield typed provenance: %v", err)
					}
					return w, err
				}
				var sends atomic.Int32
				a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
				run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
				if action == "reload" {
					removeLinkOwnerWorker(t, a, run)
					inject.Store(true)
					if _, err := removeLinkOwnerHTTP(t, a, run.InstanceID, "reload", map[string]any{}); err != nil {
						t.Fatal(err)
					}
					run = removeLinkOwnerClaim(t, a, run.InstanceID)
				}
				beforeSource, err := os.ReadFile(item.Source)
				if err != nil {
					t.Fatal(err)
				}
				beforeTarget, err := os.ReadFile(item.Target)
				if err != nil {
					t.Fatal(err)
				}
				beforeHistory, err := a.transferHistoryItem(ctx, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				removeLinkInitFailedWorker(t, a, run)
				if captured == nil || !captured.Valid() || captured.Spec() != failedSpec || failedSpec.Mode != mode {
					t.Fatalf("wrong native failure attribution: %v spec=%v", captured, failedSpec)
				}
				notices := removeLinkInitLocalNotices(t, a)
				if len(notices) != 1 {
					t.Fatalf("want exactly one local diagnostic, got %#v", notices)
				}
				prefix := failedSpec.Path + " 启动硬链接监控失败："
				if mode == "strm" {
					prefix = failedSpec.Path + " 启动 STRM 监控失败："
				}
				if notices[0].Title != "媒体文件清理" || !strings.HasPrefix(notices[0].Message, prefix) || !strings.HasSuffix(notices[0].Message, captured.Unwrap().Error()) {
					t.Fatalf("source title/prefix/cause mismatch: %#v failure=%v", notices[0], captured)
				}
				// Exercise the authenticated real HTTP/SSE path, not a mock UI queue.
				cookie := setupTestAdmin(t, a)
				response, reader := openSystemStream(t, a, cookie, "/api/system/message?role=system")
				var message systemMessage
				raw := readSystemFrame(t, reader)
				response.Body.Close()
				if err := json.Unmarshal([]byte(raw), &message); err != nil || message.Type != "plugin" || message.Title == nil || *message.Title != notices[0].Title || message.Text != notices[0].Message || string(message.Note) != "null" {
					t.Fatalf("live SSE diagnostic mismatch: %s (%v)", raw, err)
				}
				removeLinkInitSystemMessage(t, a, nil)
				if notices[0].MType != "系统" || notices[0].Link != "" {
					t.Fatal("systemmessage was converted into a routed source notification", notices[0])
				}
				o := a.pluginRemoveLink.owners[run.InstanceID]
				if o == nil || o.h == nil {
					t.Fatal("missing failed owner")
				}
				before, err := o.j.Read(ctx)
				if err != nil || before.Blocked == "" || before.Active == nil || before.Active.Kind != "configure" {
					t.Fatalf("diagnostic cleared failed checkpoint: %#v %v", before, err)
				}
				diagnostics := 0
				for _, x := range before.Effects {
					if x.Kind == "watch_init_diagnostic" {
						diagnostics++
					}
				}
				if diagnostics != 1 {
					t.Fatalf("want one sealed diagnostic effect, got %d", diagnostics)
				}
				var proof removeLinkInitDiagnostic
				for _, x := range before.Effects {
					if x.Kind == "watch_init_diagnostic" {
						if err := json.Unmarshal(x.Payload, &proof); err != nil {
							t.Fatal(err)
						}
					}
				}
				if proof.RunID != run.ID || proof.Binding != before.Binding || proof.Scope != before.Active.ID || proof.Ordinal != 1 || proof.Path != failedSpec.Path || proof.Mode != mode {
					t.Fatalf("receipt lost actual lifecycle attribution: %#v", proof)
				}
				if err := o.validateInitDiagnosticState(before, proof, run.Action); err != nil {
					t.Fatal("real receipt rejected", err)
				}
				for _, mutate := range []func(*removeLinkInitDiagnostic){func(p *removeLinkInitDiagnostic) { p.Binding.InstanceID = "other-clone" }, func(p *removeLinkInitDiagnostic) { p.Binding.Generation++ }, func(p *removeLinkInitDiagnostic) { p.Binding.Revision++ }, func(p *removeLinkInitDiagnostic) { p.RunID = "other-run" }, func(p *removeLinkInitDiagnostic) { p.Scope = "other-scope" }} {
					badProof := proof
					mutate(&badProof)
					if err := o.validateInitDiagnosticState(before, badProof, run.Action); err == nil {
						t.Fatalf("cross-scope diagnostic proof accepted: %#v", badProof)
					}
				}
				tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := readRemoveLinkInitNotice(ctx, tx, proof.Notice); err != nil {
					tx.Rollback()
					t.Fatal("cold local notice proof", err)
				}
				tx.Rollback()
				// A detached retry, including a copied owner context, must not
				// borrow the ended configure lease or create another notice.
				detached := removeLinkOwnerContext(ctx, o, &run)
				if err := o.watchInitDiagnostic(detached, proof.Binding, proof.Scope, captured); err == nil {
					t.Fatal("expired configure lease authorized replay")
				}
				if err := o.watchInitDiagnostic(detached, proof.Binding, proof.Scope, &cleanuphost.WatchInitFailure{}); err == nil {
					t.Fatal("worker-shaped zero proof authorized diagnostic")
				}

				calls := attempts.Load()
				_ = o.h.Observe(ctx, cleanuphost.Event{ID: "owned-post-init-failure", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()})
				_ = o.h.Tick(ctx)
				removeLinkNoticeDrain(t, a)
				after, err := o.j.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if after.Blocked == "" || after.Active == nil || after.Active.ID != before.Active.ID {
					t.Fatal("later event/tick unblocked initialization")
				}
				if attempts.Load() != calls {
					t.Fatal("diagnostic processing restarted watcher")
				}
				for path, want := range map[string][]byte{item.Source: beforeSource, item.Target: beforeTarget} {
					got, e := os.ReadFile(path)
					if e != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("failed init changed owned media %s: %v", path, e)
					}
				}
				history, err := a.transferHistoryItem(ctx, item.ID)
				if err != nil || !reflect.DeepEqual(history, beforeHistory) {
					t.Fatal("failed init changed transfer history", err)
				}
				if len(removeLinkInitLocalNotices(t, a)) != 1 || sends.Load() != 0 {
					t.Fatal("diagnostic replay duplicated or externally sent")
				}
				removeLinkInitSystemMessage(t, a, nil)
				for _, table := range []string{"plugin_notice_routes", "plugin_message_queue", "plugin_cleanup_private_outbox"} {
					if n := cleanupEffectsCount(t, a, table); n != 0 {
						t.Fatalf("local diagnostic created %s rows: %d", table, n)
					}
				}
			})
		}
	}
}

func TestRemoveLinkInitDiagnosticRejectsOrdinaryFactoryError(t *testing.T) {
	a, _ := historyRemovalFixture(t)
	a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return nil, errors.New("synthetic inotify limit reached; not a native typed failure")
	}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0]}, true)
	removeLinkInitFailedWorker(t, a, run)
	if got := removeLinkInitLocalNotices(t, a); len(got) != 0 {
		t.Fatalf("ordinary error acquired trusted source diagnostic: %#v", got)
	}
	removeLinkInitSystemMessage(t, a, nil)
}

func TestRemoveLinkInitDiagnosticSQLFailureRollsBackNoticeAndReceipt(t *testing.T) {
	for _, failAt := range []string{"notice", "sealed_receipt", "core_receipt"} {
		t.Run(failAt, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			statement := `CREATE TRIGGER owned_init_notice_failure BEFORE INSERT ON records WHEN NEW.kind='notification' BEGIN SELECT RAISE(ABORT,'owned init notice failure'); END`
			if failAt == "sealed_receipt" {
				statement = `CREATE TRIGGER owned_init_receipt_failure BEFORE INSERT ON plugin_cleanup_journal_rows WHEN EXISTS(SELECT 1 FROM records WHERE kind='notification' AND id LIKE 'cleanup_init_notice_%') BEGIN SELECT RAISE(ABORT,'owned init receipt failure'); END`
			}
			if failAt == "core_receipt" {
				statement = `CREATE TRIGGER owned_init_core_failure BEFORE INSERT ON plugin_cleanup_core_receipts BEGIN SELECT RAISE(ABORT,'owned init core failure'); END`
			}
			a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
				if _, err := a.Store.DB.Exec(statement); err != nil {
					t.Fatal(err)
				}
				return cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
					f, e := open(path)
					if e == nil {
						e = f.Close()
					}
					return f, e
				})
			}
			run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0]}, true)
			removeLinkInitFailedWorker(t, a, run)
			if got := removeLinkInitLocalNotices(t, a); len(got) != 0 {
				t.Fatalf("failed SQL left unreceipted public notice: %#v", got)
			}
			removeLinkInitSystemMessage(t, a, nil)
			if n := cleanupEffectsCount(t, a, "plugin_cleanup_core_receipts"); n != 0 {
				t.Fatal("failed SQL left core receipt", n)
			}
			o := a.pluginRemoveLink.owners[run.InstanceID]
			if o == nil {
				t.Fatal("missing blocked owner")
			}
			state, err := o.j.Read(context.Background())
			if err != nil || state.Blocked == "" || state.Active == nil {
				t.Fatal("SQL fault lost blocked checkpoint", err)
			}
			for _, x := range state.Effects {
				if x.Kind == "watch_init_diagnostic" {
					t.Fatal("rolled back local notice retained receipt")
				}
			}
			for _, path := range []string{item.Source, item.Target} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("SQL diagnostic failure touched media", err)
				}
			}
			if _, err := a.transferHistoryItem(context.Background(), item.ID); err != nil {
				t.Fatal("SQL diagnostic failure removed history", err)
			}
		})
	}
}

func TestRemoveLinkInitDiagnosticLostCommitACKReadsExactSealedReceipt(t *testing.T) {
	for _, outcome := range []string{"committed_reply_lost", "rolled_back", "committed_receipt_corrupt"} {
		t.Run(outcome, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			var commits, watches atomic.Int32
			a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
				watches.Add(1)
				o := a.pluginRemoveLink.owners["RemoveLink"]
				o.initDiagnosticCommit = func(tx *sql.Tx) error {
					commits.Add(1)
					removeLinkInitSystemMessage(t, a, nil) // Nothing visible before commit.
					if outcome == "rolled_back" {
						return errors.New("owned diagnostic commit rejected")
					}
					if err := tx.Commit(); err != nil {
						return err
					}
					if outcome == "committed_receipt_corrupt" {
						if _, err := a.Store.DB.Exec(`UPDATE plugin_cleanup_core_receipts SET sealed=x'00'`); err != nil {
							t.Error(err)
						}
					}
					return errors.New("owned lost diagnostic commit ACK")
				}
				return cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
					f, e := open(path)
					if e == nil {
						e = f.Close()
					}
					return f, e
				})
			}
			run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0]}, true)
			code := "PLUGIN_EXECUTION_FAILED"
			if outcome == "committed_receipt_corrupt" {
				code = "PLUGIN_DATA_CORRUPT"
			}
			got := removeLinkInitFailedWorker(t, a, run, code)
			if commits.Load() != 1 || watches.Load() != 1 {
				t.Fatal("unknown ACK retried a write or observer", commits.Load(), watches.Load())
			}
			o := a.pluginRemoveLink.owners[run.InstanceID]
			state, err := o.j.Read(context.Background())
			if err != nil || state.Blocked == "" || state.Active == nil {
				t.Fatal("ACK handling cleared failed source state", err)
			}
			want := 1
			if outcome == "rolled_back" {
				want = 0
			}
			if n := len(removeLinkInitLocalNotices(t, a)); n != want {
				t.Fatalf("atomic notice outcome=%s got=%d want=%d", outcome, n, want)
			}
			if n := cleanupEffectsCount(t, a, "plugin_cleanup_core_receipts"); n != want {
				t.Fatalf("atomic receipt outcome=%s got=%d want=%d", outcome, n, want)
			}
			if outcome == "committed_reply_lost" {
				notices := removeLinkInitLocalNotices(t, a)
				removeLinkInitSystemMessage(t, a, &notices[0])
			} else {
				removeLinkInitSystemMessage(t, a, nil)
			}
			if outcome == "committed_reply_lost" {
				for _, fact := range state.Effects {
					if fact.Kind != "watch_init_diagnostic" {
						continue
					}
					var proof removeLinkInitDiagnostic
					if err = json.Unmarshal(fact.Payload, &proof); err != nil {
						t.Fatal(err)
					}
					tx, e := a.Store.DB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
					if e != nil {
						t.Fatal(e)
					}
					if e = o.readInitDiagnosticReceipt(context.Background(), tx, fact, proof.Notice); e != nil {
						tx.Rollback()
						t.Fatal("cold sealed receipt unreadable", e)
					}
					wrong := proof.Notice
					wrong.ID += "-other"
					if e = o.readInitDiagnosticReceipt(context.Background(), tx, fact, wrong); e == nil {
						t.Fatal("receipt accepted a different notice identity")
					}
					tx.Rollback()
				}
			}
			if got.State != "reconcile_pending" {
				t.Fatal("diagnostic ACK recovery turned failed configure into success")
			}
			_ = o.h.Tick(context.Background())
			removeLinkNoticeDrain(t, a)
			if commits.Load() != 1 || watches.Load() != 1 || len(removeLinkInitLocalNotices(t, a)) != want {
				t.Fatal("post-failure tick/drain duplicated unknown outcome")
			}
			removeLinkInitSystemMessage(t, a, nil)
			for _, path := range []string{item.Source, item.Target} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("ACK recovery touched media", err)
				}
			}
		})
	}
}

func TestRemoveLinkInitDiagnosticBlockedCheckpointFailureDoesNotPublish(t *testing.T) {
	a, _ := historyRemovalFixture(t)
	a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		w, err := cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
			f, e := open(path)
			if e == nil {
				e = f.Close()
			}
			return f, e
		})
		if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_init_block_failure BEFORE INSERT ON plugin_cleanup_journal_meta BEGIN SELECT RAISE(ABORT,'owned blocked checkpoint failure'); END`); e != nil {
			t.Fatal(e)
		}
		return w, err
	}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0]}, true)
	removeLinkInitFailedWorker(t, a, run)
	if len(removeLinkInitLocalNotices(t, a)) != 0 || cleanupEffectsCount(t, a, "plugin_cleanup_core_receipts") != 0 {
		t.Fatal("uncommitted Blocked checkpoint published a diagnostic")
	}
	removeLinkInitSystemMessage(t, a, nil)
	o := a.pluginRemoveLink.owners[run.InstanceID]
	s, err := o.j.Read(context.Background())
	if err != nil || s.Active == nil {
		t.Fatal("blocked checkpoint fault lost prior active configure", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_init_block_failure`); err != nil {
		t.Fatal(err)
	}
	live, err := o.h.Snapshot(context.Background())
	if err != nil || live.Blocked == "" {
		t.Fatal("blocked checkpoint failure kept observer runnable", err)
	}
}

func TestRemoveLinkInitDiagnosticCloneAndColdReceiptIsolation(t *testing.T) {
	a, _ := historyRemovalFixture(t)
	a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
			f, e := open(path)
			if e == nil {
				e = f.Close()
			}
			return f, e
		})
	}
	ctx := context.Background()
	var owners []*pluginRemoveLinkOwner
	var facts []cleanuphost.Effect
	for _, instance := range []string{"RemoveLink", "RemoveLinkClone"} {
		run := uninstallInstall(t, a, instance, removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0]}, true)
		removeLinkInitFailedWorker(t, a, run)
		o := a.pluginRemoveLink.owners[instance]
		owners = append(owners, o)
		cold, err := newPluginRemoveLinkJournal(ctx, a.pluginStore, instance, run.Generation)
		if err != nil {
			t.Fatal(err)
		}
		s, err := cold.Read(ctx)
		if err != nil || s.Blocked == "" || s.Active == nil {
			t.Fatal("cold journal lost failed configure", err)
		}
		for _, x := range s.Effects {
			if x.Kind == "watch_init_diagnostic" {
				facts = append(facts, x)
			}
		}
	}
	if len(facts) != 2 || facts[0].ID == facts[1].ID || len(removeLinkInitLocalNotices(t, a)) != 2 {
		t.Fatal("clone failure identity collided")
	}
	notices := removeLinkInitLocalNotices(t, a)
	if notices[0].Message != notices[1].Message {
		t.Fatal("fixture diagnostics should share original dedup identity")
	}
	// Durable facts stay instance-isolated, but source MessageHelper dedups the
	// same title/text/role/note across instances in its shared live FIFO.
	removeLinkInitSystemMessage(t, a, &notices[0])
	for i, o := range owners {
		var p removeLinkInitDiagnostic
		if err := json.Unmarshal(facts[i].Payload, &p); err != nil {
			t.Fatal(err)
		}
		tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = o.readInitDiagnosticReceipt(ctx, tx, facts[i], p.Notice); err != nil {
			tx.Rollback()
			t.Fatal("own cold receipt rejected", err)
		}
		if err = owners[1-i].readInitDiagnosticReceipt(ctx, tx, facts[i], p.Notice); err == nil {
			tx.Rollback()
			t.Fatal("other clone accepted receipt")
		}
		forged := facts[i]
		forged.Binding.Generation++
		if err = o.readInitDiagnosticReceipt(ctx, tx, forged, p.Notice); err == nil {
			tx.Rollback()
			t.Fatal("different generation accepted receipt")
		}
		forged = facts[i]
		forged.Binding.Revision++
		if err = o.readInitDiagnosticReceipt(ctx, tx, forged, p.Notice); err == nil {
			tx.Rollback()
			t.Fatal("different revision accepted receipt")
		}
		tx.Rollback()
	}
}

func TestRemoveLinkInitDiagnosticColdOwnerDoesNotRestartOrReplay(t *testing.T) {
	for _, retryAction := range []string{"init", "reload"} {
		t.Run(retryAction, func(t *testing.T) {
			ctx := context.Background()
			a, item := historyRemovalFixture(t)
			a.pluginRemoveLink.watch = func(ctx context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
				return cleanuphost.NativeWatch(ctx, specs, func(path string) (*os.File, error) {
					f, e := open(path)
					if e == nil {
						e = f.Close()
					}
					return f, e
				})
			}
			run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0], "delete_history": true, "delayed_deletion": false}, true)
			removeLinkInitFailedWorker(t, a, run)
			removeLinkOwnerPersistSettings(t, a)
			before, err := a.pluginRemoveLink.owners[run.InstanceID].j.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			notices := removeLinkInitLocalNotices(t, a)
			if len(notices) != 1 {
				t.Fatal("missing original source diagnostic")
			}
			removeLinkInitSystemMessage(t, a, &notices[0])
			source, err := os.ReadFile(item.Source)
			if err != nil {
				t.Fatal(err)
			}
			target, err := os.ReadFile(item.Target)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(Options{DataDir: a.Dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			removeLinkInitSystemMessage(t, reopened, nil)
			var watches, sends atomic.Int32
			reopened.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
				watches.Add(1)
				return nil, errors.New("blocked cold owner must not reach observer")
			}
			reopened.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			recovered, err := reopened.pluginStore.Run(ctx, run.ID)
			if err != nil || recovered.State != "reconcile_pending" {
				t.Fatal("cold diagnostic falsely repaired lifecycle run", recovered.State, err)
			}
			if err = reopened.restoreRemoveLinkOwners(ctx); !errors.Is(err, cleanuphost.ErrUncertain) {
				t.Fatal("blocked cold owner must refuse opening", err)
			}
			if len(reopened.pluginRemoveLink.owners) != 0 {
				t.Fatal("blocked cold recovery retained a runnable owner")
			}
			cold, err := newPluginRemoveLinkJournal(ctx, reopened.pluginStore, run.InstanceID, run.Generation)
			if err != nil {
				t.Fatal(err)
			}
			after, err := cold.Read(ctx)
			if err != nil || after.Blocked == "" || after.Active == nil || after.Active.ID != before.Active.ID {
				t.Fatal("cold recovery lost blocked active scope", err)
			}
			i, c, err := reopened.removeLinkSnapshot(ctx, run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			initialize, err := reopened.shouldInitializeRemoveLink(ctx, i, c)
			if err != nil || initialize {
				t.Fatal("blocked cold recovery scheduled fresh initialization", err)
			}
			// Queueing is not execution authority: a fresh SDK invocation
			// reaches the restored blocked-owner check and still fails closed.
			queued, err := reopened.pluginStore.EnqueueRun(ctx, run.InstanceID, retryAction, nil)
			if err != nil {
				t.Fatal("queue retry", err)
			}
			retry := removeLinkOwnerClaim(t, reopened, run.InstanceID)
			if retry.ID != queued.ID || retry.ID == run.ID || retry.Action != retryAction {
				t.Fatal("cold retry claimed the original reconciliation-pending run", retry)
			}
			removeLinkInitFailedWorker(t, reopened, retry)
			phase, _, err := reopened.pluginStore.readDeferred(ctx, reopened.Store.DB, retry)
			if err != nil || phase.Phase != "uncertain" {
				t.Fatal("SDK blocked retry lost deferred uncertainty", phase.Phase, err)
			}
			original, err := reopened.pluginStore.Run(ctx, run.ID)
			if err != nil || original.State != "reconcile_pending" {
				t.Fatal("retry falsely completed original configure", original.State, err)
			}
			if len(reopened.pluginRemoveLink.owners) != 0 {
				t.Fatal("SDK retry opened a blocked native owner")
			}
			if _, err = reopened.pluginStore.claimRunForInstance(ctx, run.InstanceID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("reconciliation-pending run became claimable", err)
			}
			removeLinkNoticeDrain(t, reopened)
			if watches.Load() != 0 || sends.Load() != 0 {
				t.Fatal("cold failure recovery reopened watch or sent externally", watches.Load(), sends.Load())
			}
			if got := removeLinkInitLocalNotices(t, reopened); !reflect.DeepEqual(got, notices) {
				t.Fatal("cold failure recovery duplicated/changed diagnostic", got)
			}
			removeLinkInitSystemMessage(t, reopened, nil)
			for path, want := range map[string][]byte{item.Source: source, item.Target: target} {
				got, e := os.ReadFile(path)
				if e != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("cold failure recovery changed media", path, e)
				}
			}
			if _, err = reopened.transferHistoryItem(ctx, item.ID); err != nil {
				t.Fatal("cold failure recovery removed history", err)
			}
		})
	}
}
