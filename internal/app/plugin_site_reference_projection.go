package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"

	"light-media/internal/plugins/builtin"
)

type siteReferenceProjection struct {
	byOpaque map[string]nativeSiteIdentity
	byNumber map[int64]nativeSiteIdentity
	custom   []builtin.SiteReference
}

func (a *App) siteReferenceProjectionTx(ctx context.Context, tx *sql.Tx, custom []builtin.SiteControlOption) (siteReferenceProjection, error) {
	p := siteReferenceProjection{byOpaque: map[string]nativeSiteIdentity{}, byNumber: map[int64]nativeSiteIdentity{}}
	for _, option := range custom {
		p.custom = append(p.custom, option.Value)
	}
	rows, err := tx.QueryContext(ctx, `SELECT site_id FROM native_site_identities ORDER BY source_id`)
	if err != nil {
		return p, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return p, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	for _, id := range ids {
		v, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", id)
		if err != nil {
			return p, err
		}
		p.byOpaque[id], p.byNumber[v.SourceID] = v, v
	}
	return p, nil
}

func (p siteReferenceProjection) source(opaque string) (builtin.SiteReference, error) {
	v, ok := p.byOpaque[opaque]
	if !ok {
		return builtin.SiteReference{}, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "原生站点引用缺少可信编号映射")
	}
	return builtin.SiteReference{Native: v.SourceID}, nil
}

func (p siteReferenceProjection) sourceList(ids []string) ([]builtin.SiteReference, error) {
	out := make([]builtin.SiteReference, 0, len(ids))
	for _, id := range ids {
		ref, err := p.source(id)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}

func (p siteReferenceProjection) opaque(refs []builtin.SiteReference) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		v, ok := p.byNumber[ref.Native]
		if ref.Custom != "" || !ok {
			return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "系统站点选择含未映射的编号，配置未应用")
		}
		out = append(out, v.SiteID)
	}
	return out, nil
}

func siteReferenceJSONEqual(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	return json.Compact(&x, a) == nil && json.Compact(&y, b) == nil && bytes.Equal(x.Bytes(), y.Bytes())
}
