package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

const transferHistoryMPColumns = `id,src,src_fileitem,dest,dest_fileitem,mode,type,category,title,year,
tmdbid,media_source,media_id,seasons,episodes,status,errmsg,date,files,episode_group`

type transferHistoryMPRow struct {
	ID           int64
	Src          string
	SrcFileItem  []byte
	Dest         sql.NullString
	DestFileItem []byte
	Mode         sql.NullString
	Type         sql.NullString
	Category     sql.NullString
	Title        sql.NullString
	Year         sql.NullString
	TMDBID       sql.NullInt64
	MediaSource  sql.NullString
	MediaID      sql.NullString
	Seasons      sql.NullString
	Episodes     sql.NullString
	Status       int
	Errmsg       sql.NullString
	Date         string
	Files        []byte
	EpisodeGroup sql.NullString
}

func transferHistoryMPEpisodeRange(text string) []int {
	if text == "" {
		return nil
	}
	parts := strings.SplitN(text, "-", 2)
	parse := func(value string) int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "E")
		number, _ := strconv.Atoi(value)
		return number
	}
	first := parse(parts[0])
	if first <= 0 {
		return nil
	}
	if len(parts) == 1 {
		return []int{first}
	}
	last := parse(parts[1])
	if last < first || last-first > 9999 {
		return []int{first}
	}
	out := make([]int, 0, last-first+1)
	for episode := first; episode <= last; episode++ {
		out = append(out, episode)
	}
	return out
}

func (row transferHistoryMPRow) item() transferHistoryItem {
	var internal struct {
		PlanID  string `json:"plan_id"`
		FileID  string `json:"file_id"`
		AssetID string `json:"asset_id"`
		JobID   string `json:"job_id"`
		Size    int64  `json:"size"`
	}
	_ = json.Unmarshal(row.SrcFileItem, &internal)
	kind := "movie"
	if row.Type.String == "电视剧" {
		kind = "tv"
	}
	season := 0
	if strings.HasPrefix(row.Seasons.String, "S") {
		season, _ = strconv.Atoi(strings.TrimPrefix(row.Seasons.String, "S"))
	}
	episodes := transferHistoryMPEpisodeRange(row.Episodes.String)
	var files []string
	_ = json.Unmarshal(row.Files, &files)
	status, fileStatus := "failed", "error"
	if row.Status == 1 {
		status, fileStatus = "success", "imported"
	}
	created := row.Date
	media := catalog.Media{ID: row.TMDBID.Int64, MediaType: kind, Title: row.Title.String, Year: func() int { year, _ := strconv.Atoi(row.Year.String); return year }(), ProviderSource: row.MediaSource.String, ProviderID: row.MediaID.String}
	item := transferHistoryItem{
		ID: strconv.FormatInt(row.ID, 10), FileID: internal.FileID, ResourceSource: row.Src,
		PlanID: internal.PlanID, JobID: internal.JobID, JobState: map[bool]string{true: "succeeded", false: "failed"}[row.Status == 1], Media: media, Category: row.Category.String,
		EpisodeGroup: row.EpisodeGroup.String, Season: season, Episodes: episodes,
		Source: row.Src, Target: row.Dest.String, Mode: row.Mode.String, Size: internal.Size,
		Status: status, FileStatus: fileStatus, Error: row.Errmsg.String,
		CreatedAt: created, UpdatedAt: created, CanRetry: row.Status == 0,
		CanRecognize: row.Src != "",
	}
	item.mpRecord = true
	return item
}

func (a *App) scanTransferHistoryMP(row interface{ Scan(...any) error }) (transferHistoryItem, error) {
	var value transferHistoryMPRow
	if err := row.Scan(&value.ID, &value.Src, &value.SrcFileItem, &value.Dest, &value.DestFileItem,
		&value.Mode, &value.Type, &value.Category, &value.Title, &value.Year, &value.TMDBID,
		&value.MediaSource, &value.MediaID, &value.Seasons, &value.Episodes, &value.Status,
		&value.Errmsg, &value.Date, &value.Files, &value.EpisodeGroup); err != nil {
		return transferHistoryItem{}, err
	}
	return value.item(), nil
}

// transferHistoryMPPage is the MoviePilot-shaped list reader for the single
// table. It is exercised while the legacy projection still serves requests and
// becomes the production reader once the switch lands.
func (a *App) transferHistoryMPPage(ctx context.Context, page, size int) ([]transferHistoryItem, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 1000 {
		size = 50
	}
	var total int
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM transferhistory`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory ORDER BY date DESC,id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []transferHistoryItem{}
	for rows.Next() {
		item, err := a.scanTransferHistoryMP(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (a *App) transferHistoryMPFiltered(ctx context.Context, page, size int, status, query, pattern string) ([]transferHistoryItem, int, error) {
	predicates := []string{}
	args := []any{}
	if status == "success" {
		predicates = append(predicates, "status=1")
	} else if status == "failed" {
		predicates = append(predicates, "status=0")
	}
	if query != "" {
		predicates = append(predicates, `(lower(COALESCE(title,'')) LIKE lower(?) ESCAPE '\' OR lower(COALESCE(src,'')) LIKE lower(?) ESCAPE '\' OR lower(COALESCE(dest,'')) LIKE lower(?) ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	where := ""
	if len(predicates) > 0 {
		where = " WHERE " + strings.Join(predicates, " AND ")
	}
	var total int
	if err := a.Store.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM transferhistory`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		// The API contract is a JSON array; a nil slice would marshal to null and
		// the history view rejects a non-iterable items field.
		return []transferHistoryItem{}, 0, nil
	}
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory`+where+` ORDER BY date DESC,id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []transferHistoryItem{}
	for rows.Next() {
		item, err := a.scanTransferHistoryMP(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (a *App) transferHistoryMPByID(ctx context.Context, id string) (transferHistoryItem, bool, error) {
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil || number <= 0 {
		return transferHistoryItem{}, false, nil
	}
	item, err := a.scanTransferHistoryMP(a.Store.reader().QueryRowContext(ctx, `SELECT `+transferHistoryMPColumns+` FROM transferhistory WHERE id=?`, number))
	if errors.Is(err, sql.ErrNoRows) {
		return transferHistoryItem{}, false, nil
	}
	if err != nil {
		return transferHistoryItem{}, false, err
	}
	return item, true, nil
}
