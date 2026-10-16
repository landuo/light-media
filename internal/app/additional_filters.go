package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"light-media/internal/torrentfilter"
)

type additionalFilter struct{ params *torrentfilter.Parameters }

func snapshotAdditionalFilter(params *torrentfilter.Parameters) additionalFilter {
	if params == nil {
		return additionalFilter{}
	}
	raw, _ := json.Marshal(params)
	var frozen torrentfilter.Parameters
	_ = json.Unmarshal(raw, &frozen)
	return additionalFilter{&frozen}
}
func (f additionalFilter) apply(ctx context.Context, candidates []Candidate) ([]Candidate, error) {
	if f.params == nil {
		return candidates, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		resource := candidate.Resource
		description := resource.SourceSubtitle
		if description == nil {
			description = &resource.Subtitle
		}
		matched, err := torrentfilter.Match(ctx, *f.params, torrentfilter.Torrent{Title: resource.Title, Description: description, Labels: resource.Labels, UploadFactor: &resource.UploadFactor, DownloadFactor: &resource.DownloadFactor, Size: resource.Size})
		if err != nil {
			message := "订阅附加筛选失败"
			var failure *torrentfilter.Failure
			if errors.As(err, &failure) {
				field := map[string]string{"include": "包含", "exclude": "排除", "quality": "质量", "resolution": "分辨率", "effect": "特效", "size": "体积"}[failure.Field]
				if field != "" {
					message += fmt.Sprintf("：%s规则", field)
				}
			}
			return nil, bad("SUBSCRIPTION_FILTER_FAILED", message+"；请检查规则格式或匹配超时")
		}
		if matched {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, nil
}
