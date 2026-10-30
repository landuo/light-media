package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"light-media/internal/transferextensions"
)

// This is frozen producer context in the existing native import metadata, not
// another current history database. Legacy plans deliberately have no snapshot.
type sharedTransferHistorySnapshot struct {
	Row        plugins.TransferHistoryRecord `json:"row"`
	Final      bool                          `json:"final"`
	JobID      string                        `json:"job_id,omitempty"`
	Generation int                           `json:"generation,omitempty"`
}

func sharedHistoryPtr[T any](v T) *T { return &v }

func sharedHistoryLegacyTransfer(plan ImportPlan, file PlanFile, status string) plugins.TransferHistoryRecord {
	row := plugins.TransferHistoryRecord{ID: file.ID, Source: sharedHistoryPtr(file.Source), SourceStorage: sharedHistoryPtr("local"), Mode: sharedHistoryPtr(originalTransferType(file.Method)), Status: sharedHistoryPtr(status == "success")}
	if file.Target != "" {
		row.Destination = sharedHistoryPtr(file.Target)
		row.DestinationStorage = sharedHistoryPtr("local")
	}
	if file.Reason != "" {
		row.Error = sharedHistoryPtr(file.Reason)
	}
	if plan.Media.ID > 0 {
		row.TMDBID = sharedHistoryPtr(plan.Media.ID)
		row.MediaSource = sharedHistoryPtr("themoviedb")
		row.MediaID = sharedHistoryPtr(strconv.FormatInt(plan.Media.ID, 10))
		row.Title = sharedHistoryPtr(plan.Media.Title)
		if plan.Media.MediaType == "movie" {
			row.Type = sharedHistoryPtr("电影")
		} else if plan.Media.MediaType == "tv" {
			row.Type = sharedHistoryPtr("电视剧")
		}
		if plan.Media.Year != 0 {
			row.Year = sharedHistoryPtr(strconv.Itoa(plan.Media.Year))
		}
		if plan.Media.IMDbID != "" {
			row.IMDbID = sharedHistoryPtr(plan.Media.IMDbID)
		}
		row.TVDBID = plan.Media.TVDBID
	}
	// No fabricated historical date, fileitem, category or episode metadata.
	return row
}

func sharedHistoryFileItem(file PlanFile, path string, stamp importSnapshot) json.RawMessage {
	fields := map[string]any{"path": path, "storage": "local", "children": []any{}}
	for _, key := range []string{"type", "name", "basename", "extension", "size", "modify_time", "fileid", "parent_fileid", "thumbnail", "pickcode", "drive_id", "url"} {
		fields[key] = nil
	}
	name := filepath.Base(path)
	ext := *transferextensions.PathFile(path).Extension
	fields["type"], fields["name"], fields["basename"] = "file", name, strings.TrimSuffix(name, "."+ext)
	fields["extension"], fields["size"], fields["modify_time"] = ext, stamp.Size, float64(stamp.ModNS)/1e9
	if file.DirectoryDigest != "" {
		fields["type"], fields["path"] = "dir", strings.TrimSuffix(path, "/")+"/"
		fields["extension"], fields["size"] = nil, nil
	}
	raw, _ := json.Marshal(fields)
	return raw
}

func (a *App) freezeSharedTransferHistory(ctx context.Context, plan ImportPlan, meta *importMetadata, mediaRaw json.RawMessage, category string) error {
	frozen, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
	if !ok {
		return bad("HISTORY_CONTEXT_MISSING", "整理历史缺少已冻结识别上下文")
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: frozen.options})
	if err != nil {
		return err
	}
	meta.SharedHistory = map[string]sharedTransferHistorySnapshot{}
	for _, file := range plan.Files {
		if file.Status != "ready" {
			continue
		}
		row := sharedHistoryLegacyTransfer(plan, file, "")
		row.Status = nil
		row.Destination = nil
		row.DestinationStorage = nil
		row.Error = nil
		row.SourceFileItem = sharedHistoryFileItem(file, file.Source, planSnapshot(file))
		row.Category = sharedHistoryPtr(category)
		row.Image = sharedHistoryPtr("")
		if plan.Media.PosterPath != "" {
			image, err := sourceImagePath(plan.Media.PosterPath, a.Settings().TMDBImageDomain)
			if err != nil {
				return err
			}
			row.Image = sharedHistoryPtr(strings.ReplaceAll(sourceText(image), "original", "w500"))
		}
		original, hasOriginal := meta.OriginalTransfer[file.ID]
		raw := mediaRaw
		if hasOriginal {
			raw = original.Media
			if original.Item != nil {
				row.SourceFileItem, _ = json.Marshal(original.Item)
			}
		}
		if len(raw) > 0 {
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			encoded, _ := json.Marshal(row)
			var values map[string]json.RawMessage
			_ = json.Unmarshal(encoded, &values)
			for target, source := range map[string]string{"type": "type", "category": "category", "title": "title", "year": "year", "tmdbid": "tmdb_id", "imdbid": "imdb_id", "tvdbid": "tvdb_id", "doubanid": "douban_id", "bangumiid": "bangumi_id", "anilistid": "anilist_id", "media_source": "source", "media_id": "media_id"} {
				if v, exists := fields[source]; exists {
					values[target] = v
				}
			}
			encoded, _ = json.Marshal(values)
			if err = json.Unmarshal(encoded, &row); err != nil {
				return err
			}
			var poster string
			_ = json.Unmarshal(fields["poster_path"], &poster)
			row.Image = sharedHistoryPtr(strings.ReplaceAll(poster, "original", "w500"))
		}
		if hasOriginal && len(original.Meta) > 0 {
			var parsed struct {
				Season  string `json:"season"`
				Episode string `json:"episode"`
			}
			if err = json.Unmarshal(original.Meta, &parsed); err != nil {
				return err
			}
			row.Seasons = sharedHistoryPtr(parsed.Season)
			row.Episodes = sharedHistoryPtr(parsed.Episode)
		} else {
			parsed, err := parser.ParsePath(file.Source, nil)
			if err != nil {
				return err
			}
			if plan.Media.MediaType == "tv" {
				parsed.Set("type", "电视剧")
				parsed.Set("begin_season", file.Season)
				parsed.Set("end_season", nil)
				if len(file.Episodes) == 1 {
					parsed.Set("begin_episode", file.Episodes[0])
					parsed.Set("end_episode", nil)
					parsed.Set("total_episode", 1)
				}
			} else {
				parsed.Set("type", "电影")
			}
			row.Seasons = sharedHistoryPtr(parsed.Season())
			row.Episodes = sharedHistoryPtr(parsed.Episode())
		}
		meta.SharedHistory[file.ID] = sharedTransferHistorySnapshot{Row: row}
	}
	return nil
}

