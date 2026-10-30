package main

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
)

// Only the fixed original producer's empty-URL []string fixture path. This
// exercises real typed Get -> ordinary plugin prefix/marker work -> typed Set;
// it is not the migrated RemoteIdentifiers HTTP fetcher or scheduler.
func remoteEmptyURLService(ctx context.Context, shared plugins.SharedSystemConfig) (json.RawMessage, error) {
	raw, err := shared.Get(ctx, plugins.SystemConfigCustomIdentifiers)
	if err != nil {
		return nil, err
	}
	var rules []json.RawMessage
	if err = json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	end := len(rules)
	for i, rule := range rules {
		points, err := plugins.DecodeSystemConfigJSONString(rule)
		if err != nil {
			return nil, err
		}
		marker := []rune("RemoteIdentifiers")
		found := false
		for j := 0; j+len(marker) <= len(points); j++ {
			if slices.Equal(points[j:j+len(marker)], marker) {
				found = true
				break
			}
		}
		if found {
			end = i
			break
		}
	}
	marker, err := plugins.EncodeSystemConfigJSONString([]rune("#========以下识别词由 RemoteIdentifiers 插件添加========#"))
	if err != nil {
		return nil, err
	}
	rules = append(rules[:end], marker)
	if _, err = shared.Set(ctx, plugins.SystemConfigCustomIdentifiers, rules); err != nil {
		return nil, err
	}
	return json.RawMessage(`null`), nil
}
