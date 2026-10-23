package app

// These integration tests use a real SDK-initialized native owner, real SQL,
// and only owned temporary media. The sender is in memory, not a live channel.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func removeLinkBarrierOwner(t *testing.T, scrap bool) (*App, transferHistoryItem, *pluginRemoveLinkOwner) {
	t.Helper()
	a, source, media := importTestApp(t)
	importFixture(t, a, "movie")
	input := filepath.Join(source, "owned-parent", "owned-season", "History (2024).mkv")
	importWrite(t, input, []byte("owned source barrier integration media"))
	// Never let the source's ancestor walk escape either owned authorization root.
	importWrite(t, filepath.Join(source, "owner.marker"), []byte("keep"))
	importWrite(t, filepath.Join(media, "owner.marker"), []byte("keep"))
	ctx := context.Background()
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job.State, job.Generation = "running", 1
	if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	removeLinkOwnerFakeWatch(a)
	cfg := map[string]any{"enabled": true, "monitor_dirs": source + "\n" + media, "notify": true, "delete_scrap_infos": scrap, "delete_history": true, "delete_torrents": false, "delayed_deletion": false}
	run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
	removeLinkOwnerWorker(t, a, run)
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	sourceConfig := removelink.NewConfig()
	if err = sourceConfig.Apply(stored.Value); err != nil || removelink.Truth(sourceConfig.Scrap) != scrap {
		t.Fatal("fixture did not enable the original delete_scrap_infos branch", err)
	}
	o := a.pluginRemoveLink.owners[run.InstanceID]
	if o == nil || o.h == nil {
		t.Fatal("SDK did not initialize native owner")
	}
	a.Store.DB.SetMaxOpenConns(1)
	return a, item, o
}
func removeLinkBarrierDelete(t *testing.T, item transferHistoryItem, o *pluginRemoveLinkOwner, ctx context.Context) error {
	t.Helper()
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	return o.h.Observe(ctx, cleanuphost.Event{ID: "owned-source-barrier-delete", Kind: "deleted", Mode: "hardlink", Path: item.Source, At: time.Now()})
}
func removeLinkBarrierNotices(t *testing.T, o *pluginRemoveLinkOwner) []cleanuphost.Effect {
	t.Helper()
	s, err := o.j.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []cleanuphost.Effect
	for _, x := range s.Effects {
		if x.Kind == "notification" {
			out = append(out, x)
		}
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].NoticeOrdinal < out[i].NoticeOrdinal {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
func removeLinkBarrierRoute(t *testing.T, o *pluginRemoveLinkOwner, x cleanuphost.Effect) pluginNoticeRoute {
	t.Helper()
	id := o.a.pluginStore.noticeID("route", o.instance, o.generation, o.e.token(x.ID))
	r, _, err := o.a.pluginStore.readNoticeRoute(context.Background(), o.a.Store.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRemoveLinkSourceBarrierActualOwnerAtSourceBoundary(t *testing.T) {
	for _, mode := range []string{"normal", "known_type_error", "queue_external_unknown", "queue_sender_panic", "source_finish_unknown"} {
		t.Run(mode, func(t *testing.T) {
			a, item, o := removeLinkBarrierOwner(t, true)
			if mode == "known_type_error" {
				noticePreferenceTest(t, a, "superuser", "", 17)
			}
			if mode == "source_finish_unknown" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_source_finish_fail BEFORE INSERT ON plugin_cleanup_source_results BEGIN SELECT RAISE(ABORT,'owned source finish failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			var sends atomic.Int32
			a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
				n := sends.Add(1)
				// A write, not merely a read, proves no owner SQL transaction is retained.
				if err := a.Store.Put(ctx, "owned_barrier_probe", q.ID, map[string]any{"send": n}); err != nil {
					return 0, err
				}
				if n == 1 {
					if _, err := os.Stat(filepath.Dir(item.Source)); !errors.Is(err, os.ErrNotExist) {
						t.Error("notification started before source directory effect", err)
					}
					if _, err := os.Stat(filepath.Dir(filepath.Dir(item.Source))); err != nil {
						t.Error("next ancestor removed before notification returned", err)
					}
					if _, err := os.Stat(item.Target); err != nil {
						t.Error("hardlink removed before first source notice returned", err)
					}
				}
				if mode == "queue_sender_panic" {
					panic("owned module panic")
				}
				if mode == "queue_external_unknown" {
					return 1, seedhost.ErrUnknown
				}
				return 1, nil
			}
			err := removeLinkBarrierDelete(t, item, o, context.Background())
			notices := removeLinkBarrierNotices(t, o)
			if len(notices) == 0 || notices[0].SourceBarrierVersion != 1 || notices[0].NoticeSite != "empty_directory" || notices[0].NoticeOrdinal != 1 {
				t.Fatal("first exact source site missing", notices, err)
			}
			if mode == "source_finish_unknown" {
				if err == nil {
					t.Fatal("unknown source return authorized continuation")
				}
				if len(notices) != 1 {
					t.Fatal("unknown first notice allowed later notices", len(notices))
				}
				for _, p := range []string{filepath.Dir(filepath.Dir(item.Source)), item.Target} {
					if _, e := os.Stat(p); e != nil {
						t.Fatal("unknown source return allowed later deletion", p, e)
					}
				}
				before := sends.Load()
				if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_source_finish_fail`); e != nil {
					t.Fatal(e)
				}
				removeLinkNoticeDrain(t, a)
				if sends.Load() != before {
					t.Fatal("cold route completion resent accepted queue")
				}
				if _, e := os.Stat(item.Target); e != nil {
					t.Fatal("cold route completion granted file authority", e)
				}
				return
			}
			if err != nil {
				t.Fatal("proven source return failed actual host", err)
			}
			if _, err = os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("source continuation never removed linked target", err)
			}
			for i, x := range notices {
				if x.SourceBarrierVersion != 1 || x.NoticeOrdinal != i+1 {
					t.Fatal("source ordinal gap", x)
				}
				r := removeLinkBarrierRoute(t, o, x)
				if !r.Complete {
					t.Fatal("source resumed before router terminal result")
				}
				if mode == "known_type_error" && r.SourceError != "TypeError" {
					t.Fatal("known exception class lost", r.SourceError)
				}
				proofErr := o.e.readRemoveLinkSourceResult(context.Background(), x)
				if mode == "known_type_error" {
					var known *removelink.SourceNoticeError
					if !errors.As(proofErr, &known) || known.Class != "TypeError" {
						t.Fatal("missing independent known exception", proofErr)
					}
				} else if proofErr != nil {
					t.Fatal(proofErr)
				}
			}
			before := sends.Load()
			removeLinkNoticeDrain(t, a)
			if sends.Load() != before {
				t.Fatal("terminal source replay resent notification")
			}
		})
	}
}

func TestRemoveLinkSourceBarrierSettingsWriteDuringSender(t *testing.T) {
	for _, change := range []string{"domain_only", "revoke_roots", "config_revision", "cancel"} {
		t.Run(change, func(t *testing.T) {
			a, item, o := removeLinkBarrierOwner(t, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var sends atomic.Int32
			a.pluginStore.notices.send = func(sendCtx context.Context, q pluginNoticeQueueEntry) (int, error) {
				if sends.Add(1) != 1 {
					return 1, nil
				}
				if err := a.Store.Put(sendCtx, "owned_sender_write", q.ID, map[string]bool{"written": true}); err != nil {
					return 0, err
				}
				switch change {
				case "domain_only", "revoke_roots":
					cfg := a.Settings()
					cfg.AppDomain = "https://owned-sender.invalid"
					if change == "revoke_roots" {
						cfg.AllowedRoots = nil
					}
					if err := a.saveSettings(sendCtx, cfg); err != nil {
						return 0, err
					}
				case "config_revision":
					cfg, err := a.pluginStore.Config(sendCtx, o.instance)
					if err != nil {
						return 0, err
					}
					if _, err = a.pluginStore.SaveConfig(sendCtx, o.instance, cfg.Revision, cfg.Value); err != nil {
						return 0, err
					}
				case "cancel":
					cancel()
				}
				return 1, nil
			}
			err := removeLinkBarrierDelete(t, item, o, ctx)
			if sends.Load() == 0 {
				t.Fatal("sender was not reached", err)
			}
			if change == "domain_only" {
				if err != nil {
					t.Fatal("non-authority Settings write blocked continuation", err)
				}
				if _, err = os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unchanged roots did not resume", err)
				}
			} else {
				if err == nil {
					t.Fatal("changed authority or canceled action resumed")
				}
				if _, err = os.Stat(item.Target); err != nil {
					t.Fatal("remaining hardlink deleted after authority changed", err)
				}
			}
		})
	}
}

func TestRemoveLinkSourceBarrierLostSQLAcknowledgements(t *testing.T) {
	for _, point := range []string{"remove_link_acceptance", "remove_link_handoff", "queue_intent", "queue_finish", "route_finish"} {
		t.Run(point, func(t *testing.T) {
			a, item, o := removeLinkBarrierOwner(t, false)
			var faults, sends atomic.Int32
			a.pluginStore.notices.afterCommit = func(op string) error {
				if op == point {
					faults.Add(1)
					return errors.New("owned lost committed acknowledgement")
				}
				return nil
			}
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			if err := removeLinkBarrierDelete(t, item, o, context.Background()); err != nil {
				t.Fatal("exact committed proof not recovered", err)
			}
			if faults.Load() == 0 {
				t.Fatal("requested real commit seam not exercised", point)
			}
			notices := removeLinkBarrierNotices(t, o)
			if len(notices) != 1 || notices[0].NoticeSite != "hardlink_immediate" {
				t.Fatal("unexpected source site", notices)
			}
			if err := o.e.readRemoveLinkSourceResult(context.Background(), notices[0]); err != nil {
				t.Fatal(err)
			}
			removeLinkNoticeDrain(t, a)
			if sends.Load() != 1 {
				t.Fatal("lost acknowledgement caused repeated external attempt", sends.Load())
			}
		})
	}
}

func TestRemoveLinkSourceBarrierIndependentTerminalProof(t *testing.T) {
	for _, field := range []string{"route_complete", "route_error", "route_count", "route_last_sha", "missing_result", "result_site", "result_ordinal", "result_generation", "result_instance", "result_route", "result_count", "result_last_sha"} {
		t.Run(field, func(t *testing.T) {
			a, item, o := removeLinkBarrierOwner(t, false)
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { return 1, nil }
			if err := removeLinkBarrierDelete(t, item, o, context.Background()); err != nil {
				t.Fatal(err)
			}
			notices := removeLinkBarrierNotices(t, o)
			if len(notices) != 1 {
				t.Fatal(notices)
			}
			x := notices[0]
			r := removeLinkBarrierRoute(t, o, x)
			if err := o.e.readRemoveLinkSourceResult(context.Background(), x); err != nil {
				t.Fatal("untampered proof", err)
			}
			var sealed []byte
			if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_source_results WHERE route_id=?`, r.ID).Scan(&sealed); err != nil {
				t.Fatal(err)
			}
			var result removeLinkSourceResult
			if err := a.pluginStore.noticeUnseal("remove_link_source_result", r.ID, sealed, &result); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "route_complete":
				r.Complete = false
			case "route_error":
				r.SourceError = "TypeError"
			case "route_count":
				r.Count++
			case "route_last_sha":
				r.LastSHA = tokenHash("owned altered chain")
			case "missing_result":
				if _, err := a.Store.DB.Exec(`DELETE FROM plugin_cleanup_source_results WHERE route_id=?`, r.ID); err != nil {
					t.Fatal(err)
				}
			case "result_site":
				result.Site = "strm"
			case "result_ordinal":
				result.Ordinal++
			case "result_generation":
				result.Generation++
			case "result_instance":
				result.Instance = "RemoveLink.owned-sibling"
			case "result_route":
				result.RouteID = "owned-different-route"
			case "result_count":
				result.Count++
			case "result_last_sha":
				result.LastSHA = tokenHash("owned altered result chain")
			}
			if field != "missing_result" {
				kind, table, key, value := "route", "plugin_notice_routes", "id", any(r)
				if len(field) > 7 && field[:7] == "result_" {
					kind, table, key, value = "remove_link_source_result", "plugin_cleanup_source_results", "route_id", result
				}
				altered, err := a.pluginStore.noticeSeal(kind, r.ID, value)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE `+table+` SET sealed=? WHERE `+key+`=?`, altered, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := o.e.readRemoveLinkSourceResult(context.Background(), x); err == nil {
				raw, _ := json.Marshal(result)
				t.Fatal("independent proof accepted altered terminal fact", field, string(raw))
			}
		})
	}
}

func TestRemoveLinkSourceBarrierTwoRealOwnerInstances(t *testing.T) {
	a, item, first := removeLinkBarrierOwner(t, false)
	ctx := context.Background()
	siblingSource := filepath.Join(a.Settings().AllowedRoots[0], "owned-sibling.mkv")
	siblingTarget := filepath.Join(a.Settings().Roots[0].Path, "owned-sibling.mkv")
	importWrite(t, siblingSource, []byte("independent sibling media"))
	if err := os.Link(siblingSource, siblingTarget); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.pluginStore.Config(ctx, first.instance)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err = json.Unmarshal(cfg.Value, &values); err != nil {
		t.Fatal(err)
	}
	run := uninstallInstall(t, a, "RemoveLink_owned_sibling", removelink.Manifest(), values, true)
	removeLinkOwnerWorker(t, a, run)
	second := a.pluginRemoveLink.owners[run.InstanceID]
	if second == nil || second == first || second.h == first.h {
		t.Fatal("clone borrowed original owner")
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if err = removeLinkBarrierDelete(t, item, first, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(siblingTarget); err != nil {
		t.Fatal("first owner touched sibling media", err)
	}
	siblingItem := transferHistoryItem{Source: siblingSource, Target: siblingTarget}
	if err = removeLinkBarrierDelete(t, siblingItem, second, ctx); err != nil {
		t.Fatal(err)
	}
	one, two := removeLinkBarrierNotices(t, first), removeLinkBarrierNotices(t, second)
	if len(one) != 1 || len(two) != 1 || sends.Load() != 2 {
		t.Fatal("owner source results were merged", len(one), len(two), sends.Load())
	}
	r1, r2 := removeLinkBarrierRoute(t, first, one[0]), removeLinkBarrierRoute(t, second, two[0])
	if r1.ID == r2.ID || r1.Instance == r2.Instance {
		t.Fatal("clone did not retain result identity")
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_cleanup_source_results WHERE route_id=?`, r1.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_cleanup_source_results SET sealed=? WHERE route_id=?`, sealed, r2.ID); err != nil {
		t.Fatal(err)
	}
	if err = second.e.readRemoveLinkSourceResult(ctx, two[0]); err == nil {
		t.Fatal("clone accepted another owner's terminal proof")
	}
	if err = first.e.readRemoveLinkSourceResult(ctx, one[0]); err != nil {
		t.Fatal("clone tamper damaged independent original", err)
	}
}

func TestRemoveLinkSourceBarrierConcurrentLifecycleDoesNotHoldSenderLocks(t *testing.T) {
	for _, operation := range []string{"reload", "uninstall", "close"} {
		t.Run(operation, func(t *testing.T) {
			a, item, o := removeLinkBarrierOwner(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			lifecycle := make(chan error, 1)
			var sends atomic.Int32
			a.pluginStore.notices.send = func(sendCtx context.Context, q pluginNoticeQueueEntry) (int, error) {
				if sends.Add(1) != 1 {
					return 1, nil
				}
				go func() {
					if operation == "close" {
						lifecycle <- a.closeRemoveLinkOwners(ctx)
						return
					}
					req := httptest.NewRequest("POST", "/api/plugins/"+o.instance+"/"+operation, strings.NewReader("{}")).WithContext(ctx)
					_, err := a.pluginRemoveLinkLifecycleHTTP(httptest.NewRecorder(), req, o.instance+"/"+operation)
					lifecycle <- err
				}()
				// The administrative gate is held before the lifecycle operation waits
				// for this serialized host action. It must not hold App write locks.
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for len(o.gate) == 0 {
					select {
					case <-ticker.C:
					case <-ctx.Done():
						return 0, ctx.Err()
					}
				}
				settings := a.Settings()
				settings.AppDomain = "https://concurrent-owned.invalid"
				if err := a.saveSettings(sendCtx, settings); err != nil {
					return 0, err
				}
				if err := a.Store.Put(sendCtx, "owned_concurrent_sender", q.ID, map[string]bool{"done": true}); err != nil {
					return 0, err
				}
				return 1, nil
			}
			if err := removeLinkBarrierDelete(t, item, o, ctx); err != nil {
				t.Fatal("host action failed while lifecycle waited", err)
			}
			select {
			case err := <-lifecycle:
				if err != nil {
					t.Fatal("concurrent lifecycle did not finish", err)
				}
			case <-ctx.Done():
				t.Fatal("lifecycle deadlocked with source barrier", ctx.Err())
			}
			if sends.Load() != 1 {
				t.Fatal("lifecycle replayed source notification", sends.Load())
			}
		})
	}
}

func TestRemoveLinkSourceBarrierUnknownPreservesCommittedHistoryPrefix(t *testing.T) {
	a, item, o := removeLinkBarrierOwner(t, false)
	ctx := context.Background()
	var audit []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='import_plan' AND id=?`, item.PlanID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_source_result_fail BEFORE INSERT ON plugin_cleanup_source_results BEGIN SELECT RAISE(ABORT,'owned result failure'); END`); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if err := removeLinkBarrierDelete(t, item, o, ctx); err == nil {
		t.Fatal("unproven source return reported success")
	}
	if _, err := os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-notice committed unlink lost", err)
	}
	if _, err := a.transferHistoryItem(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("pre-notice history hide was rolled back", err)
	}
	var after []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='import_plan' AND id=?`, item.PlanID).Scan(&after); err != nil || !bytes.Equal(audit, after) {
		t.Fatal("immutable import audit changed", err)
	}
	notices := removeLinkBarrierNotices(t, o)
	if len(notices) != 1 || notices[0].NoticeSite != "hardlink_immediate" {
		t.Fatal(notices)
	}
	var histories int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&histories); err != nil || histories != 1 {
		t.Fatal("accepted source history was lost", histories, err)
	}
	if sends.Load() != 1 {
		t.Fatal("unexpected external attempt count", sends.Load())
	}
	if err := o.e.readRemoveLinkSourceResult(ctx, notices[0]); err == nil {
		t.Fatal("mutable completion substituted for missing immutable source result")
	}
}

func TestRemoveLinkSourceBarrierSenderPanicAndRevokedRootsReleaseLocks(t *testing.T) {
	a, item, o := removeLinkBarrierOwner(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	a.pluginStore.notices.send = func(sendCtx context.Context, q pluginNoticeQueueEntry) (int, error) {
		calls.Add(1)
		if err := a.Store.Put(sendCtx, "owned_panic_sender", q.ID, map[string]bool{"entered": true}); err != nil {
			t.Error(err)
		}
		// Queue modules consume panic as external-unknown, not a source
		// exception. Root revocation independently denies source continuation.
		settings := a.Settings()
		settings.AllowedRoots = nil
		if err := a.saveSettings(sendCtx, settings); err != nil {
			t.Error(err)
		}
		panic("owned sender panic after root revocation")
	}
	if err := removeLinkBarrierDelete(t, item, o, ctx); err == nil {
		t.Fatal("revoked roots plus callback panic authorized source continuation")
	}
	notices := removeLinkBarrierNotices(t, o)
	if len(notices) != 1 || notices[0].NoticeSite != "empty_directory" || calls.Load() != 1 {
		t.Fatal("panic did not occur at first exact source site", notices, calls.Load())
	}
	for _, p := range []string{filepath.Dir(filepath.Dir(item.Source)), item.Target} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("panic/revocation permitted later file effect", p, err)
		}
	}
	// Use fresh administrative context: no cancellation-derived early return may
	// disguise retained global lifecycle/download/import locks.
	adminCtx, adminCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer adminCancel()
	settings := a.Settings()
	settings.AppDomain = "https://after-owned-panic.invalid"
	if err := a.saveSettings(adminCtx, settings); err != nil {
		t.Fatal("sender panic retained Settings locks", err)
	}
	req := httptest.NewRequest("POST", "/api/plugins/"+o.instance+"/reload", strings.NewReader("{}")).WithContext(adminCtx)
	_, _ = a.pluginRemoveLinkLifecycleHTTP(httptest.NewRecorder(), req, o.instance+"/reload")
	if adminCtx.Err() != nil {
		t.Fatal("administrative operation deadlocked after callback panic", adminCtx.Err())
	}
	if _, err := os.Stat(item.Target); err != nil {
		t.Fatal("administrative attempt resumed revoked cleanup", err)
	}
}

func TestRemoveLinkSourceBarrierRouterCallbackPanicIsUnknown(t *testing.T) {
	a, item, o := removeLinkBarrierOwner(t, true)
	var faults, sends atomic.Int32
	a.pluginStore.notices.afterCommit = func(operation string) error {
		if operation == "route_step" {
			faults.Add(1)
			panic("owned Router callback commit panic")
		}
		return nil
	}
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if err := removeLinkBarrierDelete(t, item, o, context.Background()); err == nil {
		t.Fatal("Router callback panic became known source completion")
	}
	if faults.Load() != 1 || sends.Load() != 0 {
		t.Fatal("panic did not stop first real Router callback", faults.Load(), sends.Load())
	}
	notices := removeLinkBarrierNotices(t, o)
	if len(notices) != 1 || notices[0].NoticeSite != "empty_directory" {
		t.Fatal("panic escaped exact first source boundary", notices)
	}
	if err := o.e.readRemoveLinkSourceResult(context.Background(), notices[0]); err == nil {
		t.Fatal("callback panic manufactured terminal source proof")
	}
	o.e.mu.Lock()
	hasLease := o.e.lease != nil
	o.e.mu.Unlock()
	if hasLease {
		t.Fatal("unknown Router panic retained cleanup write lease")
	}
	for _, p := range []string{filepath.Dir(filepath.Dir(item.Source)), item.Target} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("Router panic allowed subsequent file effects", p, err)
		}
	}
	a.pluginStore.notices.afterCommit = nil
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	settings := a.Settings()
	settings.AppDomain = "https://after-router-panic.invalid"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal("Router panic retained global write locks", err)
	}
	req := httptest.NewRequest("POST", "/api/plugins/"+o.instance+"/reload", strings.NewReader("{}")).WithContext(ctx)
	_, _ = a.pluginRemoveLinkLifecycleHTTP(httptest.NewRecorder(), req, o.instance+"/reload")
	if ctx.Err() != nil {
		t.Fatal("Router panic deadlocked later administrative operation", ctx.Err())
	}
	if _, err := os.Stat(item.Target); err != nil {
		t.Fatal("administrative call resumed unknown source cleanup", err)
	}
}
