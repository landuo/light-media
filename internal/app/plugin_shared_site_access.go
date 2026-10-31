package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"light-media/internal/plugins"
	"light-media/internal/plugins/siteaccess"
)

// This new producer owns its own table. It neither invents old site history nor
// imposes a unique-domain constraint absent from the original source model.
const sourceSiteAccessSchema = `
CREATE TABLE IF NOT EXISTS source_site_access_statistic(
 id INTEGER PRIMARY KEY, domain TEXT, success INTEGER, fail INTEGER,
 seconds INTEGER, lst_state INTEGER, lst_mod_date TEXT, note TEXT);
CREATE INDEX IF NOT EXISTS source_site_access_statistic_domain ON source_site_access_statistic(domain);
`

func originalSharedSiteAccessCapability(method string) bool {
	return method == plugins.SiteAccessSuccessCapability || method == plugins.SiteAccessFailCapability
}

func originalSiteAccessInput(method string, raw json.RawMessage) (string, *int64, error) {
	invalid := func() (string, *int64, error) {
		return "", nil, bad("INVALID_PLUGIN_VALUE", "站点访问统计参数无效")
	}
	if !originalSharedSiteAccessCapability(method) || len(raw) == 0 || len(raw) > 128<<10 || !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return invalid()
	}
	// The common decoder rejects unknown/trailing values. Token scanning also
	// rejects duplicate names rather than silently replacing the original input.
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok {
			return invalid()
		}
		if _, exists := fields[key]; exists {
			return invalid()
		}
		if key != "domain" && (key != "seconds" || method != plugins.SiteAccessSuccessCapability) {
			return invalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return invalid()
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return invalid()
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalid()
	}
	value, present := fields["domain"]
	var domain *string
	if !present || json.Unmarshal(value, &domain) != nil || domain == nil || !utf8.ValidString(*domain) {
		return invalid()
	}
	var seconds *int64
	if value, present = fields["seconds"]; present && json.Unmarshal(value, &seconds) != nil {
		return invalid()
	}
	return *domain, seconds, nil
}

func (a *App) originalSharedSiteAccessResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if ctx == nil || scope.validate == nil || !originalSharedSiteAccessCapability(method) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点访问统计操作未获授权")
	}
	domain, seconds, err := originalSiteAccessInput(method, raw)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	// Source captures one local wall-clock string before its first DAO query.
	stamp := time.Now().Format("2006-01-02 15:04:05")
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// This existing guard uses the held transaction, not a second connection to
	// the DB-backed validator. pluginMu keeps installation mutation serialized.
	if err = a.sharedSystemConfigScopeTx(ctx, tx, scope); err != nil {
		return nil, err
	}
	if err = writeOriginalSiteAccess(ctx, tx, method, domain, seconds, stamp); err != nil {
		return nil, err
	}
	if err = a.sharedSystemConfigScopeTx(ctx, tx, scope); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// A cancellation/revocation after commit is an error, not a promise that the
	// already committed write was undone. There is no automatic retry.
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return json.RawMessage(`null`), nil
}

// writeOriginalSiteAccess is the purpose-specific SQL mutation inside its
// caller's transaction. timestamp is ordinary producer input, not a test clock.
// The caller commits only after its own authority and cancellation checks.
func writeOriginalSiteAccess(ctx context.Context, tx *sql.Tx, method, domain string, seconds *int64, timestamp string) error {
	if !originalSharedSiteAccessCapability(method) {
		return bad("PLUGIN_CAPABILITY_DENIED", "站点访问统计操作未获授权")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var rowID int64
	if method == plugins.SiteAccessFailCapability {
		var count int64
		var storedNote sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id,fail,note FROM source_site_access_statistic WHERE domain=? LIMIT 1`, domain).Scan(&rowID, &count, &storedNote)
		if errors.Is(err, sql.ErrNoRows) {
			next, e := siteaccess.Fail(nil, domain, timestamp)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES(?,?,?,?,?,?,?)`, domain, next.Success, next.Fail, nil, next.LastState, next.LastModified, nil)
			return e
		}
		if err != nil {
			return err
		} // In particular, NULL/noninteger fail is not zero.
		// The original ORM hydrates its JSON column before entering fail's
		// body. Invalid JSON must not bypass that prerequisite just because fail
		// does not otherwise use note. Any finite JSON shape is preserved raw;
		// no timing-map coercion is imposed on this operation.
		if storedNote.Valid && (!utf8.ValidString(storedNote.String) || !json.Valid([]byte(storedNote.String))) {
			return bad("SITE_ACCESS_JSON_INVALID", "既有访问统计JSON无效或不属于当前有限JSON范围")
		}
		// Only fail is an algorithm input here. Other fields are not rewritten;
		// nullable values and valid non-timing JSON keep their exact bytes.
		next, err := siteaccess.Fail(&siteaccess.Record{ID: &rowID, Domain: domain, Fail: count}, domain, timestamp)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE source_site_access_statistic SET fail=?,lst_state=?,lst_mod_date=? WHERE id=?`, next.Fail, next.LastState, next.LastModified, rowID)
		return err
	}
	var count int64
	var elapsed sql.NullInt64
	var note sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,success,seconds,note FROM source_site_access_statistic WHERE domain=? LIMIT 1`, domain).Scan(&rowID, &count, &elapsed, &note)
	var prior *siteaccess.Record
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		prior = &siteaccess.Record{ID: &rowID, Domain: domain, Success: count}
		if elapsed.Valid {
			v := elapsed.Int64
			prior.Seconds = &v
		}
		if note.Valid {
			var values map[string]*int64
			if !utf8.ValidString(note.String) || !sharedHistoryScalarEscapes([]byte(note.String)) || json.Unmarshal([]byte(note.String), &values) != nil {
				return bad("SITE_ACCESS_VALUE_UNSUPPORTED", "既有访问统计不属于当前整数记录范围")
			}
			if values != nil {
				prior.Note = make(map[string]int64, len(values))
				for key, value := range values {
					if value == nil { // Do not turn an unknown/noninteger JSON null into zero.
						return bad("SITE_ACCESS_VALUE_UNSUPPORTED", "既有访问统计不属于当前整数记录范围")
					}
					prior.Note[key] = *value
				}
			}
		}
	}
	next, err := siteaccess.Success(prior, domain, seconds, timestamp)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(next.Note)
	if err != nil {
		return err
	}
	if prior == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO source_site_access_statistic(domain,success,fail,seconds,lst_state,lst_mod_date,note) VALUES(?,?,?,?,?,?,?)`, domain, next.Success, next.Fail, next.Seconds, next.LastState, next.LastModified, string(encoded))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE source_site_access_statistic SET success=?,seconds=?,lst_state=?,lst_mod_date=?,note=? WHERE id=?`, next.Success, next.Seconds, next.LastState, next.LastModified, string(encoded), rowID)
	}
	return err
}
