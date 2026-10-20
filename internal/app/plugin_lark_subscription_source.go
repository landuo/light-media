package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/subscriptiondefaults"
)

type pluginLarkCallerKey struct{}
type pluginLarkCaller struct{ Username json.RawMessage }

func sourceSubscriptionNoteClone(note []int) []int {
	if note == nil {
		return nil
	}
	return append([]int{}, note...)
}

// These source kwargs remain JSON values until SubscribeOper normalizes them.
// In particular, Python accepts 1 for search_imdbid and retains falsey values
// such as 0 for the two explicit best-version overrides.
func sourceSubscriptionTruth(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		n, ok := new(big.Rat).SetString(string(value))
		return !ok || n.Sign() != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	case []any:
		return len(value) != 0
	case []string:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	default:
		return true
	}
}

// Origin is a creation fact, not a replacement for the current subscription's
// effective rules or its N/R runtime. Keep original nullable fields and the
// twelve default values until those consumers have read their actual source.
type sourceSubscriptionOrigin struct {
	SourceAddID                    string          `json:",omitempty"`
	Registration                   json.RawMessage `json:",omitempty"`
	Version                        int
	Channel, Source, UserID, State string
	Username                       json.RawMessage
	Note                           []int
	Meta                           json.RawMessage
	Defaults                       subscriptiondefaults.Object
	MediaInfo                      json.RawMessage `json:",omitempty"`
	ProviderInfo                   json.RawMessage `json:",omitempty"`
}

func larkSourceSubscriptionInput(in SubscriptionRequest) any {
	return struct {
		Request SubscriptionRequest
		Source  *sourceSubscriptionOrigin
	}{in, in.source}
}

func (a *App) saveSourceSubscriptionOriginTx(ctx context.Context, tx *sql.Tx, sub Subscription, origin *sourceSubscriptionOrigin, inputSHA string) error {
	if origin == nil {
		return nil
	}
	if sub.SourceContextSHA != tokenHash(string(mustCleanSeedJSON(origin))) {
		return bad("LARK_CORE_SCOPE_DENIED", "订阅新增缺少原消息来源授权")
	}
	if e := a.guardSourceSubscriptionMutationTx(ctx, tx, origin, inputSHA); e != nil {
		return e
	}
	sealed, e := a.pluginStore.seal("source_subscription_origin", "system", sub.ID, mustCleanSeedJSON(origin))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('source_subscription_origin',?,?,?)`, sub.ID, sealed, now())
	return e
}

func (a *App) guardSourceSubscriptionMutationTx(ctx context.Context, tx *sql.Tx, origin *sourceSubscriptionOrigin, inputSHA string) error {
	if origin != nil && origin.SourceAddID != "" {
		return a.guardUISubscriptionAddMutationTx(ctx, tx, origin, inputSHA)
	}
	handle, ok := ctx.Value(pluginLarkP0CommitKey{}).(*pluginLarkP0Commit)
	if !ok || handle == nil || handle.a != a || handle.core == nil || handle.operation != "subscription.save" || handle.inputSHA != inputSHA || origin == nil || origin.Version != 1 || origin.Channel != "飞书" || origin.Source != "Lark" || (origin.State != "N" && origin.State != "R") {
		return bad("LARK_CORE_SCOPE_DENIED", "订阅新增缺少原消息来源授权")
	}
	_, _, e := a.larkCoreScopeTx(ctx, tx, handle.core)
	return e
}

func (a *App) sourceSubscriptionOrigin(ctx context.Context, sub Subscription) (*sourceSubscriptionOrigin, error) {
	if sub.SourceContextSHA == "" {
		return nil, nil
	}
	var sealed []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_subscription_origin' AND id=?`, sub.ID).Scan(&sealed); e != nil {
		return nil, e
	}
	raw, e := a.pluginStore.unseal("source_subscription_origin", "system", sub.ID, sealed)
	if e != nil {
		return nil, e
	}
	var origin sourceSubscriptionOrigin
	if cleanupJournalStrict(raw, &origin) != nil || origin.Version != 1 || tokenHash(string(mustCleanSeedJSON(origin))) != sub.SourceContextSHA {
		return nil, larkEvidenceError()
	}
	return &origin, nil
}

func decodeSourceJSON(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		return nil, e
	}
	return value, nil
}

