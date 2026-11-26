package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Accepted organizer requests share the data volume, but not the main writer.
// A FULL-synchronous commit is required before the HTTP caller receives an ID.
// Promotion is repeatable after a crash: the main job and audit commit first,
// then the corresponding inbox receipts are removed.
type organizeInbox struct {
	subjects organizeSubjects
	db       *sql.DB
	wake     chan struct{}
	// Serialize cancellation with the handoff of the same job, without holding
	// the inbox writer while the main database waits for disk.
	handoffs [32]sync.Mutex
}

func openOrganizeInbox(dir string) (*organizeInbox, error) {
	path := filepath.Join(dir, "organize-inbox.db")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	file.Close()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("整理受理日志不是普通文件")
	}
	addr := url.URL{Scheme: "file", Path: path}
	query := addr.Query()
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "cache_size(-128)")
	query.Add("_pragma", "busy_timeout(5000)")
	addr.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", addr.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL;
	CREATE TABLE IF NOT EXISTS request(id TEXT PRIMARY KEY, kind TEXT NOT NULL, subject TEXT NOT NULL, request_key TEXT NOT NULL, job BLOB NOT NULL, state TEXT NOT NULL);
	CREATE UNIQUE INDEX IF NOT EXISTS active_subject ON request(kind,subject) WHERE state='queued';
	CREATE TABLE IF NOT EXISTS audit(id TEXT PRIMARY KEY, request_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS file_batch(id TEXT PRIMARY KEY, listings BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS batch_member(job_id TEXT PRIMARY KEY, batch_id TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS batch_members ON batch_member(batch_id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &organizeInbox{db: db, wake: make(chan struct{}, 1)}, nil
}

func (inbox *organizeInbox) signal() {
	select {
	case inbox.wake <- struct{}{}:
	default:
	}
}

type organizeRowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Check the ordinary queue as well as the inbox: a retry may arrive after the
// file has been promoted from recognition to transfer, or after its parent
// has enumerated children but before they have finished.
func activeInteractiveJob(ctx context.Context, q organizeRowReader, kind, subject string) (id, key string, err error) {
	if kind == "organize_file" {
		err = q.QueryRowContext(ctx, `SELECT j.id,COALESCE(CASE j.kind WHEN 'organize_file' THEN json_extract(j.payload,'$.id') ELSE json_extract(j.payload,'$.interactive_key') END,'')
			FROM organize_queue_member m INDEXED BY organize_queue_source CROSS JOIN jobs j ON j.id=m.job_id WHERE m.source=? AND j.state IN `+activeOrganizeStates+` LIMIT 1`, subject).Scan(&id, &key)
	} else {
		key = subject
		err = q.QueryRowContext(ctx, `SELECT id FROM jobs WHERE kind='organize_prepare' AND json_extract(payload,'$.id')=? AND (state IN `+activeOrganizeStates+` OR EXISTS(SELECT 1 FROM jobs child WHERE child.kind IN ('organize_file','import') AND child.state IN `+activeOrganizeStates+` AND json_extract(child.payload,'$._parent_job_id')=jobs.id)) LIMIT 1`, subject).Scan(&id)
	}
	return
}

type organizeAdmission struct {
	kind, subject, key string
	payload            any
	id, activeKey      string
}

const activeOrganizeFilesSQL = `SELECT m.source,j.id,COALESCE(CASE j.kind WHEN 'organize_file' THEN json_extract(j.payload,'$.id') ELSE json_extract(j.payload,'$.interactive_key') END,'')
 FROM json_each(?) wanted CROSS JOIN organize_queue_member m INDEXED BY organize_queue_source ON m.source=wanted.value
 CROSS JOIN jobs j ON j.id=m.job_id WHERE j.state IN ` + activeOrganizeStates

type organizeOwner struct{ id, key string }

func readOrganizeOwners(rows *sql.Rows) (map[string]organizeOwner, error) {
	defer rows.Close()
	owners := make(map[string]organizeOwner)
	for rows.Next() {
		var source string
		var owner organizeOwner
		if err := rows.Scan(&source, &owner.id, &owner.key); err != nil {
			return nil, err
		}
		if _, ok := owners[source]; !ok {
			owners[source] = owner
		}
	}
	return owners, rows.Err()
}

type checkedOrganizeOwner struct {
	id, key string
	version uint64
}

// The active subject index avoids a main-database read for new sources.
// Recheck all inbox claims together after any authoritative lookup, so a
// concurrent batch cannot acquire part of the selection during admission.
func (a *App) organizeAdmissionsTx(ctx context.Context, requests []organizeAdmission, phase func(string)) (*sql.Tx, error) {
	inbox := a.organizeInbox
	checked := make(map[string]checkedOrganizeOwner)
	if len(requests) == 0 {
		return inbox.db.BeginTx(ctx, nil)
	}
	// Each admission batch has one job kind.
	sources := make([]string, len(requests))
	for i, r := range requests {
		sources[i] = r.subject
	}
	selectedJSON, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}

	for {
		phase("inbox_wait")
		tx, err := inbox.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		var lookups []int
		versions := make(map[int]uint64)
		phase("inbox_lookup")
		rows, err := tx.QueryContext(ctx, `SELECT subject,id,request_key FROM request WHERE kind=? AND state='queued' AND subject IN (SELECT value FROM json_each(?))`, requests[0].kind, string(selectedJSON))
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		owners, err := readOrganizeOwners(rows)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		for i := range requests {
			r := &requests[i]
			owner := owners[r.subject]
			r.id, r.activeKey = owner.id, owner.key
			if r.id != "" {
				continue
			}
			maybe, version := inbox.subjects.lookup(r.subject)
			if !maybe {
				continue
			}
			if c, ok := checked[r.subject]; ok && c.version == version {
				r.id, r.activeKey = c.id, c.key
				continue
			}
			lookups = append(lookups, i)
			versions[i] = version
		}
		if len(lookups) == 0 {
			return tx, nil
		}
		tx.Rollback()
		phase("existing_reader_wait")
		reader, err := a.Store.reader().Conn(ctx)
		if err != nil {
			return nil, err
		}
		phase("existing_lookup")
		if len(lookups) == 1 {
			i := lookups[0]
			r := requests[i]
			id, key, e := activeInteractiveJob(ctx, reader, r.kind, r.subject)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				reader.Close()
				return nil, e
			}
			checked[r.subject] = checkedOrganizeOwner{id, key, versions[i]}
		} else {
			sources := make([]string, len(lookups))
			for j, i := range lookups {
				sources[j] = requests[i].subject
			}
			raw, e := json.Marshal(sources)
			if e != nil {
				reader.Close()
				return nil, e
			}
			rows, e := reader.QueryContext(ctx, activeOrganizeFilesSQL, string(raw))
			if e != nil {
				reader.Close()
				return nil, e
			}
			owners, e := readOrganizeOwners(rows)
			if e != nil {
				reader.Close()
				return nil, e
			}
			for _, i := range lookups {
				source := requests[i].subject
				owner := owners[source]
				checked[source] = checkedOrganizeOwner{owner.id, owner.key, versions[i]}
			}
		}
		reader.Close()
	}
}

func (a *App) acceptOrganize(ctx context.Context, kind, subject, key string, payload any, audit *organizeRequestAudit) (string, error) {
	requests := []organizeAdmission{{kind: kind, subject: subject, key: key, payload: payload}}
	if err := a.acceptOrganizeBatch(ctx, requests, "", nil, audit); err != nil {
		return "", err
	}
	return requests[0].id, nil
}

func (a *App) acceptOrganizeBatch(ctx context.Context, requests []organizeAdmission, batch string, listings map[string][]map[string]any, audit *organizeRequestAudit) error {
	phase := a.performanceStages(ctx, "organize_accept", "inbox_wait")
	defer phase("")
	tx, err := a.organizeAdmissionsTx(ctx, requests, phase)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	phase("request_insert")
	trace, _ := ctx.Value(jobTraceKey{}).(jobTrace)
	wf, stamp := id("wf_"), now()
	created := false
	// Keep the whole selection atomic while bounding both the bind count and
	// the number of serialized file plans retained per INSERT.
	const chunkSize = 64
	for chunk := range slices.Chunk(requests, chunkSize) {
		var requestArgs, memberArgs []any
		for i := range chunk {
			r := &chunk[i]
			if r.id != "" {
				if batch == "" && r.activeKey != r.key {
					return bad("JOB_EXISTS", "该源文件已有其他整理任务正在等待或执行")
				}
				continue
			}
			r.id, r.activeKey = id("job_"), r.key
			job := struct {
				Job
				Payload any `json:"payload"`
			}{Job: Job{ID: r.id, Kind: r.kind, RequestID: trace.Request, WorkflowID: wf, State: "queued", CreatedAt: stamp, UpdatedAt: stamp, RunAt: time.Now().Unix()}, Payload: r.payload}
			raw, err := json.Marshal(job)
			if err != nil {
				return err
			}
			requestArgs = append(requestArgs, r.id, r.kind, r.subject, r.key, raw)
			if batch != "" {
				memberArgs = append(memberArgs, r.id, batch)
			}
		}
		if len(requestArgs) == 0 {
			continue
		}
		values := strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,'queued'),", len(requestArgs)/5), ",")
		if _, err := tx.ExecContext(ctx, "INSERT INTO request(id,kind,subject,request_key,job,state) VALUES "+values, requestArgs...); err != nil {
			return err
		}
		if len(memberArgs) > 0 {
			values := strings.TrimSuffix(strings.Repeat("(?,?),", len(memberArgs)/2), ",")
			if _, err := tx.ExecContext(ctx, "INSERT INTO batch_member(job_id,batch_id) VALUES "+values, memberArgs...); err != nil {
				return err
			}
		}
		created = true
	}
	if batch != "" && created {
		raw, err := json.Marshal(listings)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO file_batch(id,listings) VALUES(?,?)", batch, raw); err != nil {
			return err
		}
	}
	if audit != nil {
		if _, err = tx.ExecContext(ctx, "INSERT INTO audit(id,request_id) VALUES(?,?)", audit.id, audit.request); err != nil {
			return err
		}
	}
	phase("durable_commit")
	if err = tx.Commit(); err != nil {
		return err
	}
	if audit != nil {
		audit.committed = true
	}
	a.organizeInbox.signal()
	return nil
}

