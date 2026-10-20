package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	larkhost "light-media/internal/plugins/larkmessager/host"
)

// The application root is independent from the native HTTP journal. Genesis
// belongs to a real lifecycle transaction; missing rows never mint a new owner.
const pluginLarkOwnerSchema = `CREATE TABLE IF NOT EXISTS plugin_lark_owner_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_lark_owner_chain(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,ordinal INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,ordinal));
CREATE TABLE IF NOT EXISTS plugin_lark_admissions(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,run_id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,run_id));
CREATE TABLE IF NOT EXISTS plugin_lark_calls(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,call_id TEXT NOT NULL,source_id TEXT NOT NULL,run_id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,call_id),UNIQUE(instance_id,source_id));
CREATE TABLE IF NOT EXISTS plugin_lark_kv_values(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,item_key TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,item_key));
CREATE TABLE IF NOT EXISTS plugin_lark_inbox(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,inbox_id TEXT NOT NULL,source_id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,inbox_id),UNIQUE(instance_id,source_id));
CREATE TABLE IF NOT EXISTS plugin_lark_inbox_steps(inbox_id TEXT NOT NULL,ordinal INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(inbox_id,ordinal));`

type pluginLarkOwnerRoot struct {
	Version                                 int
	Instance, Event, PreviousSHA, ParentSHA string
	Generation, Sequence                    int64
	Native                                  larkhost.Anchor
	Values                                  map[string]string
}
type pluginLarkOwnerStep struct {
	Version              int
	Root                 pluginLarkOwnerRoot
	Kind, ID, ReceiptSHA string
}
type pluginLarkAdmission struct {
	Version                                                                        int
	Instance, Run, Action, Method, InputSHA, ConfigSHA, SourceID, Phase, ErrorCode string
	Generation, Revision                                                           int64
	Result                                                                         json.RawMessage
}
type pluginLarkRootView struct {
	Root       pluginLarkOwnerRoot
	Sealed     []byte
	Digest     string
	Admissions map[string]pluginLarkAdmission
	Inboxes    map[string]pluginLarkInbox
	Calls      map[string]pluginLarkSourceCall
}

func larkEvidenceError() error {
	return bad("LARK_EVIDENCE_MISSING", "Lark执行证据缺失或无法校验，未重新初始化或重发消息")
}
func larkUnknownError() error {
	return bad("LARK_UNCONFIRMED", "Lark消息结果尚未确认，已保留原始发送记录，未重复执行")
}
func larkRootKey(g int64) string                 { return "lark-owner/v1/" + strconv.FormatInt(g, 10) }
func larkStepKey(g, seq int64) string            { return larkRootKey(g) + "/" + strconv.FormatInt(seq, 10) }
func larkAdmissionKey(g int64, id string) string { return larkRootKey(g) + "/run/" + id }
func larkLifecycleEvent(event string) bool {
	return event == "install" || event == "reset" || event == "uninstall" || event == "bulk_uninstall"
}
func larkRootSHA(root pluginLarkOwnerRoot) string   { return tokenHash(string(mustCleanSeedJSON(root))) }
func larkAdmissionSHA(r pluginLarkAdmission) string { return tokenHash(string(mustCleanSeedJSON(r))) }

