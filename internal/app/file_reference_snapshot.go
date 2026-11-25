package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Only asset and sidecar ownership is reusable. Plans and filesystem identities
// must be observed again for each mutation guard.
type fileReferenceAsset struct {
	Path, SourcePath, RootID, MediaKey string
}
type fileReferenceManaged struct {
	Path, AssetID, RootID, MediaKey string
}
type fileReferenceSnapshot struct {
	version int64
	assets  []fileReferenceAsset
	managed []fileReferenceManaged
	states  map[string]string
}

func fileReferenceVersionSchema() string {
	var b strings.Builder
	b.WriteString("CREATE TABLE IF NOT EXISTS file_reference_version (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL); INSERT OR IGNORE INTO file_reference_version VALUES(1,0);")
	for _, table := range []string{"library_asset", "managed_file"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			b.WriteString("CREATE TRIGGER IF NOT EXISTS reference_" + table + "_" + event + " AFTER " + event + " ON " + table + " BEGIN UPDATE file_reference_version SET version=version+1 WHERE id=1; END;")
		}
	}
	return b.String()
}

func (s *Store) fileReferences(ctx context.Context, tx *sql.Tx) (*fileReferenceSnapshot, error) {
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM file_reference_version WHERE id=1").Scan(&version); err != nil {
		return nil, err
	}
	if snapshot := s.fileReferencesCache.Load(); snapshot != nil && snapshot.version == version {
		return snapshot, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT 0,payload FROM library_asset UNION ALL SELECT 1,payload FROM managed_file")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshot := &fileReferenceSnapshot{version: version, states: map[string]string{}}
	for rows.Next() {
		var kind int
		var raw sql.RawBytes
		if err := rows.Scan(&kind, &raw); err != nil {
			return nil, err
		}
		if kind == 0 {
			var asset Asset
			if err := json.Unmarshal(raw, &asset); err != nil {
				return nil, err
			}
			snapshot.states[asset.ID] = asset.State
			if asset.State != "removed" && asset.State != "superseded" {
				snapshot.assets = append(snapshot.assets, fileReferenceAsset{asset.Path, asset.SourcePath, asset.RootID, mediaKey(asset.Media)})
			}
		} else {
			var file managedFile
			if err := json.Unmarshal(raw, &file); err != nil {
				return nil, err
			}
			snapshot.managed = append(snapshot.managed, fileReferenceManaged{file.Path, file.AssetID, file.RootID, file.MediaKey})
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Concurrent readers may publish different snapshots. Each reader compares
	// the version inside its own transaction before using the cached value.
	s.fileReferencesCache.Store(snapshot)
	return snapshot, nil
}

func (s *Store) mutationReferences(ctx context.Context, protect bool) (*fileReferenceSnapshot, []ImportPlan, error) {
	tx, err := s.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	snapshot := &fileReferenceSnapshot{}
	if protect {
		snapshot, err = s.fileReferences(ctx, tx)
		if err != nil {
			return nil, nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM import_plan INDEXED BY import_plan_state WHERE json_extract(payload,'$.state') IN ('queued','running')`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var plans []ImportPlan
	for rows.Next() {
		var raw sql.RawBytes
		var plan ImportPlan
		if err = rows.Scan(&raw); err != nil {
			return nil, nil, err
		}
		if err = json.Unmarshal(raw, &plan); err != nil {
			return nil, nil, err
		}
		plans = append(plans, plan)
	}
	return snapshot, plans, rows.Err()
}