type inboxJobScanner interface{ Scan(...any) error }

func scanInboxJob(row inboxJobScanner) (Job, error) {
	var raw []byte
	var state string
	var job Job
	if err := row.Scan(&raw, &state); err != nil {
		return job, err
	}
	if err := json.Unmarshal(raw, &job); err != nil {
		return job, err
	}
	job.State = state
	return job, nil
}

func (a *App) pendingOrganizeJob(ctx context.Context, id string) (Job, error) {
	return scanInboxJob(a.organizeInbox.db.QueryRowContext(ctx, "SELECT job,state FROM request WHERE id=?", id))
}
func (a *App) pendingOrganizeJobs(ctx context.Context) ([]Job, error) {
	rows, err := a.organizeInbox.db.QueryContext(ctx, "SELECT job,state FROM request WHERE state='queued' ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanInboxJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (inbox *organizeInbox) handoffSlot(id string) int {
	var slot byte
	if len(id) > 0 {
		slot = id[len(id)-1]
	}
	return int(slot) % len(inbox.handoffs)
}
func (a *App) cancelPendingOrganizeJob(ctx context.Context, id string) (bool, error) {
	return a.changePendingOrganizeState(ctx, id, "queued", "cancelled")
}

func (a *App) retryPendingOrganizeJob(ctx context.Context, id string) (bool, error) {
	return a.changePendingOrganizeState(ctx, id, "cancelled", "queued")
}

func (a *App) changePendingOrganizeState(ctx context.Context, id, from, to string) (bool, error) {
	inbox := a.organizeInbox
	lock := &inbox.handoffs[inbox.handoffSlot(id)]
	lock.Lock()
	defer lock.Unlock()
	var kind, subject string
	err := inbox.db.QueryRowContext(ctx, "SELECT kind,subject FROM request WHERE id=? AND state=?", id, from).Scan(&kind, &subject)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A crash can leave the inbox receipt after committing the authoritative
	// main row. Cancellation and retry must target that row once it exists.
	var delivered bool
	if err = a.Store.reader().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE id=?)", id).Scan(&delivered); err != nil || delivered {
		return false, err
	}
	tx, err := inbox.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if to == "queued" {
		var competing bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM request WHERE kind=? AND subject=? AND state='queued')", kind, subject).Scan(&competing); err != nil {
			return false, err
		}
		if competing {
			return false, bad("JOB_EXISTS", "已有该对象的任务正在等待或执行")
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request SET state=? WHERE id=?", to, id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	inbox.signal()
	return true, nil
}

