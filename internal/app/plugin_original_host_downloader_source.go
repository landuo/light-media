package app

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

// Private causal evidence from the trusted owner guard. A vendor response or
// worker-supplied value cannot manufacture it, and http transport errors keep
// its Unwrap chain even if the scope becomes valid again before the final check.
type originalDownloaderGuardError struct{ cause error }

func (e *originalDownloaderGuardError) Error() string { return e.cause.Error() }
func (e *originalDownloaderGuardError) Unwrap() error { return e.cause }

func originalDownloaderNativeError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var denied *originalDownloaderGuardError
	if errors.As(err, &denied) {
		return false
	}
	switch native.MutationFailureClass(err) {
	case "owner_denied", "cancelled":
		return false
	}
	return true
}

func originalDownloaderOutcomeGuard(ctx context.Context, guard func(context.Context) error, err error, source bool) error {
	if current := guard(ctx); current != nil {
		return current
	}
	if source && err != nil && !originalDownloaderNativeError(ctx, err) {
		return err
	}
	return nil
}

func originalHostDownloaderTasksOutcome(out plugins.DownloaderTasksV1, source, nativeError bool) (json.RawMessage, error) {
	if source {
		return originalHostDownloaderMarshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: out, NativeError: nativeError})
	}
	return originalHostDownloaderMarshal(out)
}
func originalHostDownloaderMutationOutcome(ctx context.Context, source bool, nativeErr error, out plugins.DownloaderMutationV1) (json.RawMessage, error) {
	if source {
		return originalHostDownloaderMarshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: out, NativeError: originalDownloaderNativeError(ctx, nativeErr)})
	}
	return originalHostDownloaderMarshal(out)
}

func (a *App) originalHostDownloaderSourceResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case plugins.DownloaderSourceTasksCapability:
		return a.originalDownloaderTasksOutcome(ctx, scope, raw, true)
	case plugins.DownloaderSourceCompletedCapability:
		var r plugins.DownloaderSourceCompletedRequestV1
		if err := originalBrushDownloaderBatchDecode(ctx, scope, raw, &r); err != nil {
			return nil, err
		}
		if r.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "已完成任务筛选无效")
		}
		return a.originalHostDownloaderFilteredOutcome(ctx, scope, r.DownloaderCompletedRequestV1, false, true, r.IncludeQBPackFields)
	case plugins.DownloaderSourceAddCapability:
		if !utf8.Valid(raw) {
			return nil, bad("INVALID_PLUGIN_VALUE", "原始种子添加参数不是有效UTF-8")
		}
		var r plugins.DownloaderSourceAddRequestV1
		if err := originalHostDownloaderAddDecode(raw, &r); err != nil {
			return nil, err
		}
		if r.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "种子添加参数无效")
		}
		return a.originalHostDownloaderAddOutcome(ctx, scope, r.DownloaderAddRequestV1, true, r.SkipCheckingWire, r.QBLabels)
	case plugins.DownloaderSourceControlCapability:
		var r plugins.DownloaderControlRequestV1
		if err := originalBrushDownloaderBatchDecode(ctx, scope, raw, &r); err != nil {
			return nil, err
		}
		if r.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "下载器控制必须指定准确任务与受支持操作")
		}
		return a.originalHostDownloaderControlOutcome(ctx, scope, r, true)
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件下载器资源未获授权")
}
