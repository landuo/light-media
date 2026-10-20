package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"light-media/internal/releasemeta"
)

type sourceEpisodeSelectionKey struct{}
type sourceEpisodeSelectionEffect struct {
	Operation    string
	Acknowledged bool
	Failure      string
}
type sourceEpisodeSelectionReceipt struct {
	Version         int
	Intent, Hash    string
	Episodes        []int
	Mode            string
	InputSHA        string
	MetadataMissing bool
	Started         bool
	Effects         []sourceEpisodeSelectionEffect
}

func withSourceEpisodeDuplicate(ctx context.Context, in Intent) context.Context {
	if len(in.SelectedEpisodes) == 0 {
		return ctx
	}
	return context.WithValue(ctx, sourceEpisodeSelectionKey{}, sourceEpisodeSelectionReceipt{Version: 1, Intent: in.ID, Hash: in.Hash, Episodes: append([]int{}, in.SelectedEpisodes...), Mode: "existing_hash", InputSHA: sourceEpisodeInputSHA(in)})
}

func sourceIntentEpisodeParser(ctx context.Context) (func(string) ([]int, error), error) {
	scope, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
	if !ok {
		return nil, bad("DOWNLOAD_CONTEXT_MISSING", "按集下载缺少原始资源识别上下文")
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: scope.options})
	if e != nil {
		return nil, e
	}
	return func(name string) ([]int, error) {
		meta, e := parser.ParseTitle(name, nil, nil)
		if e != nil {
			return nil, e
		}
		values, e := meta.EpisodeList()
		if e != nil {
			return nil, e
		}
		episodes := []int{}
		for _, n := range values {
			if !n.IsInt64() || n.Sign() < 0 || n.Int64() > int64(^uint(0)>>1) {
				return nil, bad("INVALID_EPISODE_SELECTION", "下载器文件的集号超出本机整数范围")
			}
			episodes = append(episodes, int(n.Int64()))
		}
		return episodes, nil
	}, nil
}

func sourceEpisodeInputSHA(in Intent) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Intent, Hash, Downloader, DownloaderSnapshot, Path, Directory, DirectorySnapshot, Root, Media, Preview, Material, ImportMode string
		Season                                                                                                                       int
		Episodes, Selected                                                                                                           []int
		SeasonScopeSHA                                                                                                               string `json:",omitempty"`
	}{in.ID, in.Hash, in.DownloaderID, in.DownloaderSnapshot, in.DownloadPath, in.DirectoryID, in.DirectorySnapshot, in.RootID, mediaKey(in.Media), in.PreviewID, in.MaterialRef, in.ImportMode, in.Season, in.Episodes, in.SelectedEpisodes, in.SeasonScopeSHA})))
}
func sourceEpisodeReceiptValid(r sourceEpisodeSelectionReceipt, in Intent) bool {
	return r.Version == 1 && r.Intent == in.ID && sameHash(r.Hash, in.Hash) && reflect.DeepEqual(r.Episodes, in.SelectedEpisodes) && r.InputSHA == sourceEpisodeInputSHA(in) && (r.Mode == "existing_hash" || r.Mode == "new_paused_hash")
}

func (a *App) guardSourceEpisodeReceipt(ctx context.Context, in Intent) error {
	if e := a.guardSourcePostAddReceipt(ctx, in); e != nil {
		return e
	}
	if len(in.SelectedEpisodes) == 0 {
		return nil
	}
	var sealed []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_episode_selection' AND id=?`, in.ID).Scan(&sealed); e != nil {
		return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "按集下载的原文件选择记录缺失")
	}
	raw, e := a.pluginStore.unseal("download_episode_selection", "system", in.ID, sealed)
	var r sourceEpisodeSelectionReceipt
	if e != nil || cleanupJournalStrict(raw, &r) != nil || !sourceEpisodeReceiptValid(r, in) {
		return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "按集下载的原文件选择记录无法核对")
	}
	return nil
}

func (a *App) saveSourceEpisodeSelectionTx(ctx context.Context, tx *sql.Tx, in Intent) error {
	if e := a.saveSourcePostAddTx(ctx, tx, in); e != nil {
		return e
	}
	if len(in.SelectedEpisodes) == 0 {
		return nil
	}
	r, ok := ctx.Value(sourceEpisodeSelectionKey{}).(sourceEpisodeSelectionReceipt)
	if !ok || !sourceEpisodeReceiptValid(r, in) {
		return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "按集下载缺少对应资源的原文件选择结果")
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	var previous []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_episode_selection' AND id=?`, in.ID).Scan(&previous)
	if e == nil {
		accepted, e := a.pluginStore.unseal("download_episode_selection", "system", in.ID, previous)
		if e != nil || !larkSameRaw(accepted, raw) {
			return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "原文件选择结果与本次提交不同")
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	sealed, e := a.pluginStore.seal("download_episode_selection", "system", in.ID, raw)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('download_episode_selection',?,?,?)`, in.ID, sealed, now())
	return e
}
