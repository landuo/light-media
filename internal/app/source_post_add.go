package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"light-media/internal/download"
)

type sourcePostAddKey struct{}
type sourcePostAddEffect struct {
	Operation, Failure, OriginalReturn          string
	Acknowledged, NotStarted, Rejected, Unknown bool
}
type sourcePostAddReceipt struct {
	Version                      int
	Intent, Hash, InputSHA, Mode string
	Episodes                     []int
	Duplicate, SourceReturned    bool
	SourceError                  string
	Selection                    sourceEpisodeSelectionReceipt
	Effects                      []sourcePostAddEffect
}

func sourcePostAddInputSHA(in Intent) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Version int
		Source  string
	}{in.SourcePostAddVersion, sourceEpisodeInputSHA(in)})))
}

func sourcePostAddReceiptValid(r sourcePostAddReceipt, in Intent) bool {
	if r.Version != 1 || in.SourcePostAddVersion != 1 || r.Intent != in.ID || !sameHash(r.Hash, in.Hash) || r.InputSHA != sourcePostAddInputSHA(in) || !reflect.DeepEqual(r.Episodes, in.SelectedEpisodes) {
		return false
	}
	if r.Mode != "existing_hash" && r.Mode != "add_duplicate" && r.Mode != "new_hash" || r.Duplicate != (r.Mode != "new_hash") {
		return false
	}
	if len(in.SelectedEpisodes) > 0 && !sourceEpisodeReceiptValid(r.Selection, in) {
		return false
	}
	return true
}

func sourcePostAddContext(ctx context.Context, r sourcePostAddReceipt) context.Context {
	ctx = context.WithValue(ctx, sourcePostAddKey{}, r)
	if len(r.Episodes) > 0 {
		ctx = context.WithValue(ctx, sourceEpisodeSelectionKey{}, r.Selection)
	}
	return ctx
}

func withSourcePostAddExistingHash(ctx context.Context, in Intent) context.Context {
	ctx = withSourceEpisodeDuplicate(ctx, in)
	if in.SourcePostAddVersion != 1 {
		return ctx
	}
	r := sourcePostAddReceipt{Version: 1, Intent: in.ID, Hash: in.Hash, InputSHA: sourcePostAddInputSHA(in), Episodes: append([]int(nil), in.SelectedEpisodes...), Mode: "existing_hash", Duplicate: true, SourceReturned: true, Effects: []sourcePostAddEffect{}}
	if len(in.SelectedEpisodes) > 0 {
		r.Selection = ctx.Value(sourceEpisodeSelectionKey{}).(sourceEpisodeSelectionReceipt)
	}
	return sourcePostAddContext(ctx, r)
}

// This receives the actual one-use AddResult, never a hash reconstructed from
// JSON. Every native effect remains separate from the original wrapper return.
func (a *App) applySourceIntentPostAdd(ctx context.Context, client *download.Client, added download.AddResult, in Intent) (context.Context, error) {
	var parse func(string) ([]int, error)
	if len(in.SelectedEpisodes) > 0 {
		var e error
		parse, e = sourceIntentEpisodeParser(ctx)
		if e != nil {
			return ctx, e
		}
	}
	out, sourceErr := client.SourcePostAdd(ctx, added, in.SelectedEpisodes, parse)
	mode := "new_hash"
	if out.Duplicate {
		mode = "add_duplicate"
	}
	r := sourcePostAddReceipt{Version: 1, Intent: in.ID, Hash: out.Hash, InputSHA: sourcePostAddInputSHA(in), Mode: mode, Episodes: append([]int(nil), in.SelectedEpisodes...), Duplicate: out.Duplicate, SourceReturned: sourceErr == nil, Effects: []sourcePostAddEffect{}}
	if sourceErr != nil {
		r.SourceError = a.clean(sourceErr.Error())
	}
	if len(in.SelectedEpisodes) > 0 {
		if out.Duplicate {
			r.Selection = withSourceEpisodeDuplicate(ctx, in).Value(sourceEpisodeSelectionKey{}).(sourceEpisodeSelectionReceipt)
		} else {
			r.Selection = sourceEpisodeSelectionReceipt{Version: 1, Intent: in.ID, Hash: in.Hash, Episodes: append([]int{}, in.SelectedEpisodes...), Mode: "new_paused_hash", InputSHA: sourceEpisodeInputSHA(in), MetadataMissing: out.Selection.MetadataMissing, Started: out.Selection.Started, Effects: []sourceEpisodeSelectionEffect{}}
		}
	}
	for _, effect := range out.Effects {
		failure := ""
		if effect.Failure != nil {
			failure = a.clean(effect.Failure.Error())
		}
		r.Effects = append(r.Effects, sourcePostAddEffect{Operation: effect.Operation, Acknowledged: effect.Acknowledged, Failure: failure, NotStarted: effect.NotStarted, Rejected: effect.Rejected, Unknown: effect.Unknown, OriginalReturn: effect.OriginalReturn})
		if len(in.SelectedEpisodes) > 0 && !out.Duplicate {
			r.Selection.Effects = append(r.Selection.Effects, sourceEpisodeSelectionEffect{effect.Operation, effect.Acknowledged, failure})
		}
	}
	if in.SourcePostAddVersion == 1 {
		// An already-issued operation needs a receipt even if its caller was
		// cancelled. Preserve authorization values, only detach cancellation.
		journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := a.saveSourcePostAddAttempt(journalCtx, in, r); e != nil {
			return ctx, e
		}
	}
	return sourcePostAddContext(ctx, r), sourceErr
}

