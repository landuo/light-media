package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"light-media/internal/plugins"
)

func originalHostSubscriptionsCapability(method string) bool {
	return method == plugins.SubscriptionsReadCapability || method == plugins.SubscriptionsUpdateCapability
}

func (a *App) originalHostSubscriptionsResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == plugins.SubscriptionsUpdateCapability {
		var in plugins.SubscriptionHostUpdateV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostUpdateSubscription(ctx, scope, in)
	}
	var in plugins.SubscriptionHostRequestV1
	if err := originalResourceDecode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.ID) > 96 || len(in.State) > 128 || strings.ContainsAny(in.State, "\x00\r\n") || in.TMDBID != nil && *in.TMDBID <= 0 || in.Season != nil && *in.Season < 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "订阅查询条件无效")
	}
	states := map[string]bool{}
	if in.State != "" {
		for _, state := range strings.Split(in.State, ",") {
			states[state] = true
		}
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription' ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	items := []Subscription{}
	for rows.Next() {
		var b []byte
		var s Subscription
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal(b, &s); err != nil {
			break
		}
		if s.State == "cancelled" {
			continue
		}
		if in.ID != "" && s.ID != in.ID || in.State != "" && !states[s.SourceState] || in.TMDBID != nil && s.Media.ID != *in.TMDBID || in.Season != nil && s.Season != *in.Season {
			continue
		}
		items = append(items, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := plugins.SubscriptionHostResultV1{SchemaVersion: 1, Items: []json.RawMessage{}}
	for _, s := range items {
		r, e := a.subscriptionSourceProjection(ctx, s)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, r)
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// SubscribeOper.update patches the existing model without recognizing media or
// invoking the UI's full replacement save. Preserve that property here; native
// revisions still invalidate an in-flight host subscription acquisition.
func (a *App) originalHostUpdateSubscription(ctx context.Context, scope originalBaseCallScope, in plugins.SubscriptionHostUpdateV1) (json.RawMessage, error) {
	if in.ID == "" || len(in.ID) > 96 || in.Revision < 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "订阅更新需要准确ID")
	}
	a.subMu.Lock()
	defer a.subMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, in.ID).Scan(&b)
	out := plugins.SubscriptionHostResultV1{SchemaVersion: 1, Items: []json.RawMessage{}}
	if errors.Is(err, sql.ErrNoRows) {
		return json.Marshal(out)
	}
	if err != nil {
		return nil, err
	}
	var s Subscription
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.State == "cancelled" {
		return json.Marshal(out)
	}
	if in.Revision > 0 && in.Revision != s.Revision {
		return nil, bad("SUBSCRIPTION_CONFLICT", "订阅已修改，请重新读取")
	}
	before, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	beforeOperational := originalHostSubscriptionOperational(s)
	beforeProjection, beforeProjectionErr := a.subscriptionSourceProjectionReferences(ctx, tx, s, true)
	p := in.Patch
	patchRaw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var patchFields map[string]json.RawMessage
	if err = json.Unmarshal(patchRaw, &patchFields); err != nil {
		return nil, err
	}
	nulls := map[string]bool{}
	for _, key := range s.SourceNullFields {
		nulls[key] = true
	}
	for key, value := range patchFields {
		if string(value) != "null" {
			delete(nulls, key)
		}
	}
	for _, key := range p.NullFields {
		nulls[key] = true
		switch key {
		case "sites":
			s.SiteIDs = nil
		case "filter_groups":
			s.Policy.FilterGroups = nil
		case "resolution":
			s.Policy.Resolution = ""
		case "quality":
			s.Policy.Source = ""
		case "effect":
			s.Policy.HDR = ""
		case "include":
			s.Policy.Include = ""
		case "exclude":
			s.Policy.Exclude = ""
		case "save_path":
			s.DownloadPath = ""
			s.DirectoryID = ""
			s.RootID = ""
		case "keyword":
			s.Keyword = ""
		default:
			// Additional typed native columns are applied below.
		}
	}
	if p.Sites != nil {
		if *p.Sites == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "订阅站点须为数组")
		}
		known := map[string]bool{}
		for _, site := range a.Settings().Sites {
			known[site.ID] = true
		}
		for _, id := range *p.Sites {
			if !known[id] {
				return nil, bad("INVALID_SITES", "订阅站点不存在")
			}
		}
		s.SiteIDs = append([]string{}, (*p.Sites)...)
	}
	if p.FilterGroups != nil {
		if *p.FilterGroups == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "过滤规则组须为数组")
		}
		s.Policy.FilterGroups = append([]string{}, (*p.FilterGroups)...)
	}
	for _, field := range []struct {
		in  *string
		out *string
	}{{p.Resolution, &s.Policy.Resolution}, {p.Quality, &s.Policy.Source}, {p.Effect, &s.Policy.HDR}, {p.Include, &s.Policy.Include}, {p.Exclude, &s.Policy.Exclude}, {p.Keyword, &s.Keyword}} {
		if field.in != nil {
			*field.out = *field.in
		}
	}
	// FilterParams is the source-shaped filter representation used by the
	// selector. Updating both prevents a stale parser field masking a DB patch.
	if s.Policy.FilterParams != nil {
		f := *s.Policy.FilterParams
		for _, field := range []struct {
			in  *string
			out **string
		}{{p.Resolution, &f.Resolution}, {p.Quality, &f.Quality}, {p.Effect, &f.Effect}, {p.Include, &f.Include}, {p.Exclude, &f.Exclude}} {
			if field.in != nil {
				v := *field.in
				*field.out = &v
			}
		}
		s.Policy.FilterParams = &f
		for _, key := range p.NullFields {
			switch key {
			case "resolution":
				f.Resolution = nil
			case "quality":
				f.Quality = nil
			case "effect":
				f.Effect = nil
			case "include":
				f.Include = nil
			case "exclude":
				f.Exclude = nil
			}
		}
	}
	if p.SavePath != nil {
		if strings.ContainsAny(*p.SavePath, "\x00\r\n") {
			return nil, bad("INVALID_PATH", "下载路径无效")
		}
		s.DownloadPath = *p.SavePath
		s.DirectoryID = ""
		s.RootID = ""
	}
	customWords, wordsChanged, err := a.originalHostExtendedSubscriptionPatch(ctx, tx, &s, p, patchFields)
	if err != nil {
		return nil, err
	}
	if err = validateSubscriptionPolicy(s.Policy); err != nil {
		return nil, err
	}
	s.SourceNullFields = nil
	for key, present := range nulls {
		if present {
			s.SourceNullFields = append(s.SourceNullFields, key)
		}
	}
	slices.Sort(s.SourceNullFields)
	noChange := originalHostSubscriptionJSONEqual(before, mustCleanSeedJSON(s)) && !wordsChanged
	if !noChange && !wordsChanged && beforeProjectionErr == nil && originalHostSubscriptionJSONEqual(mustCleanSeedJSON(beforeOperational), mustCleanSeedJSON(originalHostSubscriptionOperational(s))) {
		afterProjection, e := a.subscriptionSourceProjectionFrom(ctx, tx, s)
		if e != nil {
			return nil, e
		}
		noChange = originalHostSubscriptionJSONEqual(beforeProjection, afterProjection)
	}
	if noChange {
		row, err := a.subscriptionSourceProjectionFrom(ctx, tx, s)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, row)
		return json.Marshal(out)
	}
	s.Revision++
	if err = txPut(ctx, tx, "subscription", s.ID, s); err != nil {
		return nil, err
	}
	if err = a.saveSubscriptionWordsTx(ctx, tx, s.ID, customWords); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), s.ID); err != nil {
		return nil, err
	}
	row, err := a.subscriptionSourceProjectionFrom(ctx, tx, s)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// The session guard reads from the single DB connection. Validate before
	// opening this transaction and rely on its invocation context until commit.
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	out.Items = append(out.Items, row)
	return json.Marshal(out)
}
