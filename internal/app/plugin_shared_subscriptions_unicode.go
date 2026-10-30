package app

import "encoding/json"

func sharedSubscriptionRawPatch(raw json.RawMessage) (map[string]json.RawMessage, error) {
	value, err := parseSharedSubscriptionJSON(raw)
	if err != nil {
		return nil, bad("INVALID_SUBSCRIPTION_PATCH", "订阅patch无法解码")
	}
	out := map[string]json.RawMessage{}
	put := func(points []rune, value *sharedSubscriptionJSONValue) error {
		// Known persisted names are ASCII. A source-only transient attribute may
		// contain a surrogate; it is ignored, not globally rejected or normalized.
		nameBytes := make([]byte, 0, len(points))
		for _, r := range points {
			if r > 0x7f {
				return nil
			}
			nameBytes = append(nameBytes, byte(r))
		}
		name := string(nameBytes)
		if name == "id" {
			return bad("SUBSCRIPTION_ID_IMMUTABLE", "宿主订阅身份不能通过普通列patch改变")
		}
		if _, ok := sharedSubscriptionColumnTypes[name]; ok {
			out[name] = json.RawMessage(value.appendJSON(nil))
		}
		return nil
	}
	switch value.kind {
	case '{':
		for _, m := range value.members {
			if err = put(m.key, m.value); err != nil {
				return nil, err
			}
		}
	case '[':
		for _, pair := range value.items {
			if pair.kind != '[' || len(pair.items) != 2 {
				return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch序列须为键值对")
			}
			if pair.items[0].kind != '"' {
				return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅属性名须为字符串")
			}
			if err = put(pair.items[0].points, pair.items[1]); err != nil {
				return nil, err
			}
		}
	case '"':
		if len(value.points) != 0 {
			return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch不是映射")
		}
	default:
		return nil, bad("INVALID_SUBSCRIPTION_PATCH", "原订阅patch不是映射")
	}
	return out, nil
}

func sharedSubscriptionUnicodeRowError(row map[string]json.RawMessage) error {
	for _, key := range []string{"note", "sites", "filter_groups", "episode_priority"} {
		if len(row[key]) == 0 {
			continue
		}
		value, err := parseSharedSubscriptionJSON(row[key])
		if err != nil {
			return err
		}
		if value.hasUnpaired() {
			return bad("SUBSCRIPTION_UNICODE_JSON", "原订阅JSON包含孤立代理码点，已存储事实无法编码为UTF-8响应")
		}
	}
	return nil
}
func sharedSubscriptionHasUnpaired(s Subscription) bool {
	for _, key := range []string{"note", "sites", "filter_groups", "episode_priority"} {
		field, ok := s.SharedSourceFields[key]
		if !ok || !sharedSubscriptionJSONEqual(field.Native, sharedSubscriptionNative(s, key)) {
			continue
		}
		value, err := parseSharedSubscriptionJSON(field.Value)
		if err == nil && value.hasUnpaired() {
			return true
		}
	}
	return false
}
