package app

import (
	"context"
	"database/sql"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/notice"
)

// The shared source MessageTemplateHelper/Notice router consumes this private,
// source-bound payload. Keep ctype and original None fields until rendering;
// Lark's final Notification projection deliberately does not carry ctype.
type sourceBusinessNotice struct {
	AbsentContext           bool            `json:",omitempty"`
	Registration            json.RawMessage `json:",omitempty"`
	Envelope                notice.Object
	Meta                    json.RawMessage
	Media                   catalog.Media
	MediaInfo, ProviderInfo json.RawMessage
	Kwargs                  notice.Object
}

// Keep the source Lark caller wire identity while sharing the actual renderer
// and sealed business payload with native subscription completion.
type pluginLarkBusinessNotice = sourceBusinessNotice

func (a *App) larkPostBusinessNotice(ctx context.Context, core *pluginLarkCoreExecution, payload pluginLarkBusinessNotice) error {
	_, e := larkCoreWrite(ctx, a, core, "notice.post", payload, func(ctx context.Context) (pluginNoticeV2Result, error) {
		id, e := a.initLarkBusinessNotice(ctx, core, payload)
		if e != nil {
			return pluginNoticeV2Result{}, e
		}
		if e = a.runNoticeRoute(ctx, id); e != nil {
			return pluginNoticeV2Result{}, e
		}
		return a.larkCommitBusinessNotice(ctx, core, payload, id)
	})
	return e
}

// The final source event transaction must verify notice.post's original normal
// return and authenticated complete v2 route. A nil function result alone is not
// a persisted notification fact, and SourceReturned is not a delivery receipt.
func (a *App) larkRequireBusinessNoticeTx(ctx context.Context, tx *sql.Tx, core *pluginLarkCoreExecution, payload pluginLarkBusinessNotice) error {
	return a.requireLarkBusinessNoticeResultTx(ctx, tx, core, payload)
}

func sourceBusinessProjection(payload sourceBusinessNotice) string {
	if payload.AbsentContext {
		return "absent-source-context/v1"
	}
	return "tmdb-source-attributes/v1"
}
