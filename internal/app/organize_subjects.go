package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"sync"
)

// A negative hint avoids opening the main database from the HTTP request.
// Positive hints (including a rolled-back enqueue) are always checked against
// the authoritative rows. The memory ceiling also bounds bulk-folder submits.
type organizeSubjects struct {
	mu        sync.RWMutex
	known     map[[32]byte]struct{}
	full      bool
	additions int
	version   uint64
}

const organizeSubjectLimit = 4096

func (s *organizeSubjects) lookup(subject string) (bool, uint64) {
	key := sha256.Sum256([]byte(subject))
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.known[key]
	return s.full || s.known == nil || exists, s.version
}

func (s *organizeSubjects) add(subject string) {
	if subject == "" {
		return
	}
	key := sha256.Sum256([]byte(subject))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	if s.known == nil {
		return
	}
	if s.full {
		s.additions++
		return
	}
	if _, ok := s.known[key]; ok {
		return
	}
	if len(s.known) >= organizeSubjectLimit {
		s.full = true
		return
	}
	s.known[key] = struct{}{}
	s.additions++
}

func (s *organizeSubjects) needsRefresh() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.additions >= 256
}

// Only a new writer transaction, before its first mutation, may compact the
// hints. Every other producer registers before releasing that writer. This
// prevents a rollback or racing enqueue from erasing a still-active owner.
const organizeOwnedSubjectsSQL = `WITH active AS MATERIALIZED (
	SELECT id,kind,json_extract(payload,'$.id') AS subject,json_extract(payload,'$._parent_job_id') AS parent
	FROM jobs INDEXED BY jobs_due WHERE state IN ('queued','running','waiting_paused','retry_wait')
	AND kind IN ('organize_file','organize_prepare','import')
)
SELECT m.source FROM active a CROSS JOIN organize_queue_member m INDEXED BY organize_queue_job WHERE m.job_id=a.id
UNION ALL SELECT subject FROM active WHERE kind='organize_prepare'
UNION ALL SELECT json_extract(p.payload,'$.id') FROM active a CROSS JOIN jobs p WHERE p.id=a.parent AND p.kind='organize_prepare'`

func (s *organizeSubjects) restore(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, organizeOwnedSubjectsSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	next := make(map[[32]byte]struct{})
	full := false
	for rows.Next() {
		var subject sql.NullString
		if err = rows.Scan(&subject); err != nil {
			return err
		}
		if !subject.Valid || subject.String == "" {
			continue
		}
		next[sha256.Sum256([]byte(subject.String))] = struct{}{}
		if len(next) > organizeSubjectLimit {
			full = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.known, s.full, s.additions = next, full, 0
	s.version++
	s.mu.Unlock()
	return nil
}

func (a *App) restoreOrganizeSubjects(ctx context.Context) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.organizeInbox.subjects.restore(ctx, tx)
}

// Retrying a job from before startup can reactivate a terminal preparation
// through one of its children. Both file and parent claims must be considered.
func (a *App) rememberRetriedOrganize(ctx context.Context, tx *sql.Tx, job Job) error {
	if a.organizeInbox == nil || job.Kind != "import" && job.Kind != "organize_file" && job.Kind != "organize_prepare" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT source FROM organize_queue_member INDEXED BY organize_queue_job WHERE job_id=?
	UNION ALL SELECT json_extract(payload,'$.id') FROM jobs WHERE kind='organize_prepare' AND id IN (?,?)`, job.ID, job.ID, job.ParentJobID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key sql.NullString
		if err = rows.Scan(&key); err != nil {
			return err
		}
		a.organizeInbox.subjects.add(key.String)
	}
	return rows.Err()
}
