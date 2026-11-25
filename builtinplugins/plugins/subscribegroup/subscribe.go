package subscribegroup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func subscribeAdded(ctx context.Context, caller sdk.HostCaller, raw json.RawMessage, categoryEnabled bool, rules map[string]Rule, now time.Time) error {
	logger := sdk.Logger{Host: caller}
	var event *sdk.MutableEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event == nil {
		_ = logger.Log(ctx, sdk.LogError, "订阅事件数据为空")
		return nil
	}
	if !categoryEnabled {
		_ = logger.Log(ctx, sdk.LogError, "二级分类自定义填充未开启")
		return nil
	}
	if len(rules) == 0 {
		_ = logger.Log(ctx, sdk.LogError, "插件未开启二级分类自定义填充")
		return nil
	}
	var data struct {
		ID        string                     `json:"subscribe_id"`
		MediaInfo map[string]json.RawMessage `json:"mediainfo"`
	}
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
	}
	if data.ID == "" || len(data.MediaInfo) == 0 {
		_ = logger.Log(ctx, sdk.LogError, "订阅事件数据不完整")
		return nil
	}
	var category string
	if rawCategory := data.MediaInfo["category"]; len(rawCategory) > 0 {
		if err := json.Unmarshal(rawCategory, &category); err != nil {
			return err
		}
	}
	if category == "" {
		var err error
		category, err = recognizeSubscriptionCategory(ctx, caller, data.ID, data.MediaInfo)
		if err != nil || category == "" {
			return err
		}
	}
	rule, found := rules[category]
	if !found {
		_ = logger.Log(ctx, sdk.LogError, fmt.Sprintf("订阅ID:%s 二级分类:%s 未配置自定义规则", data.ID, category))
		return nil
	}
	subscriptions := sdk.Subscriptions{Host: caller}
	result, err := subscriptions.Read(ctx, sdk.SubscriptionHostRequestV1{ID: data.ID, WithRevision: true})
	if err != nil {
		return err
	}
	revision, known := result.Revisions[data.ID]
	if len(result.Items) != 1 || !known {
		return errors.New("订阅记录不存在或缺少当前修订")
	}
	var subscription struct {
		Name string  `json:"name"`
		Year *string `json:"year"`
	}
	if err = json.Unmarshal(result.Items[0], &subscription); err != nil {
		return err
	}
	year := "None"
	if subscription.Year != nil {
		year = *subscription.Year
	}
	patch, fields := subscriptionCategoryPatch(rule, subscription.Name+" ("+year+")")
	_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅记录:%s 二级分类:%s 自定义配置", subscription.Name, category))
	result, err = subscriptions.Update(ctx, sdk.SubscriptionHostUpdateV1{
		ID: data.ID, Revision: revision, Patch: patch,
		SubscribeGroupHistory: groupHistory(subscription.Name, "二级分类自定义配置 "+category, fields, now, ""),
	})
	if err != nil {
		return err
	}
	if len(result.Items) != 1 {
		return errors.New("填充时订阅记录已不存在")
	}
	_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅记录:%s 填充成功", subscription.Name))
	return nil
}

func recognizeSubscriptionCategory(ctx context.Context, caller sdk.HostCaller, id string, info map[string]json.RawMessage) (string, error) {
	var request sdk.MediaRecognizeRequest
	if err := json.Unmarshal(info["type"], &request.Type); err != nil {
		return "", err
	}
	switch request.Type {
	case "电影", "电视剧", "系列", "未知":
	default:
		return "", errors.New("订阅事件媒体类型无效")
	}
	if tmdbID := info["tmdb_id"]; len(tmdbID) > 0 {
		if err := json.Unmarshal(tmdbID, &request.TMDBID); err != nil {
			return "", err
		}
	}
	result, err := (sdk.MediaHost{Host: caller}).Recognize(ctx, request)
	if err != nil {
		return "", err
	}
	logger := sdk.Logger{Host: caller}
	_ = logger.Log(ctx, sdk.LogError, fmt.Sprintf("订阅ID:%s 未获取到二级分类，尝试通过媒体信息识别", id))
	var infoResult struct {
		Category string `json:"category"`
	}
	if err = json.Unmarshal(result, &infoResult); err != nil {
		return "", err
	}
	if infoResult.Category == "" {
		_ = logger.Log(ctx, sdk.LogError, fmt.Sprintf("订阅ID:%s 未获取到二级分类", id))
		return "", nil
	}
	_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅ID:%s 二级分类:%s 已通过媒体信息识别", id, infoResult.Category))
	return infoResult.Category, nil
}

func subscriptionCategoryPatch(rule Rule, titleYear string) (sdk.SubscriptionHostPatchV1, []field) {
	var patch sdk.SubscriptionHostPatchV1
	fields := make([]field, 0, 8)
	add := func(key string, input *string, output **string, normalize func(string) string) {
		if input == nil || *input == "" {
			return
		}
		value := *input
		if normalize != nil {
			value = normalize(value)
		}
		*output = &value
		fields = append(fields, field{key, value})
	}
	add("include", rule.Include, &patch.Include, nil)
	add("exclude", rule.Exclude, &patch.Exclude, nil)
	if len(rule.Sites) > 0 {
		patch.Sites = &rule.Sites
		fields = append(fields, field{"sites", rule.SourceSites})
	}
	if len(rule.FilterGroups) > 0 {
		patch.FilterGroups = &rule.FilterGroups
		fields = append(fields, field{"filter_groups", rule.FilterGroups})
	}
	add("resolution", rule.Resolution, &patch.Resolution, ParseResolution)
	add("quality", rule.Quality, &patch.Quality, ParseQuality)
	add("effect", rule.Effect, &patch.Effect, ParseEffect)
	add("save_path", rule.SavePath, &patch.SavePath, func(path string) string {
		return strings.ReplaceAll(path, "{name}", titleYear)
	})
	return patch, fields
}

type field struct {
	key   string
	value any
}

// The contents are visible on the history page. Preserve json.dumps insertion
// order, separators and ASCII escaping, including numeric source site IDs.
func groupHistory(name, kind string, fields []field, now time.Time, handledKey string) *sdk.SubscribeGroupHistoryV1 {
	if now.IsZero() {
		now = time.Now()
	}
	parts := make([]string, len(fields))
	for i, item := range fields {
		parts[i] = asciiString(item.key) + ": " + historyFieldValue(item.value)
	}
	return &sdk.SubscribeGroupHistoryV1{
		Name: name, Type: kind, Content: "{" + strings.Join(parts, ", ") + "}",
		Time: now.Format("2006-01-02 15:04:05"), HandledKey: handledKey,
	}
}

func historyFieldValue(value any) string {
	switch v := value.(type) {
	case string:
		return asciiString(v)
	case []string:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = asciiString(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []int64:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = strconv.FormatInt(item, 10)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = historyFieldValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []json.RawMessage:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = historyFieldValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case json.RawMessage:
		var text string
		if len(v) > 0 && v[0] == '"' && json.Unmarshal(v, &text) == nil {
			return asciiString(text)
		}
		return string(v)
	default:
		raw, _ := json.Marshal(value)
		return string(raw)
	}
}

func asciiString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r > 0xffff:
				pair := r - 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+pair/1024, 0xdc00+pair%1024)
			case r < 32 || r > 126:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
