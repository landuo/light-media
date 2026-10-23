package app

import (
	"context"
	"database/sql"
	"errors"

	"light-media/internal/notice"
	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

const removeLinkSourceResultSchema = `
CREATE TABLE IF NOT EXISTS plugin_cleanup_source_results(route_id TEXT PRIMARY KEY, sealed BLOB NOT NULL);`

// A source result is an INSERT-only terminal fact, independent of the mutable
// routing checkpoint. It authenticates source return, not external delivery.
type removeLinkSourceResult struct {
	Version                                         int
	RouteID, Instance, Token, EffectID, Scope, Site string
	Generation                                      int64
	Ordinal                                         int
	AcceptanceSHA, RouteInitialSHA                  string
	Count                                           int
	LastSHA, Outcome, Class, EndType                string
	Failure                                         *removeLinkSourceFailure `json:",omitempty"`
}
type removeLinkSourceFailure struct {
	Kind     string
	Ordinal  int
	InputSHA string
}

func knownRemoveLinkSourceClass(err error) (string, bool) {
	if err == nil {
		return "", true
	}
	var n notice.SourceError
	class := ""
	if errors.As(err, &n) {
		class = n.Class
	} else {
		return "", false
	}
	switch class {
	case "TypeError", "AttributeError", "ValueError", "RuntimeError":
		return class, true
	}
	return "", false
}

// This lookup deliberately does not call readNoticeRoute: its callers already
// validate the source chain, and recursively reading the route would deadlock
// or recurse while using a single SQL connection.
func (p *PluginStore) removeLinkSourceBinding(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) (cleanuphost.Effect, bool, error) {
	var x cleanuphost.Effect
	if r.Kind != "remove_link" {
		return x, false, nil
	}
	row := pluginCleanupOutboxRow{Instance: r.Instance, Generation: r.Generation, Token: r.Token}
	if err := q.QueryRowContext(ctx, `SELECT sealed,created_at FROM plugin_cleanup_private_outbox WHERE instance_id=? AND generation=? AND token=?`, row.Instance, row.Generation, row.Token).Scan(&row.Sealed, &row.Created); err != nil {
		return x, false, err
	}
	_, x, box, err := p.committedCleanupOutbox(ctx, q, row)
	if err != nil {
		return x, false, err
	}
	if box.Version != 3 {
		return x, false, nil
	}
	if x.Kind != "notification" || x.ID != r.EffectID || x.SourceBarrierVersion != 1 || !validRemoveLinkNoticeSite(x.NoticeSite) || x.NoticeOrdinal < 1 {
		return x, false, cleanuphost.ErrUnsafe
	}
	return x, true, nil
}

func removeLinkSourceResultBase(r pluginNoticeRoute, x cleanuphost.Effect) removeLinkSourceResult {
	return removeLinkSourceResult{Version: 1, RouteID: r.ID, Instance: r.Instance, Generation: r.Generation, Token: r.Token, EffectID: x.ID, Scope: x.Scope, Site: x.NoticeSite, Ordinal: x.NoticeOrdinal, AcceptanceSHA: r.AcceptedSHA, RouteInitialSHA: noticeInitialSHA(r), Count: r.Count, LastSHA: r.LastSHA}
}

func (p *PluginStore) finishRemoveLinkSourceTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, cursor int, sourceErr error, failures ...*removeLinkSourceFailure) error {
	x, required, err := p.removeLinkSourceBinding(ctx, tx, r)
	if err != nil || !required {
		return err
	}
	class, known := knownRemoveLinkSourceClass(sourceErr)
	if !known || !r.Complete || r.Count != cursor || r.SourceError != class {
		return cleanuphost.ErrUncertain
	}
	result := removeLinkSourceResultBase(r, x)
	result.Outcome, result.EndType = "normal_return", "return"
	if sourceErr != nil {
		result.Outcome, result.Class, result.EndType = "source_exception", class, "router_exception"
		if len(failures) > 0 && failures[0] != nil {
			result.Failure = failures[0]
			result.EndType = "callback_exception"
			if result.Failure.Ordinal != cursor || !validRemoveLinkSourceFailure(*result.Failure) {
				return cleanuphost.ErrUnsafe
			}
		}
	}
	sealed, err := p.noticeSeal("remove_link_source_result", r.ID, result)
	if err != nil {
		return err
	}
	// An exact terminal retry may observe the existing fact, but never revise it.
	var existing []byte
	readErr := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_cleanup_source_results WHERE route_id=?`, r.ID).Scan(&existing)
	if readErr == nil {
		var prior removeLinkSourceResult
		if p.noticeUnseal("remove_link_source_result", r.ID, existing, &prior) != nil || string(mustCleanSeedJSON(prior)) != string(mustCleanSeedJSON(result)) {
			return cleanuphost.ErrUnsafe
		}
		return nil
	}
	if !errors.Is(readErr, sql.ErrNoRows) {
		return readErr
	}
	// No upsert: a terminal result can never be revised by a retry.
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_source_results(route_id,sealed) VALUES(?,?)`, r.ID, sealed)
	return err
}