// larkRootRead authenticates the complete admitted run set, including removal
// or replacement of a previously accepted row. The root/step records are small;
// application input and network responses remain in their existing encrypted
// stores rather than being copied into the root.
func (p *PluginStore) larkRootRead(ctx context.Context, q pluginRemoveLinkJournalReader, instance string, g int64) (pluginLarkRootView, error) {
	var out pluginLarkRootView
	if !pluginStorageID.MatchString(instance) || g < 1 {
		return out, larkEvidenceError()
	}
	if db, ok := q.(*sql.DB); ok {
		tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return out, e
		}
		defer tx.Rollback()
		out, e = p.larkRootRead(ctx, tx, instance, g)
		if e != nil {
			return out, e
		}
		return out, tx.Commit()
	}
	if e := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_lark_owner_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&out.Sealed); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return out, larkEvidenceError()
		}
		return out, e
	}
	raw, e := p.unseal("lark_owner_root", instance, larkRootKey(g), out.Sealed)
	if e != nil || cleanupJournalStrict(raw, &out.Root) != nil {
		return out, larkEvidenceError()
	}
	root := out.Root
	if root.Version != 1 || root.Instance != instance || root.Generation != g || root.Sequence < 0 || !larkLifecycleEvent(root.Event) || !pluginStorageDigest.MatchString(root.Native.Namespace) || !pluginStorageDigest.MatchString(root.Native.Digest) {
		return out, larkEvidenceError()
	}
	if (g == 1 && (root.ParentSHA != "" || root.Event != "install")) || (g > 1 && !pluginStorageDigest.MatchString(root.ParentSHA)) {
		return out, larkEvidenceError()
	}
	rows, e := q.QueryContext(ctx, `SELECT ordinal,sealed FROM plugin_lark_owner_chain WHERE instance_id=? AND generation=? ORDER BY ordinal`, instance, g)
	if e != nil {
		return out, e
	}
	expected := int64(0)
	previous := ""
	admissions := map[string]string{}
	calls := map[string]string{}
	inboxes := map[string]string{}
	for rows.Next() {
		var n int64
		var sealed []byte
		if e = rows.Scan(&n, &sealed); e != nil {
			break
		}
		var step pluginLarkOwnerStep
		value, err := p.unseal("lark_owner_step", instance, larkStepKey(g, n), sealed)
		if err != nil || cleanupJournalStrict(value, &step) != nil || step.Version != 1 || n != expected || step.Root.Version != 1 || step.Root.Instance != instance || step.Root.Generation != g || step.Root.Sequence != n || step.Root.Event != root.Event || step.Root.ParentSHA != root.ParentSHA || step.Root.PreviousSHA != previous {
			e = larkEvidenceError()
			break
		}
		if n == 0 {
			if step.Kind != "genesis" || step.ID != "" || step.ReceiptSHA != "" {
				e = larkEvidenceError()
				break
			}
		} else {
			switch step.Kind {
			case "kv":
				if (step.ID != "last_test_result" && step.ID != "last_chats") || !pluginStorageDigest.MatchString(step.ReceiptSHA) {
					e = larkEvidenceError()
				}
			case "native":
				if step.ID != "" || step.ReceiptSHA != "" {
					e = larkEvidenceError()
				}
			case "inbox":
				if !pluginStorageID.MatchString(step.ID) || !pluginStorageDigest.MatchString(step.ReceiptSHA) {
					e = larkEvidenceError()
				} else {
					inboxes[step.ID] = step.ReceiptSHA
				}
			case "call":
				if !pluginStorageID.MatchString(step.ID) || !pluginStorageDigest.MatchString(step.ReceiptSHA) {
					e = larkEvidenceError()
				} else {
					calls[step.ID] = step.ReceiptSHA
				}
			case "admission":
				if !pluginStorageID.MatchString(step.ID) || !pluginStorageDigest.MatchString(step.ReceiptSHA) {
					e = larkEvidenceError()
				} else {
					admissions[step.ID] = step.ReceiptSHA
				}
			default:
				e = larkEvidenceError()
			}
		}
		if e != nil {
			break
		}
		previous = larkRootSHA(step.Root)
		expected++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if expected != root.Sequence+1 || previous != larkRootSHA(root) {
		return out, larkEvidenceError()
	}
	out.Digest = previous
	out.Admissions = map[string]pluginLarkAdmission{}
	rows, e = q.QueryContext(ctx, `SELECT run_id,sealed FROM plugin_lark_admissions WHERE instance_id=? AND generation=? ORDER BY run_id`, instance, g)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id string
		var sealed []byte
		if e = rows.Scan(&id, &sealed); e != nil {
			break
		}
		value, err := p.unseal("run_result", instance, larkAdmissionKey(g, id), sealed)
		var receipt pluginLarkAdmission
		if err != nil || cleanupJournalStrict(value, &receipt) != nil || receipt.Version != 1 || receipt.Instance != instance || receipt.Generation != g || receipt.Run != id || receipt.Revision < 1 || !pluginStorageDigest.MatchString(receipt.InputSHA) || !pluginStorageDigest.MatchString(receipt.ConfigSHA) || receipt.SourceID == "" || admissions[id] != larkAdmissionSHA(receipt) {
			e = larkEvidenceError()
			break
		}
		if receipt.Phase != "accepted" && receipt.Phase != "confirmed" && receipt.Phase != "failed" {
			e = larkEvidenceError()
			break
		}
		out.Admissions[id] = receipt
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Admissions) != len(admissions) {
		return out, larkEvidenceError()
	}
	out.Calls = map[string]pluginLarkSourceCall{}
	rows, e = q.QueryContext(ctx, `SELECT call_id,source_id,run_id,sealed FROM plugin_lark_calls WHERE instance_id=? AND generation=? ORDER BY call_id`, instance, g)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id, source, run string
		var sealed []byte
		if e = rows.Scan(&id, &source, &run, &sealed); e != nil {
			break
		}
		var call pluginLarkSourceCall
		value, err := p.unseal("run_result", instance, larkCallKey(g, id), sealed)
		if err != nil || cleanupJournalStrict(value, &call) != nil || call.Version != 1 || call.Instance != instance || call.Generation != g || call.ID != id || call.SourceID != source || call.Run != run || calls[id] != larkSourceCallSHA(call) || !pluginStorageDigest.MatchString(call.InputSHA) || !pluginStorageDigest.MatchString(call.ConfigSHA) || !pluginStorageID.MatchString(run) {
			e = larkEvidenceError()
			break
		}
		out.Calls[id] = call
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Calls) != len(calls) {
		return out, larkEvidenceError()
	}
	out.Inboxes = map[string]pluginLarkInbox{}
	rows, e = q.QueryContext(ctx, `SELECT inbox_id,source_id,sealed FROM plugin_lark_inbox WHERE instance_id=? AND generation=?`, instance, g)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id, source string
		var sealed []byte
		if e = rows.Scan(&id, &source, &sealed); e != nil {
			break
		}
		var fact pluginLarkInbox
		raw, err := p.unseal("lark_inbox", instance, larkRootKey(g)+"/inbox/"+id, sealed)
		if err != nil || cleanupJournalStrict(raw, &fact) != nil || fact.Version != 1 || fact.ID != id || fact.Instance != instance || fact.Generation != g || fact.SourceID != source || inboxes[id] != larkInboxSHA(fact) {
			e = larkEvidenceError()
			break
		}
		out.Inboxes[id] = fact
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(inboxes) != len(out.Inboxes) {
		return out, larkEvidenceError()
	}
	rows, e = q.QueryContext(ctx, `SELECT item_key,sealed FROM plugin_lark_kv_values WHERE instance_id=? AND generation=?`, instance, g)
	if e != nil {
		return out, e
	}
	values := 0
	for rows.Next() {
		var key string
		var sealed []byte
		if e = rows.Scan(&key, &sealed); e != nil {
			break
		}
		if (key != "last_test_result" && key != "last_chats") || root.Values[key] != tokenHash(string(sealed)) {
			e = larkEvidenceError()
			break
		}
		var value map[string]any
		raw, err := p.unseal("lark_kv", instance, larkRootKey(g)+"/kv/"+key, sealed)
		if err != nil || cleanupJournalStrict(raw, &value) != nil || value == nil {
			e = larkEvidenceError()
			break
		}
		values++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if values != len(root.Values) {
		return out, larkEvidenceError()
	}
	if g > 1 {
		parent, e := p.larkRootRead(ctx, q, instance, g-1)
		if e != nil {
			return out, e
		}
		if !hmac.Equal([]byte(parent.Digest), []byte(root.ParentSHA)) {
			return out, larkEvidenceError()
		}
	}
	return out, nil
}
func (p *PluginStore) larkAppendRootTx(ctx context.Context, tx *sql.Tx, before pluginLarkRootView, next pluginLarkOwnerRoot, kind, id, receiptSHA string) error {
	if tx == nil || next.Instance != before.Root.Instance || next.Generation != before.Root.Generation || next.Sequence != before.Root.Sequence+1 || next.Event != before.Root.Event || next.ParentSHA != before.Root.ParentSHA {
		return larkEvidenceError()
	}
	next.PreviousSHA = before.Digest
	raw := mustCleanSeedJSON(next)
	sealed, e := p.seal("lark_owner_root", next.Instance, larkRootKey(next.Generation), raw)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE plugin_lark_owner_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?`, sealed, next.Instance, next.Generation, before.Sealed)
	if e != nil {
		return e
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return larkEvidenceError()
	}
	step := pluginLarkOwnerStep{Version: 1, Root: next, Kind: kind, ID: id, ReceiptSHA: receiptSHA}
	value, e := p.seal("lark_owner_step", next.Instance, larkStepKey(next.Generation, next.Sequence), mustCleanSeedJSON(step))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_owner_chain VALUES(?,?,?,?)`, next.Instance, next.Generation, next.Sequence, value)
	return e
}

