package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

type pluginNoticeV2Binding struct {
	SiteMessage                         *siteMessageIdentity            `json:",omitempty"`
	Added                               *subscriptionAddIdentity        `json:",omitempty"`
	Completion                          *subscriptionCompletionIdentity `json:",omitempty"`
	Kind, Instance, Inbox, CoreInputSHA string
	Generation                          int64
	Ordinal                             int
}
type pluginNoticeV2Business struct {
	Version                  int
	Projection               string
	Binding                  pluginNoticeV2Binding
	Input                    json.RawMessage
	Context                  noticeV2Value
	Kwargs                   notice.Object
	EnvelopeSHA, BusinessSHA string
}
type pluginNoticeV2Cache struct {
	Key     string
	Context noticeV2Value
}
type pluginNoticeV2Render struct {
	Templates         noticeV2Value
	Snapshot          notice.EnvelopeSnapshot
	RenderSourceError string
	SourceCache       *pluginNoticeV2Cache
}
type pluginNoticeV2Result struct {
	Version                           int
	RouteID, EnvelopeSHA, BusinessSHA string
	Count                             int
	LastSHA                           string
	SourceReturned                    bool
	SourceError                       string
}
type pluginNoticeV2History struct {
	History Notification
	Digest  string
}

func noticeV2BusinessSHA(input noticeV2Value, kwargs notice.Object) string {
	return tokenHash(string(mustCleanSeedJSON(struct {
		Context noticeV2Value
		Kwargs  notice.Object
	}{input, kwargs})))
}

func (p *PluginStore) validateNoticeBusinessV2(ctx context.Context, q pluginRemoveLinkJournalReader, r pluginNoticeRoute) error {
	b := r.BusinessV2
	if b == nil || b.Version != 1 || (b.Projection != "tmdb-source-attributes/v1" && b.Projection != "absent-source-context/v1") || b.Binding.Instance != r.Instance || b.Binding.Generation != r.Generation || b.EnvelopeSHA != tokenHash(string(r.Envelope)) || b.BusinessSHA != noticeV2BusinessSHA(b.Context, b.Kwargs) || r.AcceptedSHA != tokenHash(string(mustCleanSeedJSON(b))) {
		return seedhost.ErrUnsafe
	}
	switch b.Binding.Kind {
	case "lark_core":
		if b.Binding.SiteMessage != nil || b.Binding.Added != nil || b.Binding.Completion != nil || b.Binding.Ordinal < 0 || r.Token != p.noticeID("lark-business", b.Binding.Inbox, b.Binding.Ordinal) || r.EffectID != b.Binding.Inbox+"/"+fmt.Sprint(b.Binding.Ordinal) || b.Binding.CoreInputSHA != tokenHash(string(b.Input)) {
			return seedhost.ErrUnsafe
		}
		root, e := p.larkRootRead(ctx, q, r.Instance, r.Generation)
		if e != nil {
			return e
		}
		f, ok := root.Inboxes[b.Binding.Inbox]
		if !ok {
			return seedhost.ErrUnsafe
		}
		steps, e := p.larkInboxStepsTx(ctx, q, f)
		if e != nil || b.Binding.Ordinal >= len(steps) {
			return seedhost.ErrUnsafe
		}
		s := steps[b.Binding.Ordinal]
		if s.Kind != "write" || s.Operation != "notice.post" || s.InputSHA != b.Binding.CoreInputSHA {
			return seedhost.ErrUnsafe
		}
	case "site_message":
		if e := p.validateSiteMessageNoticeBinding(ctx, q, r); e != nil {
			return e
		}
	case "subscription_add":
		if e := p.validateSubscriptionAddNoticeBinding(ctx, q, r); e != nil {
			return e
		}
	case "subscription_completion":
		if e := p.validateSubscriptionCompletionNoticeBinding(ctx, q, r); e != nil {
			return e
		}
	default:
		return seedhost.ErrUnsafe
	}
	var payload pluginLarkBusinessNotice
	if cleanupJournalStrict(b.Input, &payload) != nil {
		return seedhost.ErrUnsafe
	}
	if b.Projection != sourceBusinessProjection(payload) || payload.AbsentContext && b.Binding.Kind != "lark_core" && b.Binding.Kind != "site_message" {
		return seedhost.ErrUnsafe
	}
	env, e := notice.NewEnvelope(payload.Envelope)
	if e != nil || tokenHash(string(mustCleanSeedJSON(env.Dump()))) != b.EnvelopeSHA {
		return seedhost.ErrUnsafe
	}
	input, e := larkBusinessNoticeContext(payload)
	if e != nil {
		return e
	}
	encoded, e := noticeV2PackContext(input)
	if e != nil || !larkSameRaw(mustCleanSeedJSON(encoded), mustCleanSeedJSON(b.Context)) || !larkSameRaw(mustCleanSeedJSON(payload.Kwargs), mustCleanSeedJSON(b.Kwargs)) {
		return seedhost.ErrUnsafe
	}
	return nil
}

