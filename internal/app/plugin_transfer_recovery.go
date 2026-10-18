package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	transfer "light-media/internal/plugins/torrenttransfer"
)

// A committed native checkpoint publishes source config/cache and its original
// immutable run identity atomically. A lost final App reply is recovered from
// that authenticated checkpoint; current config revision never opens a new IO
// timeline for the same run. No HTTP/downloader call occurs here.
func (p *PluginStore) transferDeferredOutcome(ctx context.Context, run PluginRun, record pluginDeferredRecord) (json.RawMessage, bool, error) {
	if !isTorrentTransfer(record.Plugin) || record.Version != "1.10.3" {
		return nil, false, nil
	}
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	root, _, e := p.transferRootReadTx(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		return nil, false, e
	}
	if _, e = p.transferRootChainTx(ctx, tx, run.InstanceID, run.Generation, root.Journal, true); e != nil {
		return nil, false, e
	}
	fact, ok := root.Runs[run.ID]
	if !ok || fact.Phase != "confirmed" {
		return nil, false, nil
	}
	if fact.Generation != run.Generation || fact.Revision != record.Revision || fact.InputSHA != record.InputSHA || fact.Action != run.Action || fact.Method != record.Method || fact.ResultRevision != run.ConfigRevision {
		return nil, false, transfer.ErrJournalIntegrity
	}
	if record.Phase == "confirmed" && !bytes.Equal(fact.Result, record.Result) {
		return nil, false, transfer.ErrJournalIntegrity
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	return append(json.RawMessage{}, fact.Result...), true, nil
}

func transferRecoveryErrorCode(err error) string {
	switch {
	case errors.Is(err, transfer.ErrUnconfirmed):
		return "TORRENT_TRANSFER_UNCONFIRMED"
	case errors.Is(err, transfer.ErrJournalIntegrity), errors.Is(err, transfer.ErrReplayDiverged):
		return "TORRENT_TRANSFER_EVIDENCE_MISSING"
	default:
		return ""
	}
}
