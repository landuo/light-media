package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

type originalTransferWriteContextKey struct{}
type originalTransferWriteContext struct {
	File   PlanFile
	Source originalTransferFileContext
	Media  catalog.Media
}

func (a *App) originalTransferInterceptWrite(ctx context.Context, source, target, method string, over bool) error {
	active, _ := ctx.Value(originalTransferWriteContextKey{}).(*originalTransferWriteContext)
	if active == nil {
		return nil
	}
	file, projection := active.File, active.Source
	file.Source, file.Target, file.Method = source, target, method
	if active.File.Source != source {
		var err error
		projection, err = a.originalTransferContext(ctx, active.Media, file)
		if err != nil {
			return err
		}
	}
	return a.originalTransferIntercept(ctx, file, projection, over)
}

func (a *App) originalTransferIntercept(ctx context.Context, file PlanFile, source originalTransferFileContext, over bool) error {
	item, err := originalTransferSourceItem(source, file)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"fileitem": item, "meta": source.Meta, "mediainfo": source.Media,
		"target_storage": "local", "target_path": file.Target, "transfer_type": originalTransferType(file.Method),
		"options": map[string]any{"over_flag": over}, "cancel": false, "source": "未知拦截源", "reason": ""})
	if file.DirectoryDigest != "" {
		raw, err = json.Marshal(map[string]any{"fileitem": item, "meta": nil, "mediainfo": source.Media, "target_storage": "local", "target_path": file.Target, "transfer_type": originalTransferType(file.Method), "options": map[string]any{}, "cancel": false, "source": "未知拦截源", "reason": ""})
	}
	if err != nil {
		return err
	}
	event := &plugins.MutableEvent{Type: "transfer.intercept", Data: raw}
	a.sendOriginalChainEvent(ctx, event)
	if !originalBaseJSONTruthy(event.Data) {
		return nil
	}
	value, err := decodeSourceJSON(event.Data)
	if err != nil || sourceMap(value) == nil {
		return bad("PLUGIN_EVENT_INVALID", "整理拦截事件返回的字段无效")
	}
	fields := sourceMap(value)
	if sourceSubscriptionTruth(fields["cancel"]) {
		return bad("TRANSFER_CANCELLED", sourceText(fields["reason"]))
	}
	return nil
}

func originalTransferType(method string) string {
	if method == "hardlink" {
		return "link"
	}
	return method
}