func (a *App) initLarkBusinessNotice(ctx context.Context, core *pluginLarkCoreExecution, payload pluginLarkBusinessNotice) (string, error) {
	input, e := larkBusinessNoticeContext(payload)
	if e != nil {
		return "", e
	}
	encoded, e := noticeV2PackContext(input)
	if e != nil {
		return "", e
	}
	env, e := notice.NewEnvelope(payload.Envelope)
	if e != nil {
		return "", e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	_, f, e := a.larkCoreScopeTx(ctx, tx, core)
	if e != nil {
		return "", e
	}
	handle, ok := ctx.Value(pluginLarkP0CommitKey{}).(*pluginLarkP0Commit)
	rawInput := mustCleanSeedJSON(payload)
	if !ok || handle == nil || handle.a != a || handle.core != core || handle.operation != "notice.post" || handle.ordinal != core.ordinal || handle.inputSHA != tokenHash(string(rawInput)) {
		return "", larkEvidenceError()
	}
	steps, e := a.pluginStore.larkInboxStepsTx(ctx, tx, f)
	if e != nil || core.ordinal != len(steps)-1 || steps[core.ordinal].Phase != "intent" || steps[core.ordinal].Operation != "notice.post" || steps[core.ordinal].InputSHA != handle.inputSHA {
		return "", larkEvidenceError()
	}
	b := &pluginNoticeV2Business{Version: 1, Projection: sourceBusinessProjection(payload), Binding: pluginNoticeV2Binding{Kind: "lark_core", Instance: f.Instance, Generation: f.Generation, Inbox: f.ID, Ordinal: core.ordinal, CoreInputSHA: handle.inputSHA}, Input: rawInput, Context: encoded, Kwargs: payload.Kwargs, EnvelopeSHA: tokenHash(string(mustCleanSeedJSON(env.Dump()))), BusinessSHA: noticeV2BusinessSHA(encoded, payload.Kwargs)}
	p := a.pluginStore
	token := p.noticeID("lark-business", f.ID, core.ordinal)
	stamp := p.noticeNow()
	r := pluginNoticeRoute{Version: 2, ID: p.noticeID("route", f.Instance, f.Generation, token), Kind: "business", Instance: f.Instance, Generation: f.Generation, Token: token, EffectID: f.ID + "/" + fmt.Sprint(core.ordinal), AcceptedSHA: tokenHash(string(mustCleanSeedJSON(b))), Envelope: mustCleanSeedJSON(env.Dump()), CreatedAt: stamp.UTC().Format(time.RFC3339Nano), CurrentTime: stamp.Local().Format("2006-01-02 15:04:05"), BusinessV2: b}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_notice_routes WHERE id=?`, r.ID).Scan(&count); e != nil || count != 0 {
		return "", larkEvidenceError()
	}
	sealed, e := p.noticeSeal("route", r.ID, r)
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO plugin_notice_routes VALUES(?,?,?,?,?)`, r.ID, r.Instance, r.Generation, r.Token, sealed); e != nil {
		return "", e
	}
	if e = p.commitNoticeTx(tx, "business_accept"); e != nil {
		_ = tx.Rollback()
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		stored, _, readErr := p.readNoticeRoute(readCtx, a.Store.DB, r.ID)
		if readErr != nil || !larkSameRaw(mustCleanSeedJSON(stored), mustCleanSeedJSON(r)) {
			return "", seedhost.ErrUnknown
		}
	}
	return r.ID, nil
}

