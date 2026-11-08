package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScrapeFileCleanInstalledAMD64DelayedAndStop(t *testing.T) {
	bundle := os.Getenv("SOURCE_SCRAPE_FILE_CLEAN_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the exact owned Linux amd64 ScrapeFileClean package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, history := historyRemovalFixture(t)
	cookie := setupTestAdmin(t, a)
	source, library := filepath.Dir(history.Source), a.Settings().Roots[0].Path
	dir := filepath.Dir(history.Target)
	stem := strings.TrimSuffix(history.Target, filepath.Ext(history.Target))
	sidecar := stem + "-mediainfo.json"
	trickplay := stem + ".trickplay"
	keep := filepath.Join(dir, "unrelated.mkv")
	importWrite(t, sidecar, []byte("owned metadata"))
	importWrite(t, filepath.Join(trickplay, "1", "frame.jpg"), []byte("owned trickplay"))
	importWrite(t, keep, []byte("different inode must survive"))
	pendingSource := filepath.Join(source, "pending-stop.mkv")
	pendingLink := filepath.Join(library, "pending", "pending-stop.mkv")
	importWrite(t, pendingSource, []byte("do not flush on stop"))
	if err := os.MkdirAll(filepath.Dir(pendingLink), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pendingSource, pendingLink); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, cookie, bundle, "ScrapeFileClean")
	const path = "/api/plugins/ScrapeFileClean"
	form := callTest(t, a.Handler(), http.MethodGet, path+"/form", "", cookie, "")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `"monitor_dirs"`) || !strings.Contains(form.Body.String(), `"delay_seconds":30`) {
		t.Fatalf("the source form was not served: %d %s", form.Code, form.Body.String())
	}
	configure := func(enabled bool) {
		t.Helper()
		body := map[string]any{
			"enabled": enabled, "monitor_dirs": strings.Join([]string{filepath.Join(source, "missing-root"), source, library}, "\n"),
			"delete_history": true, "delete_scrap_infos": true, "custom_scrap_extensions": "-mediainfo.json",
			"delay_seconds": 10, "delayed_deletion": true,
		}
		res := callTest(t, a.Handler(), http.MethodPut, path, string(mustCleanSeedJSON(body)), cookie, "")
		assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	}
	configure(true)
	if err := os.Remove(history.Source); err != nil {
		t.Fatal(err)
	}
	queued := func(name string) bool {
		logs, _ := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
		for _, row := range logs {
			if msg, _ := row["message"].(string); strings.Contains(msg, name+" 加入延迟删除队列") {
				return true
			}
		}
		return false
	}
	privateAwait(t, "real inotify queues the deleted source", func() bool { return queued(filepath.Base(history.Source)) })
	if _, err := os.Stat(history.Target); err != nil {
		t.Fatal("the hardlink did not survive the delayed interval", err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		_, targetErr := os.Stat(history.Target)
		_, sidecarErr := os.Stat(sidecar)
		_, trickplayErr := os.Stat(trickplay)
		_, historyErr := a.transferHistoryItem(wait, history.ID)
		if errors.Is(targetErr, os.ErrNotExist) && errors.Is(sidecarErr, os.ErrNotExist) && errors.Is(trickplayErr, os.ErrNotExist) && errors.Is(historyErr, sql.ErrNoRows) {
			break
		}
		select {
		case <-poll.C:
		case <-wait.Done():
			t.Fatalf("source cleanup effects did not converge: target=%v sidecar=%v trickplay=%v history=%v", targetErr, sidecarErr, trickplayErr, historyErr)
		}
	}
	for _, unchanged := range []string{keep, library, source} {
		if _, err := os.Stat(unchanged); err != nil {
			t.Fatalf("cleanup touched an unrelated file or monitor root: %s %v", unchanged, err)
		}
	}
	if err := os.Remove(pendingSource); err != nil {
		t.Fatal(err)
	}
	privateAwait(t, "the second source deletion reaches the live queue", func() bool { return queued("pending-stop.mkv") })
	configure(false)
	if _, err := os.Stat(pendingLink); err != nil {
		t.Fatal("stopping flushed the pending deletion", err)
	}
	object := originalBaseSDKLoaded(t, a, "ScrapeFileClean")
	if enabled, err := object.getState(t.Context()); err != nil || enabled {
		t.Fatalf("disabled source watcher remained enabled: %t %v", enabled, err)
	}
	t.Log("installed Linux package queued the real source deletion, removed the hardlink, custom sidecar, trickplay and history after delay, and discarded the pending second deletion on disable")
}

