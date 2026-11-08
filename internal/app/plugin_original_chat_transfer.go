package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const originalChatTransferRetryPrefix = "transfer_retry_"

func originalChatTransferRetryNumber(data string) (int64, bool) {
	value, matched := strings.CutPrefix(data, originalChatTransferRetryPrefix)
	if !matched || value == "" || originalSiteDigits.FindString(value) != value {
		return 0, false
	}
	number, err := strconv.ParseInt(originalSiteDecimal(value), 10, 64)
	if err != nil {
		// The source accepts an arbitrary decimal integer. Out-of-range values
		// can only take the missing-record path in the local ID sequence.
		return 0, true
	}
	return number, true
}

func (a *App) originalChatTransferRetryCallback(ctx context.Context, reply originalSlashReply, number int64) error {
	if err := reply.post(ctx, a, fmt.Sprintf("开始重新整理记录 #%d ...", number), false); err != nil {
		return err
	}
	err := a.originalChatRetryTransfer(ctx, number)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	title, body := fmt.Sprintf("整理记录 #%d 已重新整理", number), ""
	if err != nil {
		title, body = "重新整理失败", a.clean(err.Error())
	}
	_, err = a.postOriginalPluginMessage(ctx, map[string]any{
		"channel": reply.info.Channel, "source": reply.info.Source, "userid": reply.info.UserID, "username": reply.username,
		"title": title, "text": body, "link": originalChatMediaLinkTo(a.Settings().AppDomain, "#/history"), "save_history": false,
	})
	return err
}

// Use the host's ordinary notification route after the corresponding failure
// is visible in history. An inability to send a notice never replays a transfer.
func (a *App) originalTransferFailureNotice(ctx context.Context, history transferHistoryItem, source *originalTransferFileContext) {
	if history.Status != "failed" {
		return
	}
	number, err := a.originalChatTransferAlias(ctx, history)
	if err != nil {
		a.originalTransferEventError(ctx, err)
	}
	var buttons [][]originalMessageButton
	if number > 0 {
		buttons = [][]originalMessageButton{{{Text: "重试", CallbackData: originalChatTransferRetryPrefix + strconv.FormatInt(number, 10)}}}
	}
	var title, text, image string
	if source == nil {
		title = filepath.Base(history.Source) + " 未识别到媒体信息，无法入库！"
		text = "原因：未识别到媒体信息"
		if number > 0 {
			text += fmt.Sprintf("\n如果按钮不可用，可回复：\n```\n/redo %d\n/redo %d [tmdbid]|[类型]\n```\n自动重试或手动识别整理。", number, number)
		}
	} else {
		meta, _ := decodeSourceJSON(source.Meta)
		media, _ := decodeSourceJSON(source.Media)
		fields := sourceMap(media)
		name, year := sourceText(fields["title"]), sourceStringValue(fields["year"])
		if name != "" && year != "" && year != "0" {
			name += " (" + year + ")"
		}
		title = name + " " + sourceText(sourceMap(meta)["season_episode"]) + " 入库失败！"
		reason := a.clean(history.Error)
		if reason == "" || reason == "未知错误" {
			reason = "未知"
		}
		text = "原因：" + reason
		if number > 0 {
			text += fmt.Sprintf("\n如果按钮不可用，可回复：\n```\n/redo %d\n```", number)
		}
		image = sourceText(fields["backdrop_path"])
		if image == "" {
			image = sourceText(fields["poster_path"])
		}
		image = strings.ReplaceAll(image, "original", "w500")
	}
	if _, err := a.postOriginalPluginMessage(ctx, map[string]any{
		"mtype": "手动处理", "title": title, "text": text, "image": image,
		"link": originalChatMediaLinkTo(a.Settings().AppDomain, "#/history"), "buttons": buttons,
	}); err != nil {
		a.originalTransferEventError(ctx, err)
	}
}
