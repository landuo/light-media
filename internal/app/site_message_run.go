package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

// The public run index is only a locator. It cannot grant recovery authority:
// the referenced sealed collection must identify this exact API invocation.
func (p *PluginStore) siteMessageCollectionForRun(ctx context.Context, q subscriptionCompletionReader, run PluginRun) (siteMessageCollection, bool, error) {
	var out siteMessageCollection
	var raw []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_message_collection_run' AND id=?`, run.ID).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	var index struct {
		Collection string `json:"collection"`
	}
	if cleanupJournalStrict(raw, &index) != nil || index.Collection == "" {
		return out, true, siteCollectionError()
	}
	out, e = p.readSiteMessageCollection(ctx, q, index.Collection)
	if e != nil {
		return out, true, e
	}
	origin := out.Origin
	if run.Action != "api" || origin.Kind != "api" || origin.ID != run.ID || origin.Instance != run.InstanceID || origin.Version != run.Version || origin.Generation != run.Generation || origin.Revision != run.ConfigRevision {
		return out, true, siteCollectionError()
	}
	var invocation plugins.Invocation
	var request plugins.HTTPRequest
	if json.Unmarshal(run.Input, &invocation) != nil || !pluginStrictJSON(invocation.Input, &request) || request.Key != "refresh_by_domain" || request.Method != "GET" || request.LastQuery("domain") != origin.Domain || !request.CredentialMatches["apikey"] {
		return out, true, siteCollectionError()
	}
	return out, true, nil
}
