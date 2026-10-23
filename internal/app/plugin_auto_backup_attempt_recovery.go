package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
)

// A native cancellation is not success and never authorizes replay. Validate
// the existing native root and exact receipt before binding its immutable
// per-run evidence to the common attempt recovery attestation.
func (j *pluginRunAttemptJournal) autoBackupCanceledEvidenceTx(ctx context.Context, tx *sql.Tx, head pluginRunAttemptStored, run PluginRun) (string, [sha256.Size]byte, bool, error) {
	var zero [sha256.Size]byte
	a := head.claim.Admission
	if a.PluginID != "AutoBackup" || a.Version != "2.1.4" || run.State != "canceled" {
		return "", zero, false, nil
	}
	d, sealed, err := j.p.readDeferred(ctx, tx, run)
	if err != nil {
		return "", zero, false, err
	}
	if d.Plugin != a.PluginID || d.Version != a.Version || d.Generation != a.Generation || d.Action != a.Action || d.Revision != run.ConfigRevision || d.InputSHA != deferredInputSHA(run.Input) {
		return "", zero, false, errPluginRunAttemptEvidence
	}
	if _, err = j.p.verifyRunConfigChainTx(ctx, tx, d.Plugin, run, d.Revision, d.InputSHA, d.ConfigSHA); err != nil {
		return "", zero, false, err
	}
	s, _, err := j.p.autoBackupRead(ctx, tx, run.InstanceID, run.Generation)
	if err != nil {
		return "", zero, false, err
	}
	action, ok := s.Runs[run.ID]
	if !ok || action.RunID != run.ID || action.Action != run.Action || action.Revision != run.ConfigRevision || action.InputSHA != d.InputSHA || action.Method != d.Method {
		return "", zero, false, errPluginRunAttemptEvidence
	}
	switch d.Phase {
	case "snapshot_cut":
		if run.ErrorCode != "AUTO_BACKUP_SNAPSHOT_CUT" || !validAutoBackupCut(action, s.Instance, s.Generation) || !validAutoBackupDeferredCut(d) || !bytes.Equal(action.Result, d.Result) || !bytes.Equal(run.Result, d.Result) {
			return "", zero, false, errPluginRunAttemptEvidence
		}
	case "abandoned_unknown":
		if run.ErrorCode != "AUTO_BACKUP_ABANDONED_UNKNOWN" || !validAutoBackupResolution(action, s.Instance, s.Generation) || !validAutoBackupDeferredAbandon(d) || !bytes.Equal(mustCleanSeedJSON(action.Resolution), d.Result) {
			return "", zero, false, errPluginRunAttemptEvidence
		}
	default:
		return "", zero, false, nil
	}
	// Do not hash the whole evolving owner root: later independent runs may
	// extend it. Its authenticated per-run action and receipt remain immutable.
	raw, err := json.Marshal(struct {
		Action  autoBackupAction
		Receipt []byte
	}{action, sealed})
	if err != nil {
		return "", zero, false, err
	}
	return "auto-backup-canceled/" + d.Phase + "/v1", sha256.Sum256(raw), true, nil
}
