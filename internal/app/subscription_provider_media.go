package app

import (
	"encoding/json"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

func providerNativeMedia(m catalog.Media) bool {
	return m.ID == 0 && (m.MediaType == "movie" || m.MediaType == "tv") &&
		(originalChatMediaProvider{Source: m.ProviderSource, ID: m.ProviderID}).sourceValid()
}

func subscriptionProviderIdentity(in SubscriptionRequest) (string, string, error) {
	source, id := in.MediaSource, in.MediaID
	if source == "" && id == "" && in.MediaKey != "" {
		var found bool
		source, id, found = strings.Cut(in.MediaKey, ":")
		if !found {
			return "", "", invalidSubscriptionProvider()
		}
	}
	nestedSource, nestedID := in.Media.ProviderSource, in.Media.ProviderID
	if source == "" && id == "" {
		source, id = nestedSource, nestedID
	}
	if source == "" && id == "" {
		return "", "", nil
	}
	source, id = originalCatalogSource(source), strings.TrimSpace(id)
	if nestedSource != "" || nestedID != "" {
		if source != originalCatalogSource(nestedSource) || id != strings.TrimSpace(nestedID) {
			return "", "", invalidSubscriptionProvider()
		}
	}
	if source == "themoviedb" {
		number, err := strconv.ParseInt(id, 10, 64)
		if err == nil && number > 0 && id == strconv.FormatInt(number, 10) {
			return source, id, nil
		}
	} else if (originalChatMediaProvider{Source: source, ID: id}).sourceValid() {
		return source, id, nil
	}
	return "", "", invalidSubscriptionProvider()
}

func invalidSubscriptionProvider() error {
	return bad("INVALID_MEDIA", "请选择具有有效来源编号的影视")
}

func subscriptionProviderMedia(info sourceMediaInformation, source, id, kind string) (catalog.Media, error) {
	view, err := originalCatalogMediaProjection(info.MediaInfo)
	if err != nil || view.Source != source || view.SourceID != id || view.MediaType != kind || view.Title == "" {
		return catalog.Media{}, invalidSubscriptionProvider()
	}
	value, err := decodeSourceJSON(info.MediaInfo)
	if err != nil {
		return catalog.Media{}, invalidSubscriptionProvider()
	}
	field := (originalChatMediaProvider{Source: source, ID: id}).field()
	if alias := sourceMap(value)[field]; sourceSubscriptionTruth(alias) && sourceStringValue(alias) != id {
		return catalog.Media{}, invalidSubscriptionProvider()
	}
	m := view.Media
	m.ProviderSource, m.ProviderID = source, id
	if !providerNativeMedia(m) {
		return catalog.Media{}, invalidSubscriptionProvider()
	}
	return m, nil
}

func subscriptionProviderEpisodes(info json.RawMessage, season int) []int {
	value, err := decodeSourceJSON(info)
	if err != nil {
		return nil
	}
	return originalChatProviderEpisodes(sourceMap(value), season)
}