// This is the independent Authority consumed by the real SQLite native
// journal. It binds the actual installed generation, not a fixture counter.
type pluginLarkAuthority struct {
	p          *PluginStore
	instance   string
	generation int64
}

func (a pluginLarkAuthority) Validate(ctx context.Context, tx *sql.Tx, anchor larkhost.Anchor) error {
	root, e := a.p.larkRootRead(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if root.Root.Native != anchor {
		return larkEvidenceError()
	}
	return nil
}
func (a pluginLarkAuthority) Advance(ctx context.Context, tx *sql.Tx, before, after larkhost.Anchor) error {
	root, e := a.p.larkRootRead(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if root.Root.Native != before || after.Namespace != before.Namespace || !pluginStorageDigest.MatchString(after.Digest) {
		return larkEvidenceError()
	}
	next := root.Root
	next.Native = after
	next.Sequence++
	return a.p.larkAppendRootTx(ctx, tx, root, next, "native", "", "")
}

func (p *PluginStore) larkGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string) error {
	if !larkLifecycleEvent(event) {
		return larkEvidenceError()
	}
	if _, e := tx.ExecContext(ctx, pluginLarkOwnerSchema); e != nil {
		return e
	}
	var plugin, version string
	var g int64
	if e := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &g); e != nil {
		return e
	}
	if plugin != "LarkMessager" || version != "0.9.1" || g < 1 {
		return larkEvidenceError()
	}
	parentSHA := ""
	if g > 1 {
		parent, e := p.larkRootRead(ctx, tx, instance, g-1)
		if e != nil {
			return e
		}
		for _, admission := range parent.Admissions {
			if admission.Phase == "accepted" {
				return larkUnknownError()
			}
		}
		parentSHA = parent.Digest
	}
	anchor, e := larkhost.GenesisTx(ctx, p.store.DB, tx, p.store.key, larkhost.Group{Owner: instance, Generation: g})
	if e != nil {
		return e
	}
	root := pluginLarkOwnerRoot{Version: 1, Instance: instance, Event: event, Generation: g, ParentSHA: parentSHA, Native: anchor}
	sealed, e := p.seal("lark_owner_root", instance, larkRootKey(g), mustCleanSeedJSON(root))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_owner_roots VALUES(?,?,?)`, instance, g, sealed); e != nil {
		return e
	}
	step := pluginLarkOwnerStep{Version: 1, Root: root, Kind: "genesis"}
	sealed, e = p.seal("lark_owner_step", instance, larkStepKey(g, 0), mustCleanSeedJSON(step))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_owner_chain VALUES(?,?,?,?)`, instance, g, 0, sealed)
	return e
}
func (p *PluginStore) larkSaveAdmissionTx(ctx context.Context, tx *sql.Tx, before pluginLarkRootView, receipt pluginLarkAdmission) error {
	if receipt.Instance != before.Root.Instance || receipt.Generation != before.Root.Generation {
		return larkEvidenceError()
	}
	raw := mustCleanSeedJSON(receipt)
	sealed, e := p.seal("run_result", receipt.Instance, larkAdmissionKey(receipt.Generation, receipt.Run), raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_lark_admissions VALUES(?,?,?,?) ON CONFLICT(instance_id,generation,run_id) DO UPDATE SET sealed=excluded.sealed`, receipt.Instance, receipt.Generation, receipt.Run, sealed); e != nil {
		return e
	}
	next := before.Root
	next.Sequence++
	return p.larkAppendRootTx(ctx, tx, before, next, "admission", receipt.Run, larkAdmissionSHA(receipt))
}
func larkAdmissionSame(a pluginLarkAdmission, run PluginRun, method string) bool {
	var invocation struct {
		Action string
		Config json.RawMessage
		Input  json.RawMessage
	}
	return cleanupJournalStrict(run.Input, &invocation) == nil && a.Run == run.ID && a.Instance == run.InstanceID && a.Generation == run.Generation && a.Revision == run.ConfigRevision && a.Action == run.Action && a.Method == method && a.InputSHA == deferredInputSHA(run.Input) && a.ConfigSHA == tokenHash(string(invocation.Config))
}
func larkSameRaw(a, b []byte) bool { return bytes.Equal(a, b) }

// Transitions authenticate both journals and the immutable main-queue/deferred
// closure before classifying unknown work. No old source fact is discarded.
func (p *PluginStore) larkTransitionTx(ctx context.Context, tx *sql.Tx, instance string) (bool, error) {
	var plugin, version string
	var g int64
	e := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &g)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	var retained int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_lark_owner_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_lark_owner_chain WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_lark_admissions WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_lark_calls WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_lark_kv_values WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_lark_inbox WHERE instance_id=?)`, instance, instance, instance, instance, instance, instance).Scan(&retained); err != nil {
		return false, err
	}
	if errors.Is(e, sql.ErrNoRows) || plugin != "LarkMessager" || version != "0.9.1" {
		if retained != 0 {
			return false, larkEvidenceError()
		}
		return false, nil
	}
	unknown := false
	for generation := g; generation > 0; generation-- {
		view, e := p.larkRootRead(ctx, tx, instance, generation)
		if e != nil {
			return true, e
		}
		proofs, e := larkhost.AuditTx(ctx, p.store.DB, tx, p.store.key, larkhost.Group{Owner: instance, Generation: generation}, pluginLarkAuthority{p, instance, generation})
		if e != nil {
			return true, e
		}
		for _, inbox := range view.Inboxes {
			if e := p.larkInboxBackingTx(ctx, tx, inbox); e != nil {
				return true, e
			}
			steps, e := p.larkInboxStepsTx(ctx, tx, inbox)
			if e != nil {
				return true, e
			}
			for _, step := range steps {
				if step.Kind == "write" && (step.Phase == "unknown" || step.Phase == "intent" && !p.larkCoreIsLive(inbox)) {
					unknown = true
				}
			}
			if inbox.State == "started" || inbox.State == "unknown" {
				if inbox.State == "unknown" || ctx.Value(pluginLarkQueueKey{}) != true || !p.larkCoreIsLive(inbox) {
					unknown = true
				}
			}
		}
		seen := map[string]bool{}
		for _, proof := range proofs {
			a, ok := view.Admissions[proof.Scope.InvocationID]
			if !ok || proof.Scope.Revision != a.Revision || proof.Scope.SourceID != a.SourceID || proof.Scope.InputSHA != a.InputSHA || proof.Scope.ConfigSHA != a.ConfigSHA || proof.Scope.Action != a.Method {
				return true, larkEvidenceError()
			}
			seen[a.Run] = true
			if proof.Uncertain && (ctx.Value(pluginLarkQueueKey{}) != true || !p.larkAdmissionIsLive(a)) {
				unknown = true
			}
		}
		for id, a := range view.Admissions {
			run, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
			if e != nil {
				return true, larkEvidenceError()
			}
			d, _, e := p.readDeferred(ctx, tx, run)
			if e != nil || !larkAdmissionSame(a, run, a.Method) || d.Plugin != "LarkMessager" || d.Instance != instance || d.Generation != generation || d.Run != id || d.Method != a.Method || d.Revision != a.Revision || d.InputSHA != a.InputSHA {
				return true, larkEvidenceError()
			}
			switch a.Phase {
			case "accepted":
				if d.Phase != "started" && d.Phase != "uncertain" {
					return true, larkEvidenceError()
				}
				lease, ok := ctx.Value(pluginLarkLeaseKey{}).(*pluginLarkLease)
				if (!ok || lease == nil || lease.call.run.ID != id || lease.owner.instance != instance || lease.owner.generation != generation) && (ctx.Value(pluginLarkQueueKey{}) != true || !p.larkAdmissionIsLive(a)) {
					unknown = true
				}
			case "confirmed":
				if !seen[id] || (d.Phase != "confirmed" && d.Phase != "started" && d.Phase != "uncertain") || (d.Phase == "confirmed" && !bytes.Equal(a.Result, d.Result)) || a.ErrorCode != "" {
					return true, larkEvidenceError()
				}
			case "failed":
				if !seen[id] || (d.Phase != "terminal_failed" && d.Phase != "started" && d.Phase != "uncertain") || (d.Phase == "terminal_failed" && (!bytes.Equal(a.Result, d.Result) || a.ErrorCode != d.ErrorCode)) {
					return true, larkEvidenceError()
				}
			default:
				return true, larkEvidenceError()
			}
			if a.Phase != "accepted" && (d.Phase == "started" || d.Phase == "uncertain") && (ctx.Value(pluginLarkQueueKey{}) != true || !p.larkAdmissionIsLive(a)) {
				unknown = true
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT r.id FROM plugin_runs r JOIN plugin_deferred_operations d ON d.run_id=r.id WHERE r.instance_id=? AND r.generation=?`, instance, generation)
		if e != nil {
			return true, e
		}
		var runs []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			runs = append(runs, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return true, e
		}
		for _, id := range runs {
			run, e := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
			if e != nil {
				return true, e
			}
			d, _, e := p.readDeferred(ctx, tx, run)
			if e != nil {
				return true, e
			}
			_, admitted := view.Admissions[id]
			if d.Plugin != "LarkMessager" || !deferredSameRun(d, PluginInstallation{PluginID: plugin, InstanceID: instance, Version: version}, run) || ((d.Phase == "prepared") == admitted) {
				return true, larkEvidenceError()
			}
		}
	}
	if unknown {
		return true, larkUnknownError()
	}
	return true, nil
}