func (a *App) drainOrganizeInbox(ctx context.Context) {
	inbox := a.organizeInbox
	for ctx.Err() == nil {
		more, err := a.deliverOrganizeInbox(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log("WARN", "organize", "inbox_delivery_delayed", "整理请求已保存，稍后重试投递："+a.clean(err.Error()), "", "")
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if !more {
			select {
			case <-ctx.Done():
				return
			case <-inbox.wake:
			}
		}
	}
}

func (a *App) deliverOrganizeInbox(ctx context.Context) (bool, error) {
	inbox := a.organizeInbox
	var jobID string
	err := inbox.db.QueryRowContext(ctx, "SELECT id FROM request ORDER BY rowid LIMIT 1").Scan(&jobID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Each retry has its own request audit, even when it reused an older ID.
	type receipt struct {
		seq         int64
		id, request string
	}
	rows, err := inbox.db.QueryContext(ctx, "SELECT rowid,id,request_id FROM audit ORDER BY rowid LIMIT 64")
	if err != nil {
		return false, err
	}
	var receipts []receipt
	for rows.Next() {
		var audit receipt
		if err = rows.Scan(&audit.seq, &audit.id, &audit.request); err != nil {
			rows.Close()
			return false, err
		}
		receipts = append(receipts, audit)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if jobID == "" && len(receipts) == 0 {
		return false, nil
	}
	phase := a.performanceStages(ctx, "organize_delivery", "main_writer")
	defer phase("")
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if inbox.subjects.needsRefresh() {
		phase("refresh_subjects")
		if err = inbox.subjects.restore(ctx, tx); err != nil {
			return false, err
		}
	}
	jobIDs := []string{}
	var batch string
	if jobID != "" {
		err = inbox.db.QueryRowContext(ctx, "SELECT batch_id FROM batch_member WHERE job_id=?", jobID).Scan(&batch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if batch == "" {
			jobIDs = append(jobIDs, jobID)
		} else {
			members, err := inbox.db.QueryContext(ctx, "SELECT job_id FROM batch_member WHERE batch_id=? ORDER BY rowid", batch)
			if err != nil {
				return false, err
			}
			for members.Next() {
				var member string
				if err = members.Scan(&member); err != nil {
					members.Close()
					return false, err
				}
				jobIDs = append(jobIDs, member)
			}
			err = members.Err()
			members.Close()
			if err != nil {
				return false, err
			}
		}
	}
	// A batch becomes visible to the main worker in a single commit. Lock each
	// handoff stripe once in stable order; cancellation uses the same stripes.
	var slots []int
	for _, member := range jobIDs {
		slots = append(slots, inbox.handoffSlot(member))
	}
	slices.Sort(slots)
	for _, slot := range slices.Compact(slots) {
		inbox.handoffs[slot].Lock()
		defer inbox.handoffs[slot].Unlock()
	}
	if batch != "" {
		var raw []byte
		if err = inbox.db.QueryRowContext(ctx, "SELECT listings FROM file_batch WHERE id=?", batch).Scan(&raw); err != nil {
			return false, err
		}
		var listings map[string]json.RawMessage
		if err = json.Unmarshal(raw, &listings); err != nil {
			return false, err
		}
		for path, entries := range listings {
			if err = txPut(ctx, tx, "organize_file_listing", organizeListingKey(batch, path), entries); err != nil {
				return false, err
			}
		}
	}
	var kind string
	for _, member := range jobIDs {
		job, err := a.pendingOrganizeJob(ctx, member)
		if err != nil {
			return false, err
		}
		phase("materialize")
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE id=?)", job.ID).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			if err = a.materializeOrganize(ctx, tx, job); err != nil {
				return false, err
			}
		}
		kind = job.Kind
	}
	phase("audit")
	for _, audit := range receipts {
		if err = txPut(ctx, tx, "audit", audit.id, requestAuditRecord("POST.files", audit.request, 200)); err != nil {
			return false, err
		}
	}
	phase("main_commit")
	if err = tx.Commit(); err != nil {
		return false, err
	}
	if kind != "" {
		a.wakeQueuedJob(kind)
	}
	phase("retire_receipt")
	done, err := inbox.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer done.Rollback()
	if batch != "" {
		if _, err = done.ExecContext(ctx, "DELETE FROM request WHERE id IN (SELECT job_id FROM batch_member WHERE batch_id=?)", batch); err != nil {
			return false, err
		}
		if _, err = done.ExecContext(ctx, "DELETE FROM batch_member WHERE batch_id=?", batch); err != nil {
			return false, err
		}
		if _, err = done.ExecContext(ctx, "DELETE FROM file_batch WHERE id=?", batch); err != nil {
			return false, err
		}
	} else if jobID != "" {
		if _, err = done.ExecContext(ctx, "DELETE FROM request WHERE id=?", jobID); err != nil {
			return false, err
		}
	}
	if len(receipts) > 0 {
		if _, err = done.ExecContext(ctx, "DELETE FROM audit WHERE rowid<=?", receipts[len(receipts)-1].seq); err != nil {
			return false, err
		}
	}
	return true, done.Commit()
}

func (a *App) materializeOrganize(ctx context.Context, tx *sql.Tx, job Job) error {
	trace := context.WithValue(ctx, jobTraceKey{}, jobTrace{Request: job.RequestID})
	var payload any
	var subject string
	var file organizeFilePayload
	switch job.Kind {
	case "organize_file":
		if err := json.Unmarshal(job.Payload, &file); err != nil {
			return err
		}
		payload, subject = file, file.File.Path
	case "organize_prepare":
		var prepare organizePreparationPayload
		if err := json.Unmarshal(job.Payload, &prepare); err != nil {
			return err
		}
		payload, subject = prepare, prepare.ID
	default:
		return errors.New("整理受理日志含未知任务")
	}
	if job.State == "queued" {
		// A download or monitor may have acquired this path since admission.
		existing, _, err := activeInteractiveJob(ctx, tx, job.Kind, subject)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing != "" {
			job.State, job.ErrorCode, job.ErrorMessage = "failed", "JOB_EXISTS", "该文件已有整理任务正在执行"
		}
	}
	if job.State != "queued" {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(job.Payload, &envelope); err != nil {
			return err
		}
		envelope["_request_id"], _ = json.Marshal(job.RequestID)
		job.Payload, _ = json.Marshal(envelope)
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at,error_code,error_message)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.WorkflowID, job.Kind, job.State, []byte(job.Payload), job.RunAt, job.CreatedAt, now(), job.ErrorCode, job.ErrorMessage)
		return err
	}
	if job.Kind == "organize_file" {
		if err := txPut(ctx, tx, "original_scrape_batch", file.BatchID, originalScrapeBatch{ID: file.BatchID, Closed: true}); err != nil {
			return err
		}
	}
	created, err := a.enqueueAcceptedTx(trace, tx, job.Kind, payload, job.WorkflowID, &job)
	if err == nil && created.ID != job.ID {
		return errors.New("整理任务受理编号不一致")
	}
	return err
}
