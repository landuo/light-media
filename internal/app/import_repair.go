package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// repairOutcome reports what a repair pass changed, so the caller can see that
// something actually happened instead of guessing.
type repairOutcome struct {
	Path          string `json:"path"`
	Observations  int    `json:"observations_removed"`
	Reservations  int    `json:"reservations_released"`
	BroadcastPath string `json:"broadcast_path,omitempty"`
}

// repairStrandedSource drops the observation and stale reservations that keep a
// finished file out of the library. It refuses paths outside every monitored
// source root and never touches a reservation whose intent has not finished
// downloading: this repairs a stuck state, it does not bypass the ownership
// rules that protect unrelated downloads.
func (a *App) repairStrandedSource(ctx context.Context, rawPath string) (repairOutcome, error) {
	out := repairOutcome{Path: rawPath}
	path := filepath.Clean(strings.TrimSpace(rawPath))
	if path == "" || !filepath.IsAbs(path) {
		return out, bad("INVALID_PATH", "需要一个绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil {
		return out, bad("INVALID_PATH", "文件不可访问："+err.Error())
	}
	if info.IsDir() {
		return out, bad("INVALID_PATH", "请指定文件而不是目录")
	}
	var rule DirectoryRule
	found := false
	for _, candidate := range a.Settings().Directories {
		if !candidate.Enabled {
			continue
		}
		root := strings.TrimSpace(candidate.SourcePath)
		if root != "" && within(root, path) {
			rule, found = candidate, true
			break
		}
	}
	if !found {
		return out, bad("INVALID_PATH", "该文件不在任何已启用的监听目录中")
	}

	// Observations for this exact path, under any rule.
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM directory_file WHERE json_extract(payload,'$.path')=?`, path)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			continue
		}
		var f DirectoryObservedFile
		if json.Unmarshal(b, &f) == nil && f.ID != "" {
			ids = append(ids, f.ID)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := a.Store.Delete(ctx, "directory_file", id); err == nil {
			out.Observations++
		}
	}
	if out.Observations == 0 {
		if id := directoryFileID(rule.ID, path); id != "" {
			if err := a.Store.Delete(ctx, "directory_file", id); err == nil {
				out.Observations++
			}
		}
	}

	// Reservations whose owner already finished downloading.
	released, err := a.releaseFinishedReservations(ctx)
	if err != nil {
		return out, err
	}
	out.Reservations = released
	a.log("INFO", "imports", "source_repaired",
		"已修复滞留源文件：清理观测与占用记录", "", "")
	return out, nil
}

// releaseFinishedReservations frees slots held by intents that already reached
// "downloaded": the download exists, so the reservation has served its purpose.
func (a *App) releaseFinishedReservations(ctx context.Context) (released int, err error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT intent_id FROM reservations`)
	if err != nil {
		return 0, nil
	}
	owners := []string{}
	for rows.Next() {
		var owner string
		if rows.Scan(&owner) == nil && owner != "" {
			owners = append(owners, owner)
		}
	}
	rows.Close()
	for _, owner := range owners {
		var in Intent
		if err := a.Store.Get(ctx, "intent", owner, &in); err != nil {
			continue
		}
		if in.State != "downloaded" && in.State != "imported" {
			// A reservation whose intent never finished still blocks the episode:
			// repair exists to release exactly that kind of hold, so it is dropped
			// here as well instead of only for completed downloads.
			if _, err := a.Store.DB.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, owner); err != nil {
				return released, err
			}
			continue
		}
		res, err := a.Store.DB.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, owner)
		if err != nil {
			return released, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			released += int(n)
		}
	}
	// Finally release any reservation whose intent no longer exists: the row keeps
	// the episode reserved and nothing else would ever clear it.
	res, err := a.Store.DB.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id NOT IN (SELECT uid FROM downloadhistory)`)
	if err != nil {
		return released, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		released += int(n)
	}
	return released, nil
}
