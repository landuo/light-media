package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"light-media/internal/releasemeta"
)

// This authority belongs to a real running subscription check. It is unrelated
// to plugin descriptors or Lark inbox capabilities. All download children are
// private until their same-transaction daemon receipt is accepted.
type subscriptionAcquisition struct {
	Completed                           bool `json:"completed,omitempty"`
	Source                              string
	Subscribe                           json.RawMessage
	Version                             int
	ID, JobSHA, JobWorkflowID, RulesSHA string
	Generation                          int
	Subscription                        Subscription
	Request                             SearchRequest
	Options                             releasemeta.Options
	Candidates                          []Candidate
	Metas                               []json.RawMessage
	Steps                               []subscriptionAcquisitionStep
}
type subscriptionAcquisitionInput struct {
	AllowWholeExtra    bool `json:"allow_whole_extra,omitempty"`
	Candidate          Candidate
	Episodes, Selected []int
	Meta               json.RawMessage
	Material           string
}
type subscriptionAcquisitionStep struct {
	Input                                                subscriptionAcquisitionInput
	InputSHA, Phase, IntentID, IntentSHA, ChildID, Error string
}
type subscriptionAcquisitionKey struct{}
type subscriptionAcquisitionGrant struct {
	a       *App
	root    *subscriptionAcquisition
	ordinal int
}
type subscriptionAcquisitionNoEffect struct{ cause error }

