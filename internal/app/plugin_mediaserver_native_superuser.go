package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Emby/Jellyfin and the already accepted ordinary notice router read the same
// source settings.SUPERUSER. Reuse that precise single-admin host carrier: an
// explicit sealed source preference first, otherwise the stored admin row and
// the existing empty/missing-row default. Never infer from the invoking cookie,
// a worker-supplied username, legacy FNOS config, or a new environment reader.
// The actual factory wrapper checks its Current guard before AND after this
// read; this helper does not manufacture invocation/lifecycle admission.
func originalNativeMediaServerSuperuser(ctx context.Context, a *App) (json.RawMessage, error) {
	if ctx == nil || a == nil || a.Store == nil || a.Store.DB == nil || a.pluginStore == nil {
		return nil, bad("MEDIA_SERVER_SUPERUSER_UNAVAILABLE", "媒体服务器管理员设置未装配")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := a.pluginStore.noticePreference(ctx, a.Store.DB, "superuser", "")
	if current := ctx.Err(); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	if value == nil {
		var username string
		err = a.Store.DB.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&username)
		if current := ctx.Err(); current != nil {
			return nil, current
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if username == "" {
			username = "admin"
		}
		value = username
	}
	username, ok := value.(string)
	if !ok {
		// A malformed local sealed carrier is not an ordinary vendor/network error;
		// fail before forwarding it into source kwargs or returning invented admin.
		return nil, bad("MEDIA_SERVER_SUPERUSER_INVALID", "媒体服务器管理员设置类型无效")
	}
	if current := ctx.Err(); current != nil {
		return nil, current
	}
	return json.Marshal(username)
}
