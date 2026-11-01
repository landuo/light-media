package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

type originalMediaRecognitionFreshKey struct{}
type originalMediaRecognitionGuardKey struct{}

// The original ContextVar is inherited with OR and restored on scope exit.
// An immutable derived context supplies the same lifetime without global state.
// This flag alone is NOT a provider-cache implementation.
func originalMediaRecognitionFresh(ctx context.Context) bool {
	fresh, _ := ctx.Value(originalMediaRecognitionFreshKey{}).(bool)
	return fresh
}

func withOriginalMediaRecognitionFresh(ctx context.Context, force bool) context.Context {
	return context.WithValue(ctx, originalMediaRecognitionFreshKey{}, force || originalMediaRecognitionFresh(ctx))
}

// Native reads must use this before and after every actual provider operation.
// The worker never supplies the callback, endpoint or settings snapshot.
func originalMediaRecognitionGuard(ctx context.Context) error {
	if ctx == nil {
		return bad("PLUGIN_CALL_CLOSED", "识别调用缺少当前范围")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	guard, ok := ctx.Value(originalMediaRecognitionGuardKey{}).(func(context.Context) error)
	if !ok || guard == nil {
		return bad("PLUGIN_CALL_CLOSED", "识别读取未绑定当前宿主范围")
	}
	return guard(ctx)
}

func originalSharedMediaRecognitionCapability(method string) bool {
	return method == plugins.MediaRecognizeCapability
}

// Only this new route: numeric truth never expands a decimal exponent into a
// big.Rat. Integer mantissas are scanned directly; decimal/exponent spellings
// retain the source JSON binary64 truth (including underflow to zero).
func originalMediaRecognitionTruth(value any) bool {
	if number, ok := value.(json.Number); ok {
		text := string(number)
		if strings.ContainsAny(text, ".eE") {
			parsed, _ := strconv.ParseFloat(text, 64) // Already validated JSON.
			return parsed != 0
		}
		for _, digit := range text {
			if digit >= '1' && digit <= '9' {
				return true
			}
		}
		return false
	}
	return sourceSubscriptionTruth(value)
}

// The reused normalizer has rational truth checks. Feed it only short finite
// binary64 spellings for top-level fractional/exponent numbers, not an exponent
// whose requested allocation is unrelated to the bounded input bytes. Preserve
// exact integer lexemes and the caller's separately retained cache/mediaid.
// This does not alter the shared normalizer or any older route.
func originalMediaRecognitionNormalizationInput(args map[string]any) (map[string]any, error) {
	copy := make(map[string]any, len(args))
	for key, value := range args {
		copy[key] = value
		number, numeric := value.(json.Number)
		if !numeric || key == "cache" || !strings.ContainsAny(string(number), ".eE") {
			continue
		}
		parsed, err := strconv.ParseFloat(string(number), 64)
		if err != nil && parsed != 0 || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "识别参数数值超出当前有限读取范围")
		}
		short := strconv.FormatFloat(parsed, 'g', -1, 64)
		if !strings.ContainsAny(short, ".eE") {
			short += ".0"
		}
		copy[key] = json.Number(short)
	}
	return copy, nil
}

// Strict, finite JSON framing for this already-authorized route. Numbers remain
// json.Number, nested duplicate names are rejected, and no global decoder changes.
func originalMediaRecognitionJSON(raw json.RawMessage) (any, error) {
	invalid := func() error { return bad("INVALID_PLUGIN_VALUE", "识别参数必须是无重复字段的有界JSON") }
	if len(raw) == 0 || len(raw) > 128<<10 || plugins.ValidateSystemConfigUTF8JSON(raw) != nil {
		return nil, invalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		token, err := d.Token()
		if err != nil {
			return nil, invalid()
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return token, nil
		}
		switch delim {
		case '{':
			fields := make(map[string]any)
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return nil, invalid()
				}
				if _, exists := fields[name]; exists {
					return nil, invalid()
				}
				value, err := read()
				if err != nil {
					return nil, err
				}
				fields[name] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, invalid()
			}
			return fields, nil
		case '[':
			values := make([]any, 0)
			for d.More() {
				value, err := read()
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, invalid()
			}
			return values, nil
		default:
			return nil, invalid()
		}
	}
	value, err := read()
	if err != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return nil, invalid()
	}
	return value, nil
}

