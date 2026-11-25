package app

import (
	"context"
	"path/filepath"
)

// Keep the same newest-500 window as the directory view. The projected columns
// and (directory_id,last_seen) index already exist. Materialize only row keys
// and states; snapshots and other payload fields never cross into the Go heap.
func (a *App) directoryHealth(ctx context.Context, ruleID string) (map[string]any, error) {
	rows, err := a.Store.reader().QueryContext(ctx, `WITH recent AS MATERIALIZED (
		SELECT rowid AS k, coalesce(state,'') AS state,last_seen FROM directory_file
		WHERE directory_id=? ORDER BY last_seen DESC,rowid ASC LIMIT 500
	), samples AS (
		SELECT * FROM recent WHERE state IN ('needs_review','error','observing','baseline')
		ORDER BY last_seen DESC,k ASC LIMIT 5
	)
	SELECT state,n,path,reason FROM (
		SELECT state,count(*) n,'' path,'' reason,NULL stamp,0 k FROM recent GROUP BY state
		UNION ALL SELECT s.state,0,coalesce(f.path,''),coalesce(substr(json_extract(f.payload,'$.reason'),1,80),''),s.last_seen,s.k
		FROM samples s JOIN directory_file f ON f.rowid=s.k
	) ORDER BY n DESC,stamp DESC,k ASC`, ruleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	pending := []map[string]any{}
	for rows.Next() {
		var state, path, reason string
		var n int
		if err := rows.Scan(&state, &n, &path, &reason); err != nil {
			return nil, err
		}
		if n != 0 {
			counts[state] = n
		} else {
			pending = append(pending, map[string]any{"file": filepath.Base(path), "state": state, "reason": reason})
		}
	}
	return map[string]any{"counts": counts, "pending": pending}, rows.Err()
}
