package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type organizeQueueMember struct {
	Group  string `json:"group"`
	Meta   string `json:"meta,omitempty"`
	Media  string `json:"media,omitempty"`
	Job    string `json:"job"`
	File   string `json:"file"`
	Source string `json:"source"`
}

// Start at active jobs, then seek their membership. Starting at all archived
// members makes an empty queue walk the whole job history on every UI poll.
const liveOrganizeGroups = `SELECT DISTINCT json_extract(m.payload,'$.group')
 FROM jobs j INDEXED BY jobs_due CROSS JOIN records m INDEXED BY organize_queue_job
 WHERE j.state IN ` + activeOrganizeStates + ` AND m.kind='organize_queue_member'
 AND json_extract(m.payload,'$.job')=(j.id||'')`

func activeOrganizeSourceTx(ctx context.Context, tx *sql.Tx, source string) (string, error) {
	var job string
	err := tx.QueryRowContext(ctx, `SELECT json_extract(m.payload,'$.job') FROM records m
	 JOIN jobs j ON j.id=json_extract(m.payload,'$.job')
	 WHERE m.kind='organize_queue_member' AND json_extract(m.payload,'$.source')=? AND j.state!='cancelled'
	 AND NOT EXISTS(SELECT 1 FROM records c WHERE c.kind='organize_file_cancel' AND c.id=j.id||'/'||json_extract(m.payload,'$.file'))
	 AND json_extract(m.payload,'$.group') IN (`+liveOrganizeGroups+`) LIMIT 1`, source).Scan(&job)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return job, err
}

// Persist MP's meta -> media group relationship alongside the durable queue.
// Completed/failed members remain visible while any related member is active;
// a later submission starts a new group after the previous one has ended.
func registerOrganizeQueueTx(ctx context.Context, tx *sql.Tx, job Job) error {
	if job.Kind != "organize_file" && job.Kind != "import" {
		return nil
	}
	var members []organizeQueueMember
	if job.Kind == "organize_file" {
		var payload organizeFilePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		season := 1
		if payload.File.Parsed.Original != nil && payload.File.Parsed.Original.Season != nil {
			season = *payload.File.Parsed.Original.Season
		}
		members = append(members, organizeQueueMember{Job: job.ID, File: job.ID, Source: payload.File.Path, Meta: fmt.Sprintf("%s\x00%d", payload.Title, season)})
	} else {
		var payload struct {
			ID      string   `json:"id"`
			FileIDs []string `json:"file_ids"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='import_plan' AND id=?`, payload.ID).Scan(&raw); err != nil {
			return err
		}
		var plan ImportPlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			return err
		}
		for _, file := range plan.Files {
			if len(payload.FileIDs) > 0 && !slices.Contains(payload.FileIDs, file.ID) {
				continue
			}
			member := organizeQueueMember{Job: job.ID, File: file.ID, Source: file.Source}
			if plan.Media.Title != "" {
				member.Media = fmt.Sprintf("%s\x00%d", mediaKey(plan.Media), file.Season)
			}
			members = append(members, member)
		}
	}
	for _, member := range members {
		key := tokenHash(job.ID + "\x00" + member.Source)
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='organize_queue_member' AND id=?`, key).Scan(&raw)
		if err == nil {
			var previous organizeQueueMember
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
			member.Group, member.Meta = previous.Group, previous.Meta
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if job.Kind == "import" && member.Media == "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE kind='organize_queue_member' AND id=?`, key); err != nil {
				return err
			}
			continue // MP removes recognition failures directly; history retains them.
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT json_extract(payload,'$.group') FROM records WHERE kind='organize_queue_member'
		 AND ((?<>'' AND json_extract(payload,'$.meta')=?) OR (?<>'' AND json_extract(payload,'$.media')=?))
		 AND json_extract(payload,'$.group') IN (`+liveOrganizeGroups+`)`, member.Meta, member.Meta, member.Media, member.Media)
		if err != nil {
			return err
		}
		groups := []string{}
		for rows.Next() {
			var group string
			if err := rows.Scan(&group); err != nil {
				rows.Close()
				return err
			}
			groups = append(groups, group)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if member.Group == "" && len(groups) > 0 {
			member.Group = groups[0]
		}
		if member.Group == "" {
			member.Group = id("queue_group_")
		}
		for _, group := range groups {
			if group == member.Group {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.group',?) WHERE kind='organize_queue_member' AND json_extract(payload,'$.group')=?`, member.Group, group); err != nil {
				return err
			}
		}
		if err := txPut(ctx, tx, "organize_queue_member", key, member); err != nil {
			return err
		}
	}
	return nil
}

// Old versions did not persist display membership. Seed only their active jobs,
// leaving historic completed batches outside the current queue.
func (a *App) seedOrganizeQueue(ctx context.Context) error {
	jobs, err := a.queryJobs(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind IN ('import','organize_file') AND state IN `+activeOrganizeStates+`
	 AND NOT EXISTS(SELECT 1 FROM records m INDEXED BY organize_queue_job WHERE m.kind='organize_queue_member' AND json_extract(m.payload,'$.job')=(jobs.id||''))`)
	if err != nil || len(jobs) == 0 {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, job := range jobs {
		if err := registerOrganizeQueueTx(ctx, tx, job); err != nil {
			return err
		}
	}
	return tx.Commit()
}