func (a *App) sourcePostAddReceipt(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, in Intent) (sourcePostAddReceipt, error) {
	var r sourcePostAddReceipt
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_post_add' AND id=?`, in.ID).Scan(&sealed); e != nil {
		return r, bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作记录缺失，请核对原任务")
	}
	raw, e := a.pluginStore.unseal("download_post_add", "system", in.ID, sealed)
	if e != nil || cleanupJournalStrict(raw, &r) != nil || !sourcePostAddReceiptValid(r, in) {
		return r, bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作记录无法核对")
	}
	return r, nil
}

func (a *App) saveSourcePostAddAttempt(ctx context.Context, in Intent, r sourcePostAddReceipt) error {
	if !sourcePostAddReceiptValid(r, in) || r.Mode == "existing_hash" {
		return larkEvidenceError()
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw []byte
	var current Intent
	if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, in.ID).Scan(&raw); e != nil {
		return e
	}
	if json.Unmarshal(raw, &current) != nil || current.State != "post_add" || sourcePostAddInputSHA(current) != r.InputSHA {
		return larkEvidenceError()
	}
	if e = a.putSourcePostAddReceiptTx(ctx, tx, in, r); e != nil {
		return e
	}
	return tx.Commit()
}

func (a *App) putSourcePostAddReceiptTx(ctx context.Context, tx *sql.Tx, in Intent, r sourcePostAddReceipt) error {
	raw := mustCleanSeedJSON(r)
	var previous []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_post_add' AND id=?`, in.ID).Scan(&previous)
	if e == nil {
		accepted, e := a.pluginStore.unseal("download_post_add", "system", in.ID, previous)
		if e != nil || !larkSameRaw(accepted, raw) {
			return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的操作记录与本次提交不同")
		}
		return a.preserveAcquisitionReceiptTx(ctx, tx, in, r)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	sealed, e := a.pluginStore.seal("download_post_add", "system", in.ID, raw)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('download_post_add',?,?,?)`, in.ID, sealed, now())
	if e != nil {
		return e
	}
	return a.preserveAcquisitionReceiptTx(ctx, tx, in, r)
}

func (a *App) saveSourcePostAddTx(ctx context.Context, tx *sql.Tx, in Intent) error {
	if in.SourcePostAddVersion == 0 {
		return nil
	}
	r, ok := ctx.Value(sourcePostAddKey{}).(sourcePostAddReceipt)
	if !ok || !sourcePostAddReceiptValid(r, in) || !r.SourceReturned || r.SourceError != "" {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作尚未正常返回")
	}
	if r.Mode == "existing_hash" {
		return a.putSourcePostAddReceiptTx(ctx, tx, in, r)
	}
	prior, e := a.sourcePostAddReceipt(ctx, tx, in)
	if e != nil {
		return e
	}
	if !larkSameRaw(mustCleanSeedJSON(prior), mustCleanSeedJSON(r)) {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作结果与封存记录不同")
	}
	return nil
}

func (a *App) guardSourcePostAddReceipt(ctx context.Context, in Intent) error {
	if in.SourcePostAddVersion == 0 {
		return nil
	}
	r, e := a.sourcePostAddReceipt(ctx, a.Store.reader(), in)
	if e != nil {
		return e
	}
	if !r.SourceReturned || r.SourceError != "" {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作未正常返回，请核对原任务")
	}
	return nil
}

func (a *App) resumeSourcePostAdd(ctx context.Context, in Intent, j Job) error {
	r, e := a.sourcePostAddReceipt(ctx, a.Store.DB, in)
	if e != nil {
		return e
	}
	if !r.SourceReturned || r.SourceError != "" {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作未正常返回，请核对原任务")
	}
	in.State, in.Error, in.UpdatedAt = "accepted", "", now()
	return a.acceptDownload(sourcePostAddContext(ctx, r), in, j)
}
