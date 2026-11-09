package app

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/sites"
)

// This opt-in test owns both downloaders and all their data. It refuses normal
// endpoints and never uses the user's existing qB/Transmission credentials.
func TestIsolatedDownloadersRealTransferAndImport(t *testing.T) {
	base := os.Getenv("LIGHT_MEDIA_ISOLATED_DIR")
	if base == "" {
		t.Skip("requires explicitly provisioned isolated NAS downloaders")
	}
	if !strings.HasPrefix(base, "/vol2/1000/docker/light-media-verify-") || filepath.Clean(base) != base {
		t.Fatal("isolated directory guard")
	}
	peer := os.Getenv("LIGHT_MEDIA_ISOLATED_TR_PEER")
	if !strings.HasPrefix(peer, "172.17.0.1:") {
		t.Fatal("isolated bridge peer guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	clip, err := os.ReadFile(filepath.Join(base, "fixture.mkv"))
	if err != nil || len(clip) < 16384 {
		t.Fatalf("fixture: bytes=%d err=%v", len(clip), err)
	}
	name := "测试影片.2024.1080p.WEB-DL.x264.mkv"
	var pieces []byte
	for offset := 0; offset < len(clip); offset += 16384 {
		end := offset + 16384
		if end > len(clip) {
			end = len(clip)
		}
		h := sha1.Sum(clip[offset:end])
		pieces = append(pieces, h[:]...)
	}
	info := workflowBencode(map[string]any{"name": name, "length": len(clip), "piece length": 16384, "pieces": pieces, "private": 1})
	h := sha1.Sum(info)
	hash := hex.EncodeToString(h[:])
	// A private, in-memory tracker advertises only the two synthetic tasks. It
	// binds Docker's host bridge and never contacts a public tracker or peer.
	trackerListener, err := net.Listen("tcp4", "172.17.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var trackerMu sync.Mutex
	trackerPeers := map[string][]byte{}
	tracker := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("info_hash") != string(h[:]) {
			http.NotFound(w, r)
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		port, portErr := strconv.Atoi(r.URL.Query().Get("port"))
		if ip == "192.168.8.129" || ip == "192.168.8.249" {
			ip = "172.17.0.1"
		}
		address := net.ParseIP(ip).To4()
		if err != nil || portErr != nil || address == nil || port < 1 || port > 65535 || !strings.HasPrefix(ip, "172.17.") {
			if r.URL.Path == "/announce" {
				t.Logf("owned tracker rejected address=%s port=%d parse_error=%t", ip, port, portErr != nil)
			}
			http.Error(w, "invalid owned peer", 400)
			return
		}
		compact := append([]byte{}, address...)
		compact = binary.BigEndian.AppendUint16(compact, uint16(port))
		key := net.JoinHostPort(ip, strconv.Itoa(port))
		trackerMu.Lock()
		if r.URL.Query().Get("event") == "stopped" {
			delete(trackerPeers, key)
		} else {
			trackerPeers[key] = compact
		}
		var peers []byte
		for other, value := range trackerPeers {
			if other != key {
				peers = append(peers, value...)
			}
		}
		trackerMu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		w.Write(workflowBencode(map[string]any{"interval": 1, "min interval": 1, "peers": peers, "complete": 1, "incomplete": 1}))
	}))
	tracker.Listener = trackerListener
	tracker.Start()
	defer tracker.Close()
	torrent := append([]byte("d8:announce"), workflowBencode(tracker.URL+"/announce")...)
	torrent = append(torrent, []byte("4:info")...)
	torrent = append(append(torrent, info...), 'e')
	qbPassword := os.Getenv("LIGHT_MEDIA_ISOLATED_QB_PASSWORD")
	if qbPassword == "" {
		t.Fatal("missing dedicated qB session credential")
	}
	jar, _ := cookiejar.New(nil)
	qbHTTP := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	loginRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:43185/api/v2/auth/login", strings.NewReader(url.Values{"username": {"admin"}, "password": {strings.TrimSpace(qbPassword)}}.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRequest.Header.Set("Origin", "http://127.0.0.1:43185")
	loginRequest.Header.Set("Referer", "http://127.0.0.1:43185/")
	login, err := qbHTTP.Do(loginRequest)
	if err != nil {
		t.Fatal("dedicated qB login transport failed")
	}
	loginBody, _ := io.ReadAll(io.LimitReader(login.Body, 100))
	login.Body.Close()
	if login.StatusCode != 200 || string(loginBody) != "Ok." {
		t.Fatalf("dedicated qB login failed: status=%d rejected=%t bytes=%d", login.StatusCode, strings.TrimSpace(string(loginBody)) == "Fails.", len(loginBody))
	}
	versionResponse, err := qbHTTP.Get("http://127.0.0.1:43185/api/v2/app/version")
	if err != nil {
		t.Fatal(err)
	}
	version, _ := io.ReadAll(io.LimitReader(versionResponse.Body, 100))
	versionResponse.Body.Close()
	t.Logf("actual qB version %s", version)
	configs := []download.Config{{ID: "isolated-qb", Kind: "qb", BaseURL: "http://127.0.0.1:43185", Username: "admin", Password: qbPassword}, {ID: "isolated-tr", Kind: "transmission", BaseURL: "http://127.0.0.1:43191/transmission/rpc"}}
	for index, config := range configs {
		t.Run(config.ID, func(t *testing.T) {
			source := filepath.Join(base, config.ID)
			if err := os.MkdirAll(source, 0750); err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				if err := os.WriteFile(filepath.Join(source, name), clip, 0640); err != nil {
					t.Fatal(err)
				}
			}
			client, err := download.New(config)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := client.List(ctx)
			if err != nil || len(tasks) != 0 {
				t.Fatalf("dedicated instance not empty: count=%d err=%v", len(tasks), err)
			}
			added, err := client.Add(ctx, download.AddRequest{Torrent: torrent, SavePath: source, Paused: true, Tags: []string{"isolated-validation"}})
			if err != nil || added.Hash != hash || added.Duplicate {
				t.Fatalf("Add: %+v err=%v", added, err)
			}
			duplicate, err := client.Add(ctx, download.AddRequest{Torrent: torrent, SavePath: filepath.Join(base, "must-not-move"), Paused: false, Tags: []string{"must-not-apply"}})
			if err != nil || !duplicate.Duplicate || duplicate.Hash != hash {
				t.Fatalf("duplicate %+v err=%v", duplicate, err)
			}
			tasks, err = client.List(ctx)
			if err != nil || len(tasks) != 1 || tasks[0].DownloadDir != source {
				t.Fatalf("duplicate mutated destination: %+v err=%v", tasks, err)
			}
			for _, action := range []string{"pause", "verify", "start"} {
				if err = client.Control(ctx, hash, action); err != nil {
					t.Fatal(action, err)
				}
			}
			// Recreate the empty leecher through the application before transfer.
			if index == 1 {
				if err = client.Control(ctx, hash, "remove_keep_data"); err != nil {
					t.Fatal(err)
				}
			}
			a, _, target := importTestApp(t)
			target = filepath.Join(base, config.ID+"-library")
			if err = os.MkdirAll(target, 0750); err != nil {
				t.Fatal(err)
			}
			a.config.Roots[0].Path = target
			m := importFixture(t, a, "movie")
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
				case "/download.php":
					w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			a.config.AllowedRoots = []string{source}
			a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "isolated-site", Name: "owned fixture", Kind: "ubits", BaseURL: site.URL, Cookie: "owned-fixture"}, Enabled: true}}
			a.config.Downloaders = []DownloaderConfig{{Config: config, DownloadPath: source, Mappings: []PathMapping{{From: source, To: source}}}}
			a.config.DefaultDownloader = config.ID
			a.config.DefaultRoot = "root"
			preview, err := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "isolated-site", ResourceID: "101"}, false)
			if err != nil {
				t.Fatal("preview", err)
			}
			intent, err := a.confirmDownload(ctx, preview.ID, false)
			if err != nil {
				t.Fatal("confirm", err)
			}
			job, err := a.claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.safeRun(ctx, job); err != nil {
				t.Fatal("download job", err)
			}
			if _, err = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(70 * time.Second); ; {
				if index == 1 {
					request, _ := http.NewRequestWithContext(ctx, http.MethodPost, configs[0].BaseURL+"/api/v2/torrents/addPeers", strings.NewReader(url.Values{"hashes": {hash}, "peers": {peer}}.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.Header.Set("Origin", configs[0].BaseURL)
					request.Header.Set("Referer", configs[0].BaseURL+"/")
					response, e := qbHTTP.Do(request)
					if e != nil {
						t.Fatal(e)
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if response.StatusCode != 200 {
						t.Fatalf("add isolated peer: %d", response.StatusCode)
					}
				}
				tasks, err = client.List(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(tasks) == 1 && tasks[0].Progress == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("completion timeout %+v", tasks)
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Second):
				}
			}
			files, err := client.Files(ctx, hash)
			if err != nil || len(files) != 1 || files[0].Path != name || files[0].Progress != 1 || files[0].Completed != int64(len(clip)) {
				t.Fatalf("Files %+v err=%v", files, err)
			}
			if index == 0 {
				// Verification is asynchronous and can preserve the stopped state.
				// Start the verified seed explicitly before offering it to TR.
				if err = client.Control(ctx, hash, "start"); err != nil {
					t.Fatal(err)
				}
				for retry := 0; retry < 10; retry++ {
					tasks, err = client.List(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if len(tasks) == 1 && tasks[0].Status == "seeding" {
						break
					}
					if retry == 9 {
						t.Fatalf("isolated seed not active: %+v", tasks)
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			if index == 1 {
				result := isolatedTransmissionRPC(t, config.BaseURL, "torrent_get", map[string]any{"ids": []string{hash}, "fields": []string{"downloaded_ever", "have_valid"}})
				var proof struct {
					Torrents []struct {
						Downloaded int64 `json:"downloaded_ever"`
						Valid      int64 `json:"have_valid"`
					} `json:"torrents"`
				}
				if err = json.Unmarshal(result, &proof); err != nil || len(proof.Torrents) != 1 || proof.Torrents[0].Downloaded < int64(len(clip)) || proof.Torrents[0].Valid != int64(len(clip)) {
					t.Fatalf("real transfer proof %s err=%v", result, err)
				}
				t.Logf("actual peer transfer bytes=%d verified=%d", proof.Torrents[0].Downloaded, proof.Torrents[0].Valid)
			} else {
				t.Logf("preseeded existing fixture verified bytes=%d (no claim of actual network download)", len(clip))
			}
			if err = a.pollDownloads(ctx); err != nil {
				t.Fatal("poll", err)
			}
			for count := 0; count < 4; count++ {
				job, err = a.claim(ctx)
				if err != nil {
					break
				}
				if err = a.safeRun(ctx, job); err != nil {
					t.Fatalf("%s job: %v", job.Kind, err)
				}
				if _, err = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			var current Intent
			if err = a.Store.Get(ctx, "intent", intent.ID, &current); err != nil || current.State != "imported" {
				if current.ImportPlanID != "" {
					var plan ImportPlan
					if a.Store.Get(ctx, "import_plan", current.ImportPlanID, &plan) == nil {
						for _, file := range plan.Files {
							t.Logf("owned plan status=%s reason=%s method=%s", file.Status, file.Reason, file.Method)
						}
					}
				}
				t.Fatalf("import intent %+v err=%v", current, err)
			}
			var assets, slots int
			a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&assets)
			a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots`).Scan(&slots)
			if assets != 1 || slots != 1 {
				t.Fatalf("assets=%d slots=%d", assets, slots)
			}
			var mediaFiles, nfoFiles int
			filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					if isVideo(path) {
						mediaFiles++
					}
					if filepath.Ext(path) == ".nfo" {
						nfoFiles++
					}
				}
				return err
			})
			if mediaFiles != 1 || nfoFiles != 1 {
				t.Fatalf("organized media=%d nfo=%d", mediaFiles, nfoFiles)
			}
			retained, err := os.ReadFile(filepath.Join(source, name))
			if err != nil || !bytes.Equal(retained, clip) {
				t.Fatal("source changed or removed", err)
			}
			tasks, err = client.List(ctx)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("workflow duplicate count=%d err=%v", len(tasks), err)
			}
			t.Logf("native search/preview/confirm -> actual %s task -> 100%% poll -> asset+NFO; source retained; task count=%d", config.Kind, len(tasks))
			// Keep the qB seeder available for the Transmission real transfer.
			if index == 1 {
				if err = client.Control(ctx, hash, "remove_keep_data"); err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(filepath.Join(source, name)); err != nil {
					t.Fatal("remove_keep_data removed source", err)
				}
			}
		})
		if t.Failed() {
			break
		}
	}
	if !t.Failed() {
		seed, err := download.New(configs[0])
		if err != nil {
			t.Fatal(err)
		}
		if err = seed.Control(ctx, hash, "remove_keep_data"); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(base, configs[0].ID, name)); err != nil {
			t.Fatal("qB remove_keep_data removed fixture", err)
		}
	}
}

func isolatedTransmissionRPC(t *testing.T, endpoint, method string, params any) json.RawMessage {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	session := ""
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Transmission-Session-Id", session)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == 409 {
			session = resp.Header.Get("X-Transmission-Session-Id")
			continue
		}
		var wire struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if resp.StatusCode != 200 || json.Unmarshal(raw, &wire) != nil || len(wire.Error) > 0 {
			t.Fatalf("owned TR RPC failed status=%d error=%s", resp.StatusCode, wire.Error)
		}
		return wire.Result
	}
	t.Fatal(fmt.Errorf("owned TR handshake exhausted"))
	return nil
}