// Called only under the existing importMu held by runImport (including its
// failure defer). Job claims monotonically increment Generation; physical
// admission rejects invalidated jobs before publication. A checkpoint is
// idempotent for one attempt, never a permanent lock on a retryable file.
func finishSharedTransferHistory(meta *importMetadata, job Job, file PlanFile, receipt importReceipt, success bool, message string) {
	value, ok := meta.SharedHistory[file.ID]
	if !ok || value.Final && value.JobID == job.ID && value.Generation >= job.Generation {
		return
	}
	row := &value.Row
	row.Mode = sharedHistoryPtr(originalTransferType(file.Method))
	row.Status = sharedHistoryPtr(success)
	row.Date = sharedHistoryPtr(time.Now().Format("2006-01-02 15:04:05"))
	row.Files = json.RawMessage(`[]`)
	row.Destination = nil
	row.DestinationStorage = nil
	row.DestinationFileItem = json.RawMessage(`null`)
	if success {
		row.Destination = sharedHistoryPtr(file.Target)
		row.DestinationStorage = sharedHistoryPtr("local")
		row.DestinationFileItem = sharedHistoryFileItem(file, file.Target, receipt.StageSnapshot)
		row.Files, _ = json.Marshal([]string{file.Source})
		row.Error = nil
		row.EpisodeGroup = nil
	} else {
		if message == "" {
			message = "未知错误"
		}
		row.Error = sharedHistoryPtr(message)
		if original, ok := meta.OriginalTransfer[file.ID]; ok {
			var fields struct {
				EpisodeGroup *string `json:"episode_group"`
			}
			_ = json.Unmarshal(original.Media, &fields)
			row.EpisodeGroup = fields.EpisodeGroup
		}
	}
	value.Final, value.JobID, value.Generation = true, job.ID, job.Generation
	meta.SharedHistory[file.ID] = value
}

func persistSharedHistoryPlan(ctx context.Context, tx *sql.Tx, plan ImportPlan, meta importMetadata) error {
	if err := txPut(ctx, tx, "import_plan", plan.ID, plan); err != nil {
		return err
	}
	return txPut(ctx, tx, "import_meta", plan.ID, meta)
}
func (a *App) persistSharedHistoryFailedPlan(ctx context.Context, plan ImportPlan, meta importMetadata) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = persistSharedHistoryPlan(ctx, tx, plan, meta); err != nil {
		return err
	}
	return tx.Commit()
}

// executeImport holds importMu and calls this inside its existing queue tx,
// before a new job can replace the latest execution. Capture only a verifiable
// legacy terminal file and its latest terminal job. This adds no new outcome,
// timestamp or unavailable source metadata. Already-running/ambiguous legacy
// states are not reconstructed from an older job's status.
func captureSharedHistoryLegacyTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, meta *importMetadata, selected []int) error {
	changed := false
	for _, index := range selected {
		file := plan.Files[index]
		if _, exists := meta.SharedHistory[file.ID]; exists {
			continue
		}
		if file.Status != "imported" && file.Status != "already_imported" && file.Status != "error" {
			continue
		}
		var raw []byte
		var jobID, jobState, status string
		var generation int
		err := tx.QueryRowContext(ctx, transferHistoryBaseCTE+`SELECT file,job_id,job_state,status,
 (SELECT generation FROM jobs WHERE id=executed_files.job_id)
 FROM executed_files WHERE plan_id=? AND json_extract(file,'$.id')=? AND file_rank=1`, plan.ID, file.ID).Scan(&raw, &jobID, &jobState, &status, &generation)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if jobState != "failed" && jobState != "succeeded" || status == "pending" || generation < 1 {
			continue
		}
		var effective PlanFile
		if err = json.Unmarshal(raw, &effective); err != nil {
			return err
		}
		if effective.Status != "imported" && effective.Status != "already_imported" && effective.Status != "error" {
			continue
		}
		if meta.SharedHistory == nil {
			meta.SharedHistory = map[string]sharedTransferHistorySnapshot{}
		}
		meta.SharedHistory[file.ID] = sharedTransferHistorySnapshot{Row: sharedHistoryLegacyTransfer(plan, effective, status), Final: true, JobID: jobID, Generation: generation}
		changed = true
	}
	if !changed {
		return nil
	}
	return txPut(ctx, tx, "import_meta", plan.ID, *meta)
}