func (p *PluginStore) noticeTemplateHelper() *noticetemplate.Helper {
	p.notices.mu.Lock()
	defer p.notices.mu.Unlock()
	if p.notices.templateHelper == nil {
		p.notices.templateHelper = noticetemplate.NewHelper(nil)
	}
	return p.notices.templateHelper
}

func (a *App) runNoticeBusinessV2(ctx context.Context, initial pluginNoticeRoute) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	if e := a.requireNoticeV2Execution(ctx, initial); e != nil {
		return e
	}
	d := &pluginNoticeDriver{a: a, id: initial.ID}
	input, e := noticeV2UnpackContext(initial.BusinessV2.Context)
	if e != nil {
		return e
	}
	var fields notice.Object
	if cleanupJournalStrict(initial.Envelope, &fields) != nil {
		return seedhost.ErrUnsafe
	}
	env, e := notice.NewEnvelope(fields) // The validated original input only.
	if e != nil {
		return e
	}
	value, e := d.step(ctx, "superuser", nil, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
		name, e := a.pluginStore.noticePreference(ctx, tx, "superuser", "")
		if e != nil || name != nil {
			return name, e
		}
		var user string
		e = tx.QueryRowContext(ctx, `SELECT username FROM admin WHERE id=1`).Scan(&user)
		if errors.Is(e, sql.ErrNoRows) {
			return "admin", nil
		}
		return user, e
	})
	if e != nil {
		return e
	}
	superuser, ok := value.(string)
	if !ok {
		e = notice.SourceError{Class: "TypeError"}
		if finishErr := d.finish(ctx, e); finishErr != nil {
			return finishErr
		}
		return e
	}
	router := notice.Router{Dependencies: notice.Dependencies{
		Superuser: superuser,
		Render: func(ctx context.Context, message *notice.Envelope, _ notice.Business, kwargs notice.Object) (*notice.Envelope, error) {
			value, e := d.step(ctx, "render", struct {
				EnvelopeSHA, BusinessSHA string
				Kwargs                   notice.Object
			}{initial.BusinessV2.EnvelopeSHA, initial.BusinessV2.BusinessSHA, kwargs}, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
				templates, e := a.pluginStore.noticePreference(ctx, tx, "templates", "NotificationTemplates")
				if e != nil {
					return nil, e
				}
				if e = noticeV2RequireSourceObjectImplementation(message, templates); e != nil {
					return nil, e
				}
				encodedTemplates, e := noticeV2Encode(templates)
				if e != nil {
					return nil, e
				}
				business := input
				business.Extra = map[string]any{}
				for key, value := range input.Extra {
					business.Extra[key] = value
				}
				for key, value := range kwargs {
					business.Extra[key], e = noticeV2NormalizeNumberValues(value)
					if e != nil {
						return nil, e
					}
				}
				result, e := a.pluginStore.noticeTemplateHelper().RenderMessage(ctx, message, business, func(context.Context) (any, error) { return templates, nil })
				if e != nil {
					return nil, e
				}
				snapshot, e := result.Message.Snapshot()
				if e != nil {
					return nil, e
				}
				out := pluginNoticeV2Render{Templates: encodedTemplates, Snapshot: snapshot, RenderSourceError: result.SourceError}
				if result.SourceCache != nil {
					cached, e := noticeV2Encode(result.SourceCache.Context)
					if e != nil {
						return nil, e
					}
					out.SourceCache = &pluginNoticeV2Cache{Key: result.SourceCache.Key, Context: cached}
				}
				return out, nil
			})
			if e != nil {
				return nil, e
			}
			var out pluginNoticeV2Render
			if cleanupJournalStrict(mustCleanSeedJSON(value), &out) != nil {
				return nil, seedhost.ErrUnsafe
			}
			return notice.RestoreSnapshot(out.Snapshot) // Never revalidate assignments.
		},
		SaveHistory: func(_ context.Context, fields notice.Object) error {
			_, e := d.step(ctx, "history", fields, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				text := func(key string) (string, error) {
					if fields[key] == nil {
						return "", nil
					}
					v, ok := fields[key].(string)
					if !ok {
						return "", notice.ErrTemplateDependency
					}
					return v, nil
				}
				title, e := text("title")
				if e != nil {
					return nil, e
				}
				message, e := text("text")
				if e != nil {
					return nil, e
				}
				mtype, e := text("mtype")
				if e != nil {
					return nil, e
				}
				link, e := text("link")
				if e != nil {
					return nil, e
				}
				source, e := text("source")
				if e != nil {
					return nil, e
				}
				n := Notification{Source: source, ID: "notice_" + a.pluginStore.noticeID("business-history", r.ID, ordinal), Title: a.clean(title), Message: a.clean(message), MType: mtype, Link: link, CreatedAt: a.pluginStore.noticeNow().UTC().Format(time.RFC3339Nano)}
				if e = txPut(ctx, tx, "notification", n.ID, n); e != nil {
					return nil, e
				}
				saved := pluginNoticeV2History{History: n, Digest: a.pluginStore.noticeID("business-history-value", n)}
				if r.BusinessV2.Binding.Kind == "site_message" {
					if e = a.pluginStore.saveSiteMessageHistoryExecutionTx(ctx, tx, r, ordinal, fields, saved); e != nil {
						return nil, e
					}
				}
				return saved, nil
			})
			return e
		},
		NotificationSwitch: func(_ context.Context, key any) (any, error) {
			return d.step(ctx, "switch", key, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
				text, ok := key.(string)
				if !ok {
					return nil, notice.SourceError{Class: "AttributeError"}
				}
				return a.pluginStore.noticePreference(ctx, tx, "switch", text)
			})
		},
		UserSettings: func(_ context.Context, key any) (any, error) {
			return d.step(ctx, "user", key, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
				text, ok := key.(string)
				if !ok {
					return nil, notice.SourceError{Class: "TypeError"}
				}
				return a.pluginStore.noticePreference(ctx, tx, "user", text)
			})
		},
		NoticeMessage: func(_ context.Context, fields notice.Object) error {
			_, e := d.step(ctx, "publish", fields, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				return a.publishPluginNoticeTx(ctx, tx, r, ordinal, fields)
			})
			return e
		},
		QueueCall: func(_ context.Context, call notice.Call) error {
			value, e := d.step(ctx, "queue", call, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				return a.acceptPluginMessageTx(ctx, tx, r, ordinal, call)
			})
			if e != nil {
				return e
			}
			out, ok := value.(map[string]any)
			if !ok {
				return seedhost.ErrUnsafe
			}
			id, ok := out["id"].(string)
			if !ok {
				return seedhost.ErrUnsafe
			}
			if out["direct"] == true {
				return a.attemptPluginMessage(ctx, id)
			}
			return nil
		},
	}}
	e = router.Post(ctx, env, notice.Business{Meta: input.Meta, MediaInfo: input.Media}, initial.BusinessV2.KwargsWithTime(initial.CurrentTime))
	if errors.Is(e, seedhost.ErrUnknown) || errors.Is(e, seedhost.ErrUnsafe) || errors.Is(e, notice.ErrTemplateDependency) || errors.Is(e, notice.ErrDependency) || errors.Is(e, exec.ErrUnsupported) || ctx.Err() != nil {
		return e
	}
	if e != nil {
		var source notice.SourceError
		if !errors.As(e, &source) {
			// Native SQL/transport/protocol failures are not an original Python
			// exception. Preserve the incomplete route for exact local recovery.
			return e
		}
	}
	if finishErr := d.finish(ctx, e); finishErr != nil {
		return finishErr
	}
	return e
}

func (b *pluginNoticeV2Business) KwargsWithTime(stamp string) notice.Object {
	out := notice.Object{}
	for key, value := range b.Kwargs {
		out[key] = value
	}
	if _, exists := out["current_time"]; !exists {
		out["current_time"] = stamp
	}
	return out
}
