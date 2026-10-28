package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// Initial accepted download material is made by the existing real in-process
// protocol flow (owned site search/torrent + Client.Add + owning SQL facts).
// It is never presented as a cmd/server download. Completion itself, its veto,
// plugin notification delivery and complete broadcast run in real cmd/server.
func TestPluginSubscriptionCompletionProductionCLI(t *testing.T) {
	server := os.Getenv("PLUGIN_PROCESS_SERVER_BINARY")
	if server == "" {
		t.Skip("unverified: actual cmd/server required")
	}
	for _, name := range []string{"OWNED_INPUT_FIXTURE_BINARY", "OWNED_EPISODES_REFRESH_FIXTURE_BINARY", "OWNED_COMPLETION_CHECK_FIXTURE_BINARY", "OWNED_NOTICE_FIXTURE_BINARY"} {
		if os.Getenv(name) == "" {
			t.Fatal("actual compiled fixture required", name)
		}
	}
	kind := os.Getenv("PLUGIN_PROCESS_COMPLETION_TYPE")
	if kind == "" {
		kind = "movie"
	}
	if kind != "movie" && kind != "tv" {
		t.Fatal("invalid owned completion type")
	}
	ordinaryMovieDownloadWorkflow(t, kind, func(a *App, s Subscription) {
		cookie := setupTestAdmin(t, a)
		ctx := context.Background()
		// The protocol fixture's old site is not needed by an already accepted
		// completion. Keep only actual downloader/root settings, and never teach
		// production to accept a loopback site just for this test.
		settings := a.Settings()
		settings.Sites = nil
		settings.APIToken = "owned-completion-process-api-key"
		for i := range settings.Downloaders {
			disabled := false
			settings.Downloaders[i].Enabled = &disabled
		}
		if err := a.saveSettings(ctx, settings); err != nil {
			t.Fatal("persist owned completion settings", err)
		}
		input := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_INPUT_FIXTURE_BINARY"))
		checker := installOrderWorker(t, a, cookie, os.Getenv("OWNED_COMPLETION_CHECK_FIXTURE_BINARY"))
		provider := installOrderWorker(t, a, cookie, os.Getenv("OWNED_EPISODES_REFRESH_FIXTURE_BINARY"))
		notifier := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_NOTICE_FIXTURE_BINARY"))
		configure := func(id string, body any) {
			raw, _ := json.Marshal(body)
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
		}
		configure(input.PluginID, map[string]any{"enabled": true, "event_controls": []map[string]any{{"action": "add", "kind": "broadcast", "type": "subscribe.complete", "method": "event_dynamic"}}})
		r := callTest(t, a.Handler(), "POST", "/api/plugins/"+input.PluginID+"/clone", `{"suffix":"Complete"}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		clone := input.PluginID + "complete"
		configure(clone, map[string]any{"enabled": true, "event_controls": []map[string]any{{"action": "add", "kind": "broadcast", "type": "subscribe.complete", "method": "event_dynamic"}}})
		trace := filepath.Join(a.Dir, "owned-completion-chain.jsonl")
		configure(checker.PluginID, map[string]any{"enabled": true, "mode": "cancel_error", "source": "Owned completion veto", "reason": "owned still open", "trace_path": trace})
		wire := map[string]any{"id": s.Media.ID, "title": s.Media.Title, "original_title": s.Media.OriginalTitle, "release_date": "2024-01-01", "overview": s.Media.Overview, "poster_path": "/owned.jpg", "backdrop_path": "/owned.jpg", "vote_average": 7.3, "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{"imdb_id": "tt1234567"}, "translations": map[string]any{"translations": []any{}}, "alternative_titles": map[string]any{"titles": []any{}, "results": []any{}}}
		wireRaw, _ := json.Marshal(wire)
		if kind == "tv" {
			wire["name"], wire["original_name"], wire["first_air_date"] = s.Media.Title, s.Media.OriginalTitle, "2024-01-01"
			wire["seasons"] = []any{map[string]any{"season_number": s.Season, "episode_count": 1}}
			wireRaw, _ = json.Marshal(wire)
		}
		full, err := sourceTMDBMediaInfo(wireRaw, kind, "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		configure(provider.PluginID, map[string]any{"enabled": true, "mode": "observe", "trace_path": trace, "recognize_media": json.RawMessage(full)})
		var mu sync.Mutex
		delivered := map[string]int{}
		readOnly, err := sql.Open("sqlite", "file:"+filepath.Join(a.Dir, "light-media.db")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer readOnly.Close()
		countRows := func(kind, id string) int {
			t.Helper()
			var count int
			query := `SELECT count(*) FROM records WHERE kind=?`
			args := []any{kind}
			if id != "" {
				query += ` AND id=?`
				args = append(args, id)
			}
			if err := readOnly.QueryRow(query, args...).Scan(&count); err != nil {
				t.Error("actual completion readonly SQL", err)
				return -1
			}
			return count
		}
		sink := httptestNewOwnedCompletionSink(t, &mu, delivered, func() {
			if countRows("subscription_history", "") != 1 || countRows("subscription", s.ID) != 0 {
				t.Error("actual notice preceded committed history/delete")
			}
		})
		defer sink.Close()
		configure(notifier.PluginID, map[string]any{"enabled": true, "endpoint": sink.URL + "/source"})
		r = callTest(t, a.Handler(), "POST", "/api/plugins/"+notifier.PluginID+"/clone", `{"suffix":"Complete"}`, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		noticeClone := notifier.PluginID + "complete"
		configure(noticeClone, map[string]any{"enabled": true, "endpoint": sink.URL + "/clone"})
		subscriptionStatsInstallSettings(t, a, "http://owned-unused.invalid", false)
		dir := a.Dir
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		origin := "http://" + address
		log, err := os.Create(filepath.Join(dir, "actual-completion-process.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		defer copyOwnedEventsProcessEvidence(t, "events36-completion-process.log", filepath.Join(dir, "actual-completion-process.log"))
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
		var process *exec.Cmd
		stop := func() {
			t.Helper()
			if process == nil {
				return
			}
			_ = process.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Error("actual completion shutdown", err)
				}
			case <-time.After(10 * time.Second):
				_ = process.Process.Kill()
				<-done
				t.Error("actual completion stop timeout")
			}
			process = nil
		}
		defer stop()
		start := func() {
			t.Helper()
			process = exec.Command(server, "--listen", address, "--data-dir", dir)
			process.Stdout, process.Stderr = log, log
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(12 * time.Second)
			for {
				response, err := client.Get(origin + "/api/auth/status")
				if err == nil {
					response.Body.Close()
					if response.StatusCode == 200 {
						break
					}
				}
				if time.Now().After(until) {
					t.Fatal("actual completion server unavailable")
				}
				time.Sleep(20 * time.Millisecond)
			}
			response, err := client.Post(origin+"/api/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"a-strong-test-password"}`))
			if err != nil {
				t.Fatal("actual completion login")
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("actual completion login rejected")
			}
		}
		request := func(method, path string, body any) []byte {
			t.Helper()
			var reader io.Reader
			if body != nil {
				raw, _ := json.Marshal(body)
				reader = bytes.NewReader(raw)
			}
			req, _ := http.NewRequest(method, origin+path, reader)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-KEY", settings.APIToken)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal("actual completion HTTP", method, path)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			if response.StatusCode != 200 {
				t.Fatalf("actual completion HTTP %s %s status%d", method, path, response.StatusCode)
			}
			return raw
		}
		list := func() []Subscription {
			var out struct {
				Data struct{ Items []Subscription }
			}
			if json.Unmarshal(request("GET", "/api/subscriptions", nil), &out) != nil {
				t.Fatal("actual completion list")
			}
			return out.Data.Items
		}
		completeEvents := func(id string) []json.RawMessage {
			var out struct {
				Events []struct {
					Type string
					Data json.RawMessage
				}
			}
			if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil), &out) != nil {
				t.Fatal("actual completion events")
			}
			events := []json.RawMessage{}
			for _, e := range out.Events {
				if e.Type == "subscribe.complete" {
					events = append(events, e.Data)
				}
			}
			return events
		}
		start()
		until := time.Now().Add(10 * time.Second)
		for {
			items := list()
			vetoed := false
			for _, item := range items {
				if item.ID == s.ID && item.LastReason == "完成被 Owned completion veto 否决：owned still open" {
					vetoed = true
				}
			}
			if vetoed {
				break
			}
			if time.Now().After(until) {
				t.Fatal("actual completion chain veto not consumed")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(completeEvents(input.PluginID)) != 0 || len(completeEvents(clone)) != 0 {
			t.Fatal("actual veto allowed Complete")
		}
		mu.Lock()
		count := delivered["/source"] + delivered["/clone"]
		mu.Unlock()
		if count != 0 {
			t.Fatal("actual veto sent notice")
		}
		if countRows("subscription_history", "") != 0 {
			t.Fatal("actual veto wrote history")
		}
		request("PUT", "/api/plugins/"+checker.PluginID, map[string]any{"enabled": true, "mode": "ordinary", "trace_path": trace})
		request("POST", "/api/subscriptions/"+s.ID+"/check", map[string]any{})
		until = time.Now().Add(15 * time.Second)
		for {
			events := completeEvents(clone)
			if len(events) == 1 {
				var data map[string]json.RawMessage
				if json.Unmarshal(events[0], &data) != nil || string(data["subscribe_id"]) != `"`+s.ID+`"` || len(data["subscribe_info"]) == 0 || len(data["mediainfo"]) == 0 {
					t.Fatal("actual completion source payload")
				}
				break
			}
			if len(events) > 1 || time.Now().After(until) {
				t.Fatal("actual completed event missing or repeated", len(events))
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(completeEvents(input.PluginID)) != 1 {
			t.Fatal("actual source/clone complete isolation")
		}
		for _, item := range list() {
			if item.ID == s.ID {
				t.Fatal("actual completion did not remove subscription")
			}
		}
		mu.Lock()
		sourceSends, cloneSends := delivered["/source"], delivered["/clone"]
		mu.Unlock()
		if sourceSends != 1 || cloneSends != 1 {
			t.Fatal("actual completion native notice sender counts", sourceSends, cloneSends)
		}
		mediaType, season := "电影", any(nil)
		if kind == "tv" {
			mediaType, season = "电视剧", s.Season
			calls := 0
			for _, row := range readOrderTrace(t, trace) {
				if row.Method == "recognize_media" {
					calls++
				}
			}
			// One initial preparation, one explicit check and its new durable
			// preparation: each operation recognizes once, rather than twice
			// inside either preparation.
			if calls != 3 {
				t.Fatal("actual TV prepare repeated full recognition", calls)
			}
		}
		requireOwnedSubscriptionRecognizeSource(t, readOrderTrace(t, trace), s.Media.Title, "2024", mediaType, s.Media.ID, season)
		copyOwnedEventsProcessEvidence(t, "events36-completion-sdk-trace.jsonl", trace)
		for _, id := range []string{input.PluginID, clone} {
			body, _ := json.Marshal(completeEvents(id))
			writeOwnedEventsProcessEvidence(t, "events36-complete-"+id+".json", body)
		}
		oldPID := process.Process.Pid
		stop()
		start()
		if process.Process.Pid == oldPID {
			t.Fatal("actual completion cold process retained")
		}
		time.Sleep(150 * time.Millisecond)
		if len(completeEvents(clone)) != 0 {
			t.Fatal("cold completion replayed")
		}
		mu.Lock()
		coldSends := delivered["/source"] + delivered["/clone"]
		mu.Unlock()
		if coldSends != 2 {
			t.Fatal("cold completion resent notice")
		}
		for _, id := range []string{noticeClone, notifier.PluginID, clone, input.PluginID, checker.PluginID, provider.PluginID} {
			request("DELETE", "/api/plugins/"+id, nil)
		}
		t.Logf("actual Linux complete: initial accepted download from owned in-process search/Client.Add/SQL protocol flow; real cmd/server consumed cancel_error veto before history/delete/notice/event, ordinary-return did not veto next actual /check; history/delete->actual standard SDK notices source/clone HTTP1/1->Complete source/clone once; cold PID%d->%d does not replay or resend", oldPID, process.Process.Pid)
	}, func(in *SubscriptionRequest) {
		if kind == "tv" {
			manual := false
			in.ManualTotalEpisode = &manual
		}
	})
}

func httptestNewOwnedCompletionSink(t *testing.T, mu *sync.Mutex, counts map[string]int, before func()) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]json.RawMessage
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("actual complete sender request")
		}
		before()
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(204)
	}))
}

func installOwnedResourceProcessWorker(t *testing.T, a *App, cookie *http.Cookie, path string) plugins.Manifest {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil {
		t.Fatal("actual resource fixture manifest")
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repo.Close)
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	body, _ := json.Marshal(map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	return manifest
}
