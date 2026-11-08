package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta/legacyanime"
)

const originalChatTransferRedoUsage = "请输入正确的命令格式：/redo [id] 或 /redo [id] [来源前缀:媒体ID]|[类型]，[id] 为整理记录编号"

// The source command replies only for bad arguments or a failed transfer.
// The retry button has its own start and terminal replies.
func (a *App) originalChatTransferRedo(ctx context.Context, reply originalSlashReply, args string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, verified := originalLarkSlashIngress(ctx, reply.info)
	user, err := originalInputID(reply.info.UserID, false)
	if err != nil || !verified || user == nil || *user != reply.user || strings.EqualFold(strings.TrimSpace(*user), "system") || !a.originalChatSubscriptionIngress(ctx) {
		return bad("PLUGIN_CAPABILITY_DENIED", "重新整理需要已验证的 Lark 消息")
	}
	fields := strings.Fields(args)
	if len(fields) == 0 || len(fields) > 2 || !legacyanime.IsDigit(fields[0]) {
		return reply.post(ctx, a, originalChatTransferRedoUsage, false)
	}
	number, err := legacyanime.Integer(fields[0])
	if err != nil {
		return reply.post(ctx, a, originalChatTransferRedoUsage, false)
	}
	var historyID int64
	if number.IsInt64() {
		historyID = number.Int64()
	}
	var recognize func(context.Context) (json.RawMessage, error)
	if len(fields) == 2 {
		parts := strings.SplitN(fields[1], "|", 3)
		if len(parts) < 2 || parts[1] != "电影" && parts[1] != "电视剧" {
			return reply.post(ctx, a, originalChatTransferRedoUsage, false)
		}
		if parts[0] != "" {
			kind, mediaID := parts[1], parts[0]
			recognize = func(ctx context.Context) (json.RawMessage, error) {
				return a.originalChatRedoMedia(ctx, kind, mediaID)
			}
		}
	}
	if recognize == nil {
		err = a.originalChatRetryTransfer(ctx, historyID)
	} else {
		err = a.originalChatRetryTransferWithMedia(ctx, historyID, recognize)
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, err = a.postOriginalPluginMessage(ctx, map[string]any{
		"channel": reply.info.Channel, "source": reply.info.Source, "userid": reply.info.UserID,
		"title": "手动整理失败", "text": a.clean(err.Error()), "link": originalChatMediaLinkTo(a.Settings().AppDomain, "#/history"), "save_history": false,
	})
	return err
}

func (a *App) originalChatRedoMedia(ctx context.Context, mediaType, key string) (json.RawMessage, error) {
	args := map[string]any{"mtype": mediaType}
	prefix, mediaID, prefixed := strings.Cut(key, ":")
	prefix, mediaID = originalCatalogSource(prefix), legacyanime.Strip(mediaID)
	if prefixed && prefix != "" && mediaID != "" {
		args["source"], args["mediaid"] = prefix, mediaID
	} else if legacyanime.IsDigit(key) {
		args["tmdbid"] = key
	} else {
		args["doubanid"] = key
	}
	kwargs, err := originalCatalogRecognitionCallKwargs(nil, args)
	if err != nil {
		return nil, err
	}
	source := sourceText(kwargs["source"])
	kind := "movie"
	if mediaType == "电视剧" {
		kind = "tv"
	}
	nativeID, _ := strconv.ParseInt(sourceStringValue(kwargs["tmdbid"]), 10, 64)
	ctx = context.WithValue(ctx, originalHostMediaNativeSourceKey{}, source)
	model, err := a.originalCatalogRecognizeMethod(ctx, "recognize_media", kwargs, func() (json.RawMessage, error) {
		if source != "themoviedb" {
			return a.originalHostMediaRecognizeSource(ctx, source, kwargs)
		}
		if nativeID <= 0 {
			return nil, nil
		}
		information, err := a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: nativeID, MediaType: kind})
		return information.MediaInfo, err
	})
	if err != nil {
		return nil, err
	}
	if !originalModuleResultTruthy(model) {
		return nil, bad("MEDIA_NOT_FOUND", fmt.Sprintf("未识别到媒体信息，类型：%s，id：%s", mediaType, key))
	}
	view, err := originalCatalogMediaProjection(model)
	if err != nil {
		return nil, err
	}
	if view.MediaType != kind || view.Source != source {
		return nil, bad("IMPORT_IDENTITY_MISMATCH", "识别结果与指定媒体来源或类型不符")
	}
	if source == "themoviedb" {
		if nativeID <= 0 || view.ID != nativeID {
			return nil, bad("IMPORT_IDENTITY_MISMATCH", "识别结果与指定 TMDB ID 不符")
		}
		return model, nil
	}
	requested := kwargs[map[string]string{"douban": "doubanid", "bangumi": "bangumiid", "anilist": "anilistid"}[source]]
	if requested == nil {
		requested = kwargs["mediaid"]
	}
	if view.SourceID == "" || view.SourceID != sourceStringValue(requested) {
		return nil, bad("IMPORT_IDENTITY_MISMATCH", "识别结果与指定来源 ID 不符")
	}
	return model, nil
}
