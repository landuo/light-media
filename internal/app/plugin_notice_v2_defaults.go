package app

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
)

// The source 2.1.4 database migration creates these four templates, then 2.1.5
// applies its season_fmt rewrite. Native initialization is deliberately scoped
// to an absent row in a new template domain; it never replaces an explicit
// nil, empty or customized value, nor re-seeds a later migrated absence.
//
//go:embed defaults/notification-templates.json
var notificationTemplateDefaults []byte

func (p *PluginStore) initNotificationTemplatesTx(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS notice_template_initialization(id TEXT PRIMARY KEY,source_sha TEXT NOT NULL)`); e != nil {
		return e
	}
	var exists int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM notice_template_initialization WHERE id='source-default-v1'`).Scan(&exists); e != nil || exists != 0 {
		return e
	}
	token := p.noticeID("preference", "templates", "NotificationTemplates")
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_notice_preferences WHERE kind='templates' AND token=?`, token).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		var defaults map[string]any
		if e := json.Unmarshal(notificationTemplateDefaults, &defaults); e != nil {
			return e
		}
		if e := p.putNoticePreferenceTx(ctx, tx, "templates", "NotificationTemplates", defaults); e != nil {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO notice_template_initialization VALUES('source-default-v1',?)`, tokenHash(string(notificationTemplateDefaults)))
	return e
}
