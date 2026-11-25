package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Older callbacks and URLs use the execution file ID stored inside the FileItem.
const transferHistoryFileIDExpr = `CASE WHEN json_valid(src_fileitem) THEN json_extract(src_fileitem,'$.file_id') END`

// MoviePilot keeps one transferhistory row per source: add_force deletes the
// previous row for src and inserts a new one, so every completed attempt gets a
// new ID. This table is the target of the single-table refactor and is written
// alongside the legacy projection until all readers are switched.
func transferHistoryMPSchema() string {
	return `
CREATE TABLE IF NOT EXISTS transferhistory (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 src TEXT NOT NULL,
 src_storage TEXT NOT NULL DEFAULT '',
 src_fileitem TEXT NOT NULL DEFAULT '{}',
 dest TEXT,
 dest_storage TEXT,
 dest_fileitem TEXT,
 mode TEXT,
 type TEXT,
 category TEXT,
 title TEXT,
 year TEXT,
 tmdbid INTEGER,
 imdbid TEXT,
 tvdbid INTEGER,
 doubanid TEXT,
 bangumiid INTEGER,
 anilistid INTEGER,
 media_source TEXT,
 media_id TEXT,
 seasons TEXT,
 episodes TEXT,
 image TEXT,
 downloader TEXT,
 download_hash TEXT,
 status INTEGER NOT NULL DEFAULT 1,
 errmsg TEXT,
 date TEXT NOT NULL,
 files TEXT NOT NULL DEFAULT '[]',
 episode_group TEXT
);
CREATE INDEX IF NOT EXISTS ix_transfer_history_src ON transferhistory(src);
CREATE INDEX IF NOT EXISTS ix_transfer_history_file_id ON transferhistory((` + transferHistoryFileIDExpr + `),id DESC)
 WHERE (` + transferHistoryFileIDExpr + `) IS NOT NULL;
CREATE INDEX IF NOT EXISTS ix_transfer_history_title ON transferhistory(title);
CREATE INDEX IF NOT EXISTS ix_transfer_history_tmdbid ON transferhistory(tmdbid);
CREATE INDEX IF NOT EXISTS ix_transfer_history_download_hash ON transferhistory(download_hash);
CREATE INDEX IF NOT EXISTS ix_transfer_history_status_date ON transferhistory(status,date);
CREATE INDEX IF NOT EXISTS ix_transfer_history_date_id ON transferhistory(date,id);
CREATE INDEX IF NOT EXISTS ix_transfer_history_media_identity ON transferhistory(media_source,media_id);
`
}

type transferHistoryMPInput struct {
	Plan       ImportPlan
	File       PlanFile
	Job        Job
	Success    bool
	Message    string
	CallbackID string
	Downloader string
	Hash       string
	Image      string
	Files      []string
}

func transferHistoryMPType(mediaType string) string {
	if mediaType == "tv" {
		return "电视剧"
	}
	return "电影"
}

func transferHistoryMPSeasons(season int) string {
	if season <= 0 {
		return ""
	}
	return fmt.Sprintf("S%02d", season)
}

func transferHistoryMPEpisodes(episodes []int) string {
	if len(episodes) == 0 {
		return ""
	}
	if len(episodes) == 1 {
		return fmt.Sprintf("E%02d", episodes[0])
	}
	return fmt.Sprintf("E%02d-E%02d", episodes[0], episodes[len(episodes)-1])
}

func transferHistoryMPFileItem(file PlanFile, internal map[string]any) json.RawMessage {
	item := map[string]any{"storage": "local", "path": file.Source, "type": "file", "name": file.Source, "size": file.Size}
	if file.Target != "" {
		item["target"] = file.Target
	}
	for key, value := range internal {
		item[key] = value
	}
	raw, _ := json.Marshal(item)
	return raw
}

// writeTransferHistoryMPTx applies MoviePilot's add_force semantics inside the
// caller's execution transaction: delete the row for this src, then insert a new
// row with a new auto-increment ID. Previews and running attempts never call it.
func writeTransferHistoryMPTx(ctx context.Context, tx *sql.Tx, in transferHistoryMPInput) error {
	src := in.File.Source
	if src == "" {
		return nil
	}
	internal := map[string]any{"plan_id": in.Plan.ID, "file_id": in.File.ID, "asset_id": in.File.AssetID, "root_id": in.Plan.RootID, "workflow_id": in.Plan.WorkflowID, "job_id": in.Job.ID}
	if in.CallbackID != "" {
		internal["callback_id"] = in.CallbackID
	}
	srcItem := transferHistoryMPFileItem(in.File, internal)
	destItem := json.RawMessage(`null`)
	if in.File.Target != "" {
		destFile := in.File
		destFile.Source = in.File.Target
		destItem = transferHistoryMPFileItem(destFile, internal)
	}
	files := in.Files
	if len(files) == 0 {
		files = []string{in.File.Target}
	}
	filesRaw, _ := json.Marshal(files)
	status := 0
	if in.Success {
		status = 1
	}
	year := ""
	if in.Plan.Media.Year > 0 {
		year = strconv.Itoa(in.Plan.Media.Year)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transferhistory WHERE src=?`, src); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO transferhistory
 (src,src_storage,src_fileitem,dest,dest_storage,dest_fileitem,mode,type,category,title,year,
  tmdbid,imdbid,tvdbid,doubanid,bangumiid,anilistid,media_source,media_id,seasons,episodes,image,
  downloader,download_hash,status,errmsg,date,files,episode_group)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		src, "local", string(srcItem), in.File.Target, "local", string(destItem),
		firstNonEmpty(in.File.Method, in.Plan.Mode), transferHistoryMPType(in.Plan.Media.MediaType),
		in.Plan.Category, in.Plan.Media.Title, year,
		in.Plan.Media.ID, in.Plan.Media.IMDbID, 0, "", 0, 0,
		in.Plan.Media.ProviderSource, in.Plan.Media.ProviderID,
		transferHistoryMPSeasons(historySeason(in.File)), transferHistoryMPEpisodes(historyEpisodes(in.File)), in.Image,
		in.Downloader, in.Hash, status, in.Message, time.Now().Format("2006-01-02 15:04:05"),
		string(filesRaw), in.Plan.EpisodeGroup)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func historySeason(file PlanFile) int {
	if file.GroupNumbering != nil && file.GroupNumbering.Season > 0 {
		return file.GroupNumbering.Season
	}
	return file.Season
}

func historyEpisodes(file PlanFile) []int {
	if file.GroupNumbering != nil && len(file.GroupNumbering.Episodes) > 0 {
		return file.GroupNumbering.Episodes
	}
	return file.Episodes
}
