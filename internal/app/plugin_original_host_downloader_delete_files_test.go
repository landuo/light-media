package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestHostDownloaderDeleteFilesActualSDKOwnedAndFailures(t *testing.T) {
	for _, mode := range []string{"confirmed", "unowned", "outside_root", "symlink", "hardlink", "shared_task", "shared_metadata_unknown", "snapshot_changed", "reference_added", "daemon_unknown", "bookkeeping_failed"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			root := t.TempDir()
			file := filepath.Join(root, "owned.bin")
			if err := os.WriteFile(file, []byte("owned payload"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "symlink" {
				target := filepath.Join(root, "real.bin")
				if err := os.Rename(file, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, file); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "hardlink" {
				if err := os.Link(file, filepath.Join(root, "other-link.bin")); err != nil {
					t.Fatal(err)
				}
			}
			a.config.AllowedRoots = []string{root}
			if mode == "outside_root" {
				a.config.AllowedRoots = []string{t.TempDir()}
			}
			hash := strings.Repeat("a", 40)
			other := strings.Repeat("b", 40)
			for _, record := range []NativeDownload{{ID: "owned-delete", Hash: hash, DownloaderID: "owned", State: "accepted"}, {ID: "other-delete", Hash: other, DownloaderID: "owned", State: "accepted"}} {
				if mode == "unowned" && record.Hash == hash {
					continue
				}
				if err := a.Store.Put(context.Background(), "native_download", record.ID, record); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "confirmed" {
				for _, value := range []map[string]any{{"id": "selected-intent", "hash": hash, "downloader_id": "owned", "state": "accepted"}, {"id": "already-cancelled-other", "hash": other, "downloader_id": "owned", "state": "cancelled"}} {
					id := value["id"].(string)
					if err := a.Store.Put(context.Background(), "intent", id, value); err != nil {
						t.Fatal(err)
					}
					payload, _ := json.Marshal(map[string]any{"id": id})
					if _, err := a.Store.DB.Exec(`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES(?,'','native_download','retry_wait',?,0,?,?)`, id, payload, now(), now()); err != nil {
						t.Fatal(err)
					}
					if _, err := a.Store.DB.Exec(`INSERT INTO reservations(media_key,season,episode,intent_id) VALUES(?,1,1,?)`, id, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "bookkeeping_failed" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_delete_failure BEFORE UPDATE ON records WHEN OLD.id='owned-delete' BEGIN SELECT RAISE(ABORT,'owned synthetic failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			var writes, filesReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					row := workflowTask(hash, 1)
					row["save_path"] = root
					row["state"] = "uploading"
					row["tags"] = ""
					if (mode == "shared_task" || mode == "shared_metadata_unknown") && r.FormValue("hashes") == "" && r.URL.Query().Get("hashes") == "" {
						otherRow := workflowTask(other, 1)
						otherRow["save_path"] = root
						otherRow["tags"] = "foreign"
						workflowJSON(w, []any{row, otherRow})
					} else {
						workflowJSON(w, []any{row})
					}
				case "/api/v2/torrents/files":
					if mode == "reference_added" && filesReads.Add(1) == 2 {
						if err := a.Store.Put(context.Background(), "asset", "new-owner", Asset{ID: "new-owner", State: "present", Path: file}); err != nil {
							t.Error(err)
						}
					}
					if mode == "shared_metadata_unknown" && r.URL.Query().Get("hash") == other {
						workflowJSON(w, []any{})
						return
					}
					if mode == "snapshot_changed" && filesReads.Add(1) == 2 {
						if err := os.WriteFile(file, []byte("replacement payload"), 0600); err != nil {
							t.Error(err)
						}
					}
					workflowJSON(w, []any{map[string]any{"name": "owned.bin", "size": 13, "progress": 1}})
				case "/api/v2/torrents/delete":
					_ = r.ParseForm()
					if r.Form.Get("hashes") != hash || r.Form.Get("deleteFiles") != "true" {
						t.Error("source delete-file primitive changed", r.Form)
					}
					writes.Add(1)
					if err := os.Remove(file); err != nil {
						t.Error(err)
					}
					if mode == "daemon_unknown" {
						h := w.(http.Hijacker)
						conn, _, err := h.Hijack()
						if err != nil {
							t.Error(err)
						} else {
							conn.Close()
						}
						return
					}
				default:
					t.Error("unexpected request", r.URL.Path)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
			var out plugins.DownloaderDeleteFilesV1
			err := hostDownloaderCall(t, object, "delete_files", plugins.DownloaderDeleteFilesRequestV1{DownloaderID: "owned", Hashes: []string{hash}}, &out)
			rejected := mode == "unowned" || mode == "outside_root" || mode == "symlink" || mode == "hardlink" || mode == "shared_task" || mode == "shared_metadata_unknown" || mode == "snapshot_changed" || mode == "reference_added"
			if rejected {
				if err == nil || writes.Load() != 0 {
					t.Fatalf("unsafe selection dispatched: receipt=%+v err=%v writes=%d", out, err, writes.Load())
				}
				if _, err = os.Lstat(file); err != nil {
					t.Fatal("rejected file lost", err)
				}
			} else {
				if err != nil || writes.Load() != 1 {
					t.Fatalf("receipt=%+v err=%v writes=%d", out, err, writes.Load())
				}
				if _, err = os.Lstat(file); !os.IsNotExist(err) {
					t.Fatal("actual owned file remains", err)
				}
				if mode == "daemon_unknown" {
					if out.Outcome != "unknown" || out.DaemonApplied || out.BookkeepingCompleted {
						t.Fatal(out)
					}
				} else if !out.Accepted || !out.DaemonApplied || out.Outcome != "confirmed" || out.BookkeepingCompleted != (mode != "bookkeeping_failed") {
					t.Fatal(out)
				}
				if mode == "bookkeeping_failed" && out.FailureCode != "DOWNLOAD_BOOKKEEPING_FAILED" {
					t.Fatal(out)
				}
			}
			var unrelated NativeDownload
			if err := a.Store.Get(context.Background(), "native_download", "other-delete", &unrelated); err != nil || unrelated.State != "accepted" {
				t.Fatal("unrelated bookkeeping changed", unrelated, err)
			}
			if mode != "unowned" {
				var selected NativeDownload
				if err := a.Store.Get(context.Background(), "native_download", "owned-delete", &selected); err != nil {
					t.Fatal(err)
				}
				want := "accepted"
				if mode == "confirmed" {
					want = "cancelled"
				}
				if selected.State != want {
					t.Fatalf("selected state=%s want=%s", selected.State, want)
				}
			}
			if mode == "confirmed" {
				for _, id := range []string{"selected-intent", "already-cancelled-other"} {
					var state string
					var reservations int
					if err := a.Store.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, id).Scan(&state); err != nil {
						t.Fatal(err)
					}
					if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations WHERE intent_id=?`, id).Scan(&reservations); err != nil {
						t.Fatal(err)
					}
					if id == "selected-intent" && (state != "cancelled" || reservations != 0) || id != "selected-intent" && (state != "retry_wait" || reservations != 1) {
						t.Fatal("selected cancellation affected unrelated retry/reservation", id, state, reservations)
					}
				}
			}
		})
	}
}

func TestHostDownloaderDeleteFilesTransmissionDialectSource(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			root := t.TempDir()
			file := filepath.Join(root, "owned.bin")
			if err := os.WriteFile(file, []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			a.config.AllowedRoots = []string{root}
			hash := strings.Repeat("c", 40)
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var call map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&call) != nil {
					t.Fatal("invalid JSON")
				}
				var method string
				_ = json.Unmarshal(call["method"], &method)
				key := "params"
				if legacy {
					key = "arguments"
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(call[key], &args)
				result := map[string]any{}
				switch strings.ReplaceAll(method, "-", "_") {
				case "session_get":
					if legacy {
						result["version"] = "4.0.6"
					} else {
						result["version"] = "4.1.0"
					}
				case "torrent_get":
					result["torrents"] = []any{map[string]any{"hashString": hash, "hash_string": hash, "downloadDir": root, "download_dir": root, "status": 6, "labels": []string{"light-media"}, "files": []any{map[string]any{"name": "owned.bin", "length": 5, "bytesCompleted": 5, "bytes_completed": 5}}}}
				case "torrent_remove":
					var ids []string
					_ = json.Unmarshal(args["ids"], &ids)
					field := "delete_local_data"
					if legacy {
						field = "delete-local-data"
					}
					var deletion bool
					_ = json.Unmarshal(args[field], &deletion)
					if len(ids) != 1 || ids[0] != hash || !deletion {
						t.Error("wrong source removal", args)
					}
					writes.Add(1)
					if err := os.Remove(file); err != nil {
						t.Error(err)
					}
				default:
					t.Error("unexpected TR method", method)
				}
				if legacy {
					workflowJSON(w, map[string]any{"result": "success", "tag": json.RawMessage(call["tag"]), "arguments": result})
				} else {
					workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(call["id"]), "result": result})
				}
			}))
			defer server.Close()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
			var out plugins.DownloaderDeleteFilesV1
			if err := hostDownloaderCall(t, object, "delete_files", plugins.DownloaderDeleteFilesRequestV1{DownloaderID: "owned", Hashes: []string{hash}}, &out); err != nil || !out.DaemonApplied || !out.BookkeepingCompleted || writes.Load() != 1 {
				t.Fatal(out, err, writes.Load())
			}
		})
	}
}