func TestScrapeFileCleanInstalledAMD64IndexAndLostWatch(t *testing.T) {
	bundle := os.Getenv("SOURCE_SCRAPE_FILE_CLEAN_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the exact owned Linux amd64 ScrapeFileClean package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, history := historyRemovalFixture(t)
	cookie := setupTestAdmin(t, a)
	source, library := filepath.Dir(history.Source), a.Settings().Roots[0].Path
	root := filepath.Join(source, "owned-watch-root")
	input, link := filepath.Join(root, "Guarded.mkv"), filepath.Join(library, "Guarded.mkv")
	importWrite(t, input, []byte("keep after observation is lost"))
	if err := os.Link(input, link); err != nil {
		t.Fatal(err)
	}
	symlinkSource, symlinkLink := filepath.Join(root, "Recreated.mkv"), filepath.Join(library, "Recreated.mkv")
	importWrite(t, symlinkSource, []byte("same original file remains through symlink"))
	if err := os.Link(symlinkSource, symlinkLink); err != nil {
		t.Fatal(err)
	}
	var owned managedFile
	if err := a.Store.Get(t.Context(), "managed_file", tokenHash(history.Target), &owned); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, cookie, bundle, "ScrapeFileClean")
	configure := func(monitor string, delayed bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": monitor + "\n" + library, "delay_seconds": 10, "delayed_deletion": delayed})
		res := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/ScrapeFileClean", string(body), cookie, "")
		assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	}
	configure(source, false)
	if err := os.Remove(history.Source); err != nil {
		t.Fatal(err)
	}
	privateAwait(t, "confirmed cleanup marks the same managed asset missing without deleting history", func() bool {
		var asset Asset
		err := a.Store.Get(t.Context(), "asset", owned.AssetID, &asset)
		return err == nil && asset.Path == history.Target && asset.State == "missing_pending" && asset.FNOSState == "needs_attention"
	})
	if _, err := a.transferHistoryItem(t.Context(), history.ID); err != nil {
		t.Fatal("optional record deletion was disabled", err)
	}
	configure(root, true)
	logged := func(contains string) bool {
		logs, _ := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
		for _, row := range logs {
			if message, _ := row["message"].(string); strings.Contains(message, contains) {
				return true
			}
		}
		return false
	}
	if err := os.Remove(symlinkSource); err != nil {
		t.Fatal(err)
	}
	privateAwait(t, "the recreated path is queued before its replacement", func() bool { return logged("Recreated.mkv 加入延迟删除队列") })
	if err := os.Symlink(symlinkLink, symlinkSource); err != nil {
		t.Fatal(err)
	}
	time.Sleep(11 * time.Second)
	if _, err := os.Stat(symlinkLink); err != nil || !logged("联动清理失败：permission denied") {
		t.Fatal("the delayed probe did not preserve the still-linked entity at the replaced path", err)
	}
	deletedAt := time.Now()
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	privateAwait(t, "the real watcher queues a delayed task", func() bool { return logged("Guarded.mkv 加入延迟删除队列") })
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	privateAwait(t, "the root move invalidates live monitoring", func() bool { return logged("authorized watch root moved or disappeared") })
	time.Sleep(time.Until(deletedAt.Add(11 * time.Second)))
	if _, err := os.Stat(link); err != nil {
		t.Fatal("a delayed deletion fired after observation was lost", err)
	}
	t.Log("exact installed package reconciled a confirmed video unlink, preserved the re-created entity, and fenced the queued deletion after root identity loss")
}