func (e *subscriptionAcquisitionNoEffect) Error() string { return e.cause.Error() }
func (e *subscriptionAcquisitionNoEffect) Unwrap() error { return e.cause }
func acquisitionEvidence() error {
	return bad("REVIEW_SUBSCRIPTION_ACQUISITION", "订阅批次的原始授权、下载任务或接收事实无法核对")
}
func acquisitionIntentSHA(in Intent) string {
	in.State, in.Hash, in.UpdatedAt, in.Error, in.ImportPlanID = "", "", "", "", ""
	in.Progress = 0
	return tokenHash(string(mustCleanSeedJSON(in)))
}
func (a *App) saveAcquisitionTx(ctx context.Context, tx *sql.Tx, r subscriptionAcquisition) error {
	raw := mustCleanSeedJSON(r)
	if len(raw) > pluginDataValueLimit-(8<<10) {
		return bad("SUBSCRIPTION_ACQUISITION_TOO_LARGE", "订阅批次快照超过存储范围，尚未确认后续下载")
	}
	sealed, e := a.pluginStore.seal("subscription_acquisition", "system", r.ID, raw)
	if e != nil {
		return e
	}
	return txPutRaw(ctx, tx, "subscription_acquisition", r.ID, sealed)
}
func txPutRaw(ctx context.Context, tx *sql.Tx, kind, key string, b []byte) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, b, now())
	return e
}
func (a *App) readAcquisition(ctx context.Context, q qualityReader, key string) (subscriptionAcquisition, error) {
	var r subscriptionAcquisition
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription_acquisition' AND id=?`, key).Scan(&sealed)
	if e != nil {
		return r, e
	}
	raw, e := a.pluginStore.unseal("subscription_acquisition", "system", key, sealed)
	if e != nil {
		return r, e
	}
	if cleanupJournalStrict(raw, &r) != nil || r.Version != 1 || r.ID != key || r.Subscription.ID == "" || len(r.Candidates) != len(r.Metas) {
		return r, acquisitionEvidence()
	}
	source, e := subscriptionAcquisitionSource(r.Subscribe)
	if e != nil || source != r.Source {
		return r, acquisitionEvidence()
	}
	for _, s := range r.Steps {
		if tokenHash(string(mustCleanSeedJSON(s.Input))) != s.InputSHA {
			return r, acquisitionEvidence()
		}
	}
	return r, nil
}
func (a *App) acquisitionScopeTx(ctx context.Context, q qualityReader, want *subscriptionAcquisition) (subscriptionAcquisition, error) {
	r, e := a.readAcquisition(ctx, q, want.ID)
	if e != nil {
		return r, e
	}
	if r.JobSHA != want.JobSHA || r.Generation != want.Generation || r.RulesSHA != want.RulesSHA || r.Subscription.ID != want.Subscription.ID || r.Subscription.Revision != want.Subscription.Revision || r.Subscription.SourceContextSHA != want.Subscription.SourceContextSHA {
		return r, acquisitionEvidence()
	}
	j, e := scanJob(q.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, r.ID))
	if e != nil {
		return r, e
	}
	var p struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	if json.Unmarshal(j.Payload, &p) != nil || j.Kind != "subscription" || j.State != "running" || j.Generation != r.Generation || tokenHash(string(j.Payload)) != r.JobSHA || j.WorkflowID != r.JobWorkflowID || p.ID != r.Subscription.ID || p.Revision != 0 && p.Revision != r.Subscription.Revision {
		return r, acquisitionEvidence()
	}
	var current Subscription
	if e = qualityRecord(ctx, q, "subscription", r.Subscription.ID, &current); e != nil {
		return r, e
	}
	if current.State != "active" || current.Revision != r.Subscription.Revision || current.SourceContextSHA != r.Subscription.SourceContextSHA || qualityRuleSHA(a.Settings()) != r.RulesSHA {
		return r, acquisitionEvidence()
	}
	return r, nil
}
func (a *App) acquisitionGrantTx(ctx context.Context, tx *sql.Tx) (*subscriptionAcquisitionGrant, subscriptionAcquisition, error) {
	g, ok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant)
	if !ok || g == nil || g.a != a || g.root == nil {
		return nil, subscriptionAcquisition{}, acquisitionEvidence()
	}
	r, e := a.acquisitionScopeTx(ctx, tx, g.root)
	if e != nil {
		return nil, r, e
	}
	if g.ordinal < 0 || g.ordinal >= len(r.Steps) || g.ordinal >= len(g.root.Steps) || r.Steps[g.ordinal].InputSHA != g.root.Steps[g.ordinal].InputSHA {
		return nil, r, acquisitionEvidence()
	}
	return g, r, nil
}
func (a *App) acquisitionConfirmTx(ctx context.Context, tx *sql.Tx, p DownloadPreview, in Intent, child Job) error {
	if in.SubscriptionAcquisition == "" {
		return nil
	}
	g, r, e := a.acquisitionGrantTx(ctx, tx)
	if e != nil {
		return e
	}
	step := &r.Steps[g.ordinal]
	input := step.Input
	if step.Phase != "intent" || input.Candidate.Resource.SiteID != in.SiteID || input.Candidate.Resource.ID != in.ResourceID || !reflect.DeepEqual(input.Episodes, in.Episodes) || !reflect.DeepEqual(input.Selected, in.SelectedEpisodes) || in.SubscriptionID != r.Subscription.ID || in.SubscriptionRevision != r.Subscription.Revision || child.Kind != "subscription_download" || child.ParentJobID != r.ID || child.WorkflowID != in.WorkflowID {
		return acquisitionEvidence()
	}
	var c downloadContextSnapshot
	if e = a.readQualitySealed(ctx, tx, "download_context", "intent:"+in.ID, &c); e != nil {
		return e
	}
	if c.DownloadKind != "intent" || c.DownloadID != in.ID || !sameHash(c.Hash, in.Hash) || !reflect.DeepEqual(mustCleanSeedJSON(c.SourceMeta), mustCleanSeedJSON(input.Meta)) {
		return acquisitionEvidence()
	}
	step.Phase, step.IntentID, step.IntentSHA, step.ChildID = "confirmed", in.ID, acquisitionIntentSHA(in), child.ID
	if e = a.saveAcquisitionTx(ctx, tx, r); e == nil {
		*g.root = r
	}
	return e
}
func (a *App) acquisitionChildTx(ctx context.Context, tx *sql.Tx, j Job, in Intent) (*subscriptionAcquisitionGrant, subscriptionAcquisition, error) {
	g, r, e := a.acquisitionGrantTx(ctx, tx)
	if e != nil {
		return nil, r, e
	}
	s := r.Steps[g.ordinal]
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		return nil, r, e
	}
	var p struct {
		ID string `json:"id"`
	}
	if s.Phase != "confirmed" || s.IntentID != in.ID || s.IntentSHA != acquisitionIntentSHA(in) || s.ChildID != j.ID || in.SubscriptionAcquisition != r.ID || actual.Kind != "subscription_download" || actual.ParentJobID != r.ID || actual.WorkflowID != in.WorkflowID || actual.State != "running" || actual.Generation != j.Generation || json.Unmarshal(actual.Payload, &p) != nil || p.ID != in.ID || !reflect.DeepEqual(actual.Payload, j.Payload) {
		return nil, r, acquisitionEvidence()
	}
	return g, r, nil
}
func (a *App) guardAcquisitionChild(ctx context.Context, j Job, in Intent) error {
	if in.SubscriptionAcquisition == "" && j.Kind != "subscription_download" {
		return nil
	}
	if in.State == "accepted" || in.State == "downloading" || in.State == "downloaded" || in.State == "imported" {
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		r, e := a.readAcquisition(ctx, tx, in.SubscriptionAcquisition)
		if e != nil {
			return e
		}
		actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
		if e != nil {
			return e
		}
		for _, step := range r.Steps {
			if step.Phase == "accepted" && step.IntentID == in.ID && step.IntentSHA == acquisitionIntentSHA(in) && step.ChildID == actual.ID && actual.Kind == "subscription_download" && actual.State == "succeeded" && actual.ParentJobID == r.ID && actual.WorkflowID == in.WorkflowID && slices.Equal(actual.Payload, j.Payload) {
				return a.guardAcceptedDownloadProgressTx(ctx, tx, in)
			}
		}
		return acquisitionEvidence()
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, _, e = a.acquisitionChildTx(ctx, tx, j, in)
	return e
}
func (a *App) acceptAcquisitionChildTx(ctx context.Context, tx *sql.Tx, j Job, in Intent) error {
	if in.SubscriptionAcquisition == "" && j.Kind != "subscription_download" {
		return nil
	}
	return a.acceptAcquisitionReceiptTx(ctx, tx, in, j)
}
func (a *App) submitAcquisitionChild(ctx context.Context, in Intent) (Intent, error) {
	var empty Intent
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	g, r, e := a.acquisitionGrantTx(ctx, tx)
	if e != nil {
		return empty, e
	}
	step := r.Steps[g.ordinal]
	if step.Phase != "confirmed" || step.IntentID != in.ID || step.IntentSHA != acquisitionIntentSHA(in) {
		return empty, acquisitionEvidence()
	}
	j, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, step.ChildID))
	if e != nil {
		return empty, e
	}
	if j.Kind != "subscription_download" || j.ParentJobID != r.ID {
		return empty, acquisitionEvidence()
	}
	if in.State == "post_add" {
		// A sealed, normally returned post-Add receipt authorizes SQL acceptance
		// recovery only. This branch never repeats Add, selection or Start.
		post, e := a.sourcePostAddReceipt(ctx, tx, in)
		if e != nil || !post.SourceReturned || post.SourceError != "" {
			return empty, bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后原操作尚未确认，未重试外部动作")
		}
		if j.State != "running" && j.State != "queued" && j.State != "retry_wait" {
			return empty, acquisitionEvidence()
		}
		oldGeneration, oldState := j.Generation, j.State
		j.State = "running"
		j.Generation++
		j.Attempt++
		j.LeaseUntil = time.Now().Add(10 * time.Minute).Unix()
		res, e := tx.ExecContext(ctx, `UPDATE jobs SET state='running',generation=?,attempt=?,lease_until=?,updated_at=? WHERE id=? AND state=? AND generation=?`, j.Generation, j.Attempt, j.LeaseUntil, now(), j.ID, oldState, oldGeneration)
		if e != nil {
			return empty, e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return empty, acquisitionEvidence()
		}

	} else {
		if j.State != "queued" || j.Attempt != 0 || j.Generation != 0 {
			return empty, acquisitionEvidence()
		}
		j.State, j.Attempt, j.Generation = "running", 1, 1
		j.LeaseUntil = time.Now().Add(10 * time.Minute).Unix()
		res, e := tx.ExecContext(ctx, `UPDATE jobs SET state='running',attempt=1,generation=1,lease_until=?,updated_at=? WHERE id=? AND state='queued' AND attempt=0 AND generation=0`, j.LeaseUntil, now(), j.ID)
		if e != nil {
			return empty, e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return empty, acquisitionEvidence()
		}
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	e = a.runDownload(ctx, j, in.ID)
	if e != nil {
		return empty, a.rejectAcquisitionChild(ctx, j, in, e)
	}
	var accepted Intent
	if e = a.Store.Get(ctx, "intent", in.ID, &accepted); e != nil {
		return empty, e
	}
	if accepted.State != "accepted" {
		return empty, acquisitionEvidence()
	}
	return accepted, nil
}
func (a *App) rejectAcquisitionChild(ctx context.Context, j Job, in Intent, cause error) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var current Intent
	if e = qualityRecord(ctx, tx, "intent", in.ID, &current); e != nil {
		return e
	}
	// A known rejection or a pre-Add validation returns to planned. Unknown Add,
	// lost ACK, post-Add and cancellation never permit a fallback external write.
	if current.State != "planned" || acquisitionIntentSHA(current) != acquisitionIntentSHA(in) {
		return cause
	}
	g, r, e := a.acquisitionChildTx(ctx, tx, j, current)
	if e != nil {
		return e
	}
	current.State, current.Error, current.UpdatedAt = "failed", a.clean(cause.Error()), now()
	if e = txPut(ctx, tx, "intent", current.ID, current); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, in.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',lease_until=0,error_code=?,error_message=?,updated_at=? WHERE id=? AND state='running' AND generation=?`, pluginErrorCodeInternal(cause), current.Error, now(), j.ID, j.Generation); e != nil {
		return e
	}
	r.Steps[g.ordinal].Phase, r.Steps[g.ordinal].Error = "rejected", current.Error
	if e = a.saveAcquisitionTx(ctx, tx, r); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	*g.root = r
	return &subscriptionAcquisitionNoEffect{cause}
}
