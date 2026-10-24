package app

import (
	"context"
	"encoding/json"
	"regexp"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

var noticeV2CacheKey = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (p *PluginStore) validateNoticeBusinessV2Steps(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute, steps []pluginNoticeStep) error {
	var rendered *pluginNoticeV2Render
	history := 0
	for _, step := range steps {
		switch step.Kind {
		case "superuser":
			if step.Ordinal != 0 {
				return seedhost.ErrUnsafe
			}
		case "render":
			if rendered != nil || step.Ordinal != 1 {
				return seedhost.ErrUnsafe
			}
			var out pluginNoticeV2Render
			if cleanupJournalStrict(step.Output, &out) != nil {
				return seedhost.ErrUnsafe
			}
			if _, e := notice.RestoreSnapshot(out.Snapshot); e != nil {
				return seedhost.ErrUnsafe
			}
			if _, e := noticeV2Decode(out.Templates); e != nil {
				return seedhost.ErrUnsafe
			}
			if out.SourceCache != nil {
				if !noticeV2CacheKey.MatchString(out.SourceCache.Key) {
					return seedhost.ErrUnsafe
				}
				if _, e := noticeV2Decode(out.SourceCache.Context); e != nil {
					return seedhost.ErrUnsafe
				}
			}
			want := struct {
				EnvelopeSHA, BusinessSHA string
				Kwargs                   notice.Object
			}{r.BusinessV2.EnvelopeSHA, r.BusinessV2.BusinessSHA, r.BusinessV2.KwargsWithTime(r.CurrentTime)}
			if step.InputSHA != tokenHash(string(mustCleanSeedJSON(want))) {
				return seedhost.ErrUnsafe
			}
			rendered = &out
		case "history":
			if rendered == nil || history != 0 || step.InputSHA != tokenHash(string(mustCleanSeedJSON(rendered.Snapshot.Fields))) {
				return seedhost.ErrUnsafe
			}
			var saved pluginNoticeV2History
			if cleanupJournalStrict(step.Output, &saved) != nil || saved.History.ID != "notice_"+p.noticeID("business-history", r.ID, step.Ordinal) || saved.Digest != p.noticeID("business-history-value", saved.History) {
				return seedhost.ErrUnsafe
			}
			if r.BusinessV2.Binding.Kind == "site_message" {
				if e := p.validateSiteMessageHistoryExecution(ctx, q, r, step, saved); e != nil {
					return e
				}
				history++
				continue
			}
			var raw []byte
			if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, saved.History.ID).Scan(&raw); e != nil {
				return seedhost.ErrUnsafe
			}
			var current Notification
			if cleanupJournalStrict(raw, &current) != nil {
				return seedhost.ErrUnsafe
			}
			current.Read = false
			if saved.Digest != p.noticeID("business-history-value", current) {
				return seedhost.ErrUnsafe
			}
			history++
		case "switch", "user", "publish", "queue":
			if rendered == nil {
				return seedhost.ErrUnsafe
			}
		default:
			return seedhost.ErrUnsafe
		}
	}
	if r.Complete && r.SourceError == "" {
		if rendered == nil || noticeTruth(rendered.Snapshot.Fields["save_history"]) != (history == 1) {
			return seedhost.ErrUnsafe
		}
	}
	return nil
}

// Preserve full route evidence for diagnostics and native consumers. The public
// notification projection does not replace the source render/cache transcript.
func (p *PluginStore) noticeBusinessRender(ctx context.Context, id string) (pluginNoticeV2Render, error) {
	var out pluginNoticeV2Render
	r, steps, e := p.readNoticeRoute(ctx, p.store.DB, id)
	if e != nil || r.Version != 2 {
		return out, seedhost.ErrUnsafe
	}
	for _, step := range steps {
		if step.Kind == "render" {
			if json.Unmarshal(step.Output, &out) == nil {
				return out, nil
			}
		}
	}
	return out, seedhost.ErrUnsafe
}