func validRemoveLinkSourceFailure(f removeLinkSourceFailure) bool {
	switch f.Kind {
	case "superuser", "history", "switch", "user", "publish", "queue":
		return f.Ordinal >= 0 && pluginStorageDigest.MatchString(f.InputSHA)
	}
	return false
}

func (p *PluginStore) validateRemoveLinkSourceResult(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	x, required, err := p.removeLinkSourceBinding(ctx, q, r)
	if err != nil || !required {
		return err
	}
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_cleanup_source_results WHERE route_id=?`, r.ID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) && !r.Complete {
		return nil
	}
	if err != nil {
		return err
	}
	if !r.Complete {
		return cleanuphost.ErrUnsafe
	}
	var result removeLinkSourceResult
	if err = p.noticeUnseal("remove_link_source_result", r.ID, sealed, &result); err != nil {
		return err
	}
	base := removeLinkSourceResultBase(r, x)
	// Compare the immutable identity and the full successful callback prefix.
	actual := result
	actual.Outcome, actual.Class, actual.EndType, actual.Failure = "", "", "", nil
	if string(mustCleanSeedJSON(actual)) != string(mustCleanSeedJSON(base)) {
		return cleanuphost.ErrUnsafe
	}
	switch result.Outcome {
	case "normal_return":
		if result.Class != "" || r.SourceError != "" || result.EndType != "return" || result.Failure != nil {
			return cleanuphost.ErrUnsafe
		}
	case "source_exception":
		if _, ok := knownRemoveLinkSourceClass(notice.SourceError{Class: result.Class}); !ok || result.Class != r.SourceError {
			return cleanuphost.ErrUnsafe
		}
		if result.EndType == "callback_exception" {
			if result.Failure == nil || result.Failure.Ordinal != r.Count || !validRemoveLinkSourceFailure(*result.Failure) {
				return cleanuphost.ErrUnsafe
			}
		} else if result.EndType != "router_exception" || result.Failure != nil {
			return cleanuphost.ErrUnsafe
		}
	default:
		return cleanuphost.ErrUnsafe
	}
	return nil
}

func (e *pluginRemoveLinkEffects) readRemoveLinkSourceResult(ctx context.Context, x cleanuphost.Effect) error {
	if x.SourceBarrierVersion != 1 || x.Kind != "notification" {
		return cleanuphost.ErrUnsafe
	}
	tx, err := e.j.p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return cleanuphost.ErrUncertain
	}
	defer tx.Rollback()
	id := e.j.p.noticeID("route", e.j.instance, e.j.generation, e.token(x.ID))
	r, _, err := e.j.p.readNoticeRoute(ctx, tx, id)
	if err != nil || !r.Complete {
		return cleanuphost.ErrUncertain
	}
	accepted, required, err := e.j.p.removeLinkSourceBinding(ctx, tx, r)
	if err != nil || !required {
		return cleanuphost.ErrUncertain
	}
	identity, _, err := e.identity(x)
	original, _, otherErr := e.identity(accepted)
	if err != nil || otherErr != nil || identity != original {
		return cleanuphost.ErrUnsafe
	}
	if r.SourceError != "" {
		return &removelink.SourceNoticeError{Class: r.SourceError}
	}
	return nil
}
