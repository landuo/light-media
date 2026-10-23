//go:build linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// The original form contains cloud examples. They must not accidentally become
// local authority when used against this Linux/local-only native owner.
func TestRemoveLinkHostCapabilityRejectsRemoteStorageWithoutEffects(t *testing.T) {
	for _, storage := range []string{"alipan", "u115", "rclone", "alist", "openlist", "unknown"} {
		t.Run(storage, func(t *testing.T) {
			a, item := historyRemovalFixture(t)
			ctx := context.Background()
			source, media := a.Settings().AllowedRoots[0], a.Settings().Roots[0].Path
			cfg := map[string]any{"enabled": true, "monitor_dirs": source, "monitor_strm_deletion": true, "strm_path_mappings": source + ":" + storage + ":" + media, "delete_history": true, "delete_torrents": true, "delete_scrap_infos": true, "notify": true, "delayed_deletion": false}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.removeLinkRoots(raw, nil); !errors.Is(err, cleanuphost.ErrUnsupported) {
				t.Fatalf("nonlocal preflight must reject, not gain local authority: %v", err)
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
			var attempts atomic.Int32
			a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
				attempts.Add(1)
				return nil, errors.New("unexpected watch attempt for unsupported storage")
			}
			run := uninstallInstall(t, a, "RemoveLink", removelink.Manifest(), cfg, true)
			removeLinkInitFailedWorker(t, a, run)
			if attempts.Load() != 0 {
				t.Fatal("unsupported storage reached watcher")
			}
			afterSource, e1 := os.ReadFile(item.Source)
			afterTarget, e2 := os.ReadFile(item.Target)
			if e1 != nil || e2 != nil || !bytes.Equal(beforeSource, afterSource) || !bytes.Equal(beforeTarget, afterTarget) {
				t.Fatal("unsupported storage changed owned media", e1, e2)
			}
			afterHistory, err := a.transferHistoryItem(ctx, item.ID)
			if err != nil || !reflect.DeepEqual(beforeHistory, afterHistory) {
				t.Fatal("unsupported storage changed history", err)
			}
			if len(removeLinkInitLocalNotices(t, a)) != 0 {
				t.Fatal("nonlocal preflight forged a monitor-init diagnostic")
			}
		})
	}
}
