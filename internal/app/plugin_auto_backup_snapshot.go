package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"

	"light-media/internal/plugins"
)

// This tombstone is written only to the owned VACUUM snapshot. It is a cut
// before this run's archive/retention/WebDAV actions, never a success receipt.
type autoBackupSnapshotCut struct {
	Version                                                                   int
	RunID, Instance, Action, InputSHA, Method, OccurrenceID, EffectSHA, Point string
	Generation, Revision                                                      int64
}

func validAutoBackupCut(r autoBackupAction, instance string, generation int64) bool {
	cut := r.Cut
	return cut != nil && cut.Version == 1 && cut.RunID == r.RunID && cut.Instance == instance && cut.Generation == generation && cut.Revision == r.Revision && cut.Action == r.Action && cut.InputSHA == r.InputSHA && cut.Method == r.Method && cut.Point == "before_archive_retention_webdav" && len(r.Effects) == 1 && r.Effects[0].Kind == "sqlite_snapshot" && r.Effects[0].Phase == "started" && cut.EffectSHA == tokenHash(string(mustCleanSeedJSON(r.Effects[0]))) && r.ErrorCode == "AUTO_BACKUP_SNAPSHOT_CUT" && json.Valid(r.Result)
}
func validAutoBackupDeferredCut(r pluginDeferredRecord) bool {
	var cut autoBackupSnapshotCut
	return r.Plugin == "AutoBackup" && r.Version == "2.1.4" && r.ErrorCode == "AUTO_BACKUP_SNAPSHOT_CUT" && cleanupJournalStrict(r.Result, &cut) == nil && cut.Version == 1 && cut.RunID == r.Run && cut.Instance == r.Instance && cut.Generation == r.Generation && cut.Revision == r.Revision && cut.Action == r.Action && cut.InputSHA == r.InputSHA && cut.Method == r.Method && cut.Point == "before_archive_retention_webdav" && pluginStorageDigest.MatchString(cut.EffectSHA)
}
func (c *autoBackupCall) snapshotCut(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=rw"
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// The original VACUUM target is private and has no shared writer. Set rollback
	// journaling before normalization so closing it leaves a standalone DB file.
	if _, e = db.ExecContext(ctx, `PRAGMA journal_mode=DELETE;PRAGMA synchronous=FULL;`); e != nil {
		return e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	p := &PluginStore{store: &Store{DB: db, key: append([]byte(nil), c.o.a.Store.key...)}}
	actual, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, c.run.ID))
	if e != nil || actual.State != "running" || actual.InstanceID != c.run.InstanceID || actual.Generation != c.run.Generation || actual.ConfigRevision != c.run.ConfigRevision || !bytes.Equal(actual.Input, c.run.Input) {
		return errors.New("owned snapshot original admission mismatch")
	}
	s, previous, e := p.autoBackupRead(ctx, tx, c.o.instance, c.o.generation)
	if e != nil {
		return e
	}
	r, ok := s.Runs[c.run.ID]
	// A snapshot made after any other effect is not proven safe to normalize.
	if !ok || r.Phase != "started" || len(r.Effects) != 1 || r.Effects[0].Kind != "sqlite_snapshot" || r.Effects[0].Phase != "started" || r.InputSHA != deferredInputSHA(c.run.Input) || r.Revision != c.run.ConfigRevision || r.Method != c.method {
		return errors.New("snapshot cut is not before external backup effects")
	}
	deferred, old, e := p.readDeferred(ctx, tx, c.run)
	if e != nil || deferred.Plugin != "AutoBackup" || deferred.Phase != "started" || deferred.Method != c.method || deferred.InputSHA != r.InputSHA || deferred.Revision != r.Revision {
		return errors.New("owned snapshot deferred admission mismatch")
	}
	cut := autoBackupSnapshotCut{Version: 1, RunID: r.RunID, Instance: c.o.instance, Generation: c.o.generation, Revision: r.Revision, Action: r.Action, InputSHA: r.InputSHA, Method: r.Method, Point: "before_archive_retention_webdav", EffectSHA: tokenHash(string(mustCleanSeedJSON(r.Effects[0])))}
	if c.run.Action == "once" || c.run.Action == "service" {
		var inv plugins.Invocation
		var token struct {
			ID string `json:"event_id"`
		}
		if cleanupJournalStrict(c.run.Input, &inv) != nil || cleanupJournalStrict(inv.Input, &token) != nil {
			return errors.New("snapshot occurrence input mismatch")
		}
		var occurrence string
		if e = tx.QueryRowContext(ctx, `SELECT id FROM plugin_schedule_occurrences WHERE id=? AND run_id=? AND instance_id=? AND generation=? AND config_revision=? AND state='running'`, token.ID, c.run.ID, c.run.InstanceID, c.run.Generation, c.run.ConfigRevision).Scan(&occurrence); e != nil {
			return e
		}
		cut.OccurrenceID = occurrence
		if _, e = tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='canceled' WHERE id=? AND run_id=?`, occurrence, c.run.ID); e != nil {
			return e
		}
	}
	body, e := json.Marshal(cut)
	if e != nil {
		return e
	}
	r.Phase = "snapshot_cut"
	r.ErrorCode = "AUTO_BACKUP_SNAPSHOT_CUT"
	r.Result = body
	r.Cut = &cut
	s.Runs[c.run.ID] = r
	if !validAutoBackupCut(r, s.Instance, s.Generation) {
		return errors.New("invalid backup snapshot tombstone")
	}
	if e = p.autoBackupSave(ctx, tx, s, previous); e != nil {
		return e
	}
	deferred.Phase = "snapshot_cut"
	deferred.ErrorCode = r.ErrorCode
	deferred.Result = body
	if e = p.writeDeferred(ctx, tx, deferred, old); e != nil {
		return e
	}
	sealed, e := p.seal("run_result", c.run.InstanceID, c.run.ID, body)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code=?,result=? WHERE id=? AND state='running'`, r.ErrorCode, sealed, c.run.ID); e != nil {
		return e
	}
	return tx.Commit()
}