func (a *App) larkSubscriptionDefaults(ctx context.Context, core *pluginLarkCoreExecution, media catalog.Media, best *bool) (subscriptiondefaults.Object, error) {
	return larkCoreRead(ctx, a, core, "subscription.defaults", struct {
		Media catalog.Media
		Best  *bool
	}{media, best}, func(ctx context.Context) (subscriptiondefaults.Object, error) {
		environment := subscriptiondefaults.Object{}
		var sealed []byte
		e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_subscription_environment' AND id='main'`).Scan(&sealed)
		if e == nil {
			raw, e := a.pluginStore.unseal("source_subscription_environment", "system", "main", sealed)
			if e != nil {
				return nil, e
			}
			if cleanupJournalStrict(raw, &environment) != nil {
				return nil, larkEvidenceError()
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		config := a.Settings()
		provider := subscriptiondefaults.Provider{Environment: environment, ReadDatabase: func(key string) (any, error) {
			var raw json.RawMessage
			switch key {
			case "DefaultMovieSubscribeConfig":
				raw = config.DefaultMovieSubscribeConfig
			case "DefaultTvSubscribeConfig":
				raw = config.DefaultTVSubscribeConfig
			default:
				return nil, nil
			}
			return decodeSourceJSON(raw)
		}}
		kwargs := subscriptiondefaults.Object{}
		if best != nil {
			kwargs["best_version"] = *best
		}
		kind := "电影"
		if media.MediaType == "tv" {
			kind = "电视剧"
		}
		return provider.Apply(kind, kwargs)
	})
}

// The original UserOper.list order is independent of hashed preference keys.
// An existing user set without its imported order is an explicit dependency;
// never choose an arbitrary hash/insertion order or assume the administrator.
func (p *PluginStore) sourceUserOrder(ctx context.Context, q pluginRemoveLinkJournalReader) ([]string, error) {
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_user_order' AND id='main'`).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		var count int
		if e = q.QueryRowContext(ctx, `SELECT count(*) FROM plugin_notice_preferences WHERE kind='user'`).Scan(&count); e != nil {
			return nil, e
		}
		if count != 0 {
			return nil, bad("SOURCE_USER_ORDER_MISSING", "原用户绑定顺序尚未导入，无法选择订阅用户名")
		}
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	raw, e := p.unseal("source_user_order", "system", "main", sealed)
	if e != nil {
		return nil, e
	}
	var names []string
	if cleanupJournalStrict(raw, &names) != nil {
		return nil, larkEvidenceError()
	}
	return names, nil
}

func (p *PluginStore) saveSourceUserOrderTx(ctx context.Context, tx *sql.Tx, names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			return larkEvidenceError()
		}
		seen[name] = true
		var exists int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_notice_preferences WHERE kind='user' AND token=?`, p.noticeID("preference", "user", name)).Scan(&exists); e != nil || exists != 1 {
			return larkEvidenceError()
		}
		if _, e := p.noticePreference(ctx, tx, "user", name); e != nil {
			return e
		}
	}
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_notice_preferences WHERE kind='user'`).Scan(&count); e != nil || count != len(names) {
		return larkEvidenceError()
	}
	sealed, e := p.seal("source_user_order", "system", "main", mustCleanSeedJSON(names))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('source_user_order','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now())
	return e
}

func (a *App) larkSubscriptionUsername(ctx context.Context, core *pluginLarkCoreExecution, user string) (json.RawMessage, error) {
	caller, _ := ctx.Value(pluginLarkCallerKey{}).(pluginLarkCaller)
	if len(caller.Username) == 0 {
		caller.Username = json.RawMessage("null")
	}
	return larkCoreRead(ctx, a, core, "subscription.username", struct {
		User   string
		Caller json.RawMessage
	}{user, caller.Username}, func(ctx context.Context) (json.RawMessage, error) {
		tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		names, e := a.pluginStore.sourceUserOrder(ctx, tx)
		if e != nil {
			return nil, e
		}
		for _, name := range names {
			settings, e := a.pluginStore.noticePreference(ctx, tx, "user", name)
			if e != nil {
				return nil, e
			}
			if settings == nil {
				continue
			}
			mapping, ok := settings.(map[string]any)
			if !ok {
				return nil, bad("SOURCE_USER_SETTINGS_INVALID", "原用户绑定设置无法读取")
			}
			if binding, ok := mapping["feishu_userid"].(string); ok && binding == user {
				return json.Marshal(name)
			}
		}
		return caller.Username, nil
	})
}

func larkSourceMediaImage(media catalog.Media, domain string) string {
	path := media.BackdropPath
	if path == "" {
		path = media.PosterPath
	}
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "/") {
		if domain == "" {
			domain = "image.tmdb.org"
		}
		path = "https://" + domain + "/t/p/original/" + strings.TrimPrefix(path, "/")
	}
	return strings.ReplaceAll(path, "original", "w500")
}

func sourceSubscriptionLink(domain, kind string) any {
	if domain == "" {
		return nil
	}
	if !strings.HasSuffix(domain, "/") {
		domain += "/"
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "http://" + domain
	}
	base, e := url.Parse(domain)
	if e != nil {
		return nil
	}
	path := "movie"
	if kind == "tv" {
		path = "tv"
	}
	return base.ResolveReference(&url.URL{Fragment: "/subscribe/" + path + "?tab=mysub"}).String()
}