func originalMediaRecognitionArgs(raw json.RawMessage) (map[string]any, error) {
	value, err := originalMediaRecognitionJSON(raw)
	if err != nil {
		return nil, err
	}
	outer, ok := value.(map[string]any)
	if !ok || len(outer) != 1 {
		return nil, bad("INVALID_PLUGIN_VALUE", "识别请求只允许args对象")
	}
	args, ok := outer["args"].(map[string]any)
	if !ok {
		return nil, bad("INVALID_PLUGIN_VALUE", "识别请求缺少args对象")
	}
	for key := range args {
		if !slices.Contains([]string{"meta", "mtype", "source", "mediaid", "tmdbid", "doubanid", "bangumiid", "anilistid", "episode_group", "cache", "share_meta"}, key) {
			return nil, bad("INVALID_PLUGIN_VALUE", "识别请求含未定义参数")
		}
	}
	if args["meta"] != nil || args["share_meta"] != nil {
		return nil, bad("SOURCE_RECOGNITION_SCOPE_UNSUPPORTED", "当前主动识别尚未接入元数据对象往返")
	}
	// This existing source normalization supplies cache=True only when absent;
	// an explicit null/false remains available to plugins and native fresh logic.
	bounded, err := originalMediaRecognitionNormalizationInput(args)
	if err != nil {
		return nil, err
	}
	kwargs, err := originalCatalogRecognitionCallKwargs(nil, bounded)
	if err != nil {
		return nil, err
	}
	// This route has already required source meta=None. Do not pass the old
	// normalizer's typed-nil map interface as a non-nil native metadata object.
	kwargs["meta"] = nil
	if requested, present := args["mediaid"]; present {
		kwargs["mediaid"] = requested
	}
	if group, present := args["episode_group"]; present {
		// With meta=None the source hasattr branch never replaces a falsey
		// explicit group. Preserve this module-visible input, not the safe copy.
		kwargs["episode_group"] = group
	}
	if kwargs["source"] != "themoviedb" {
		return nil, bad("SOURCE_RECOGNITION_SCOPE_UNSUPPORTED", "当前主动识别仅接入显式TMDB身份")
	}
	id, ok := kwargs["tmdbid"].(json.Number)
	number, parseErr := strconv.ParseInt(string(id), 10, 64)
	if !ok || parseErr != nil || number <= 0 || number > 1<<31-1 {
		return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "TMDB身份超出当前原生读取范围")
	}
	if kind := kwargs["mtype"]; kind != nil {
		text, stringType := kind.(string)
		if !stringType || text != "电影" && text != "电视剧" {
			return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "当前主动识别类型尚未适配")
		}
	}
	return kwargs, nil
}

func (a *App) originalSharedMediaRecognitionResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedMediaRecognitionCapability(method) || ctx == nil || scope.object == nil || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "影视识别缺少实际插件调用范围")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	kwargs, err := originalMediaRecognitionArgs(raw)
	if err != nil {
		return nil, err
	}
	settings := a.Settings()
	parentGuard, _ := ctx.Value(originalMediaRecognitionGuardKey{}).(func(context.Context) error)
	guard := func(current context.Context) error {
		if err := current.Err(); err != nil {
			return err
		}
		if parentGuard != nil {
			if err := parentGuard(current); err != nil {
				return err
			}
		}
		if err := scope.validate(current); err != nil {
			return err
		}
		now := a.Settings()
		if now.TMDB != settings.TMDB || now.ProxyHost != settings.ProxyHost || now.TMDBImageDomain != settings.TMDBImageDomain {
			return bad("PLUGIN_SNAPSHOT_STALE", "识别服务配置已变化")
		}
		return nil
	}
	ctx = context.WithValue(ctx, originalMediaRecognitionGuardKey{}, guard)
	ctx = withOriginalMediaRecognitionFresh(ctx, !originalMediaRecognitionTruth(kwargs["cache"]))
	// Do this before discovery calls get_state/get_module or takes methodMu.
	// A detached caller is not rediscovered; unrelated serial workers are valid.
	if originalObjectStillLoaded(scope.object) && slices.Contains(scope.object.manifest.Methods, "get_module") {
		if err = originalManagerNestedTarget(scope, scope.object); err != nil {
			return nil, err
		}
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(kwargs)
	if err != nil {
		return nil, err
	}
	result, err := a.invokeOriginalPluginModules(ctx, "recognize_media", nil, json.RawMessage(`[]`), encoded)
	if err != nil {
		return nil, err
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	var model json.RawMessage
	var list []json.RawMessage
	callNative := func() (json.RawMessage, error) {
		a.logOriginalPluginContext(ctx, "DEBUG", "module_request", "请求系统模块执行：recognize_media ...")
		value, callErr := a.originalSharedTMDBIDNative(ctx, kwargs)
		if guardErr := guard(ctx); guardErr != nil {
			return nil, guardErr
		}
		// Only a direct native-constructed, definite source algorithm exception
		// enters the original core error handler. No errors.As, code-prefix or
		// blanket catch of platform/authority/pending/provider-unknown failures.
		if sourceErr, direct := callErr.(*originalMediaRecognitionSourceError); direct && sourceErr != nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "媒体识别模块发生了错误", map[string]any{"type": "module", "module_id": originalCatalogSystemModuleID("recognize_media"), "module_name": originalCatalogSystemModuleName("recognize_media"), "module_method": "recognize_media"}, sourceErr)
			return nil, nil
		}
		return value, callErr
	}
	if originalModuleResultEmpty(result) {
		// Fixed production helper, intentionally unresolved until the native
		// owner completes real provider/cache/model handling. No success stub.
		model, err = callNative()
	} else if json.Unmarshal(result, &list) == nil {
		_, err = callNative()
		if err == nil && len(list) != 0 {
			err = originalCatalogMediaInvalid()
		}
	} else if originalModuleResultTruthy(result) {
		model, err = originalLiveMediaImagesJSON(result)
	}
	// Native pending/authority/transport errors must not become a successful
	// None through the older UI wrapper's blanket system-error fallback.
	if err != nil {
		return nil, err
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	if len(model) == 0 {
		model = json.RawMessage(`null`)
	}
	if !json.Valid(model) || !bytes.Equal(bytes.TrimSpace(model), []byte("null")) && (len(bytes.TrimSpace(model)) == 0 || bytes.TrimSpace(model)[0] != '{') {
		return nil, originalCatalogMediaInvalid()
	}
	out, err := json.Marshal(struct {
		Value json.RawMessage `json:"value"`
	}{model})
	if err != nil {
		return nil, err
	}
	if len(out) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "识别结果超过既有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
