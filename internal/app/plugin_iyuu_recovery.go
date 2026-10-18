package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

// A committed native checkpoint publishes source config/cache and its original
// immutable run identity atomically. A lost final App reply is recovered from
// that authenticated checkpoint; current config revision never opens a new IO
// timeline for the same run. No HTTP/downloader call occurs here.
func (p *PluginStore) iyuuDeferredOutcome(ctx context.Context, run PluginRun, record pluginDeferredRecord) (json.RawMessage, bool, error) {
	if record.Plugin != "IYUUAutoSeed" || record.Version != "2.19" {
		return nil, false, nil
	}
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	root, _, e := p.iyuuRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return nil, false, e
	}
	if _, e = p.iyuuRootChainTx(ctx, tx, run.InstanceID, run.Generation, root.Journal, true); e != nil {
		return nil, false, e
	}
	fact, ok := root.Runs[run.ID]
	if !ok || fact.Phase != "confirmed" {
		return nil, false, nil
	}
	if fact.Generation != run.Generation || fact.Revision != record.Revision || fact.InputSHA != record.InputSHA || fact.Action != run.Action || fact.Method != record.Method || fact.ResultRevision != run.ConfigRevision {
		return nil, false, iyuu.ErrJournalIntegrity
	}
	if record.Phase == "confirmed" && !bytes.Equal(fact.Result, record.Result) {
		return nil, false, iyuu.ErrJournalIntegrity
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	return append(json.RawMessage{}, fact.Result...), true, nil
}

func iyuuRecoveryErrorCode(err error) string {
	switch {
	case errors.Is(err, iyuu.ErrUnconfirmed):
		return "IYUU_UNCONFIRMED"
	case errors.Is(err, iyuu.ErrJournalIntegrity), errors.Is(err, iyuu.ErrReplayDiverged):
		return "IYUU_EVIDENCE_MISSING"
	default:
		return ""
	}
}
