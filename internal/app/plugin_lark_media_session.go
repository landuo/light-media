package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"light-media/internal/catalog"
)

type pluginLarkMediaSession struct {
	Version                                      int
	Request, User, Action, Keyword, Title, Phase string
	Meta                                         json.RawMessage
	Medias                                       []catalog.Media
	Candidates                                   []Candidate
	CandidateMetas                               []json.RawMessage
	PendingNoExists                              *pluginLarkMediaTargets
	Targets                                      []SearchRequest
	SiteNames                                    map[string]string
	Selected                                     *catalog.Media
	Page, PendingPage                            int
	PendingMode                                  string
	PendingCandidate                             *Candidate
	Directories                                  []DirectoryRule
	PendingDirectoryFacts                        *larkDownloadDirectorySnapshot
	Created, Updated                             time.Time
	Sequence                                     int64
	Removed                                      bool
}

// Session snapshots are authenticated members of the source transcript. The
// latest sequence is selected from that complete set, rather than trusting an
// unsealed mutable cache or recreating missing interaction history.
func (p *PluginStore) larkMediaSessionTx(ctx context.Context, tx *sql.Tx, root pluginLarkRootView, user string) (*pluginLarkMediaSession, error) {
	var current *pluginLarkMediaSession
	for _, f := range root.Inboxes {
		if e := p.larkInboxBackingTx(ctx, tx, f); e != nil {
			return nil, e
		}
		steps, e := p.larkInboxStepsTx(ctx, tx, f)
		if e != nil {
			return nil, e
		}
		for _, s := range steps {
			if s.Kind != "transaction" || s.Operation != "media_session" {
				continue
			}
			var v pluginLarkMediaSession
			if cleanupJournalStrict(s.Result, &v) != nil || v.Version != 1 || v.User == "" || v.Request == "" || v.Sequence < 1 || v.Sequence > root.Root.Sequence || v.Created.IsZero() || v.Updated.Before(v.Created) {
				return nil, larkEvidenceError()
			}
			if v.User == user && (current == nil || v.Sequence > current.Sequence) {
				copy := v
				current = &copy
			}
		}
	}
	if current != nil && (current.Removed || time.Since(current.Created) > 24*time.Hour) {
		return nil, nil
	}
	return current, nil
}
func (a *App) larkCurrentMediaSession(ctx context.Context, core *pluginLarkCoreExecution, user string) (*pluginLarkMediaSession, error) {
	return larkCoreRead(ctx, a, core, "media_session_read", user, func(ctx context.Context) (*pluginLarkMediaSession, error) {
		tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		root, _, e := a.larkCoreScopeTx(ctx, tx, core)
		if e != nil {
			return nil, e
		}
		v, e := a.pluginStore.larkMediaSessionTx(ctx, tx, root, user)
		if e != nil {
			return nil, e
		}
		return v, tx.Commit()
	})
}
func (a *App) larkSaveMediaSession(ctx context.Context, core *pluginLarkCoreExecution, session pluginLarkMediaSession) (*pluginLarkMediaSession, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	root, f, e := a.larkCoreScopeTx(ctx, tx, core)
	if e != nil {
		return nil, e
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil {
		return nil, e
	}
	// A replay compares the original proposed state, before host-assigned time
	// and sequence, then returns the accepted snapshot without extending its TTL.
	input := tokenHash(string(mustCleanSeedJSON(session)))
	if core.ordinal < len(steps) {
		s := steps[core.ordinal]
		if s.Kind != "transaction" || s.Operation != "media_session" || s.InputSHA != input {
			return nil, larkEvidenceError()
		}
		var result pluginLarkMediaSession
		if cleanupJournalStrict(s.Result, &result) != nil {
			return nil, larkEvidenceError()
		}
		core.ordinal++
		return &result, tx.Commit()
	}
	if core.ordinal != len(steps) {
		return nil, larkEvidenceError()
	}
	now := time.Now().UTC()
	session.Version = 1
	session.Sequence = root.Root.Sequence + 1
	session.Updated = now
	if session.Created.IsZero() {
		session.Created = now
	}
	if session.User == "" || session.Request == "" {
		return nil, larkEvidenceError()
	}
	s := pluginLarkInboxStep{Version: 1, Inbox: f.ID, Ordinal: core.ordinal, Kind: "transaction", Operation: "media_session", InputSHA: input, PreviousSHA: f.LastSHA, Phase: "confirmed", Result: mustCleanSeedJSON(session)}
	if e = a.larkSaveCoreStepTx(ctx, tx, root, f, s, true); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	core.ordinal++
	return &session, nil
}
