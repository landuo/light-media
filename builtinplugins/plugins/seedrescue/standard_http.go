package seedrescue

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

func httpAPIs() []sdk.HTTPAPI {
	query := []sdk.HTTPParameter{}
	for _, name := range []string{"view", "page", "page_size", "state", "promotion", "source_promotion", "source_site_id", "min_seeders", "max_seeders"} {
		query = append(query, sdk.HTTPParameter{Name: name, Kind: "string"})
	}
	return []sdk.HTTPAPI{
		{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear", Query: query},
		{Key: "scan", Path: "/scan", Methods: []string{"POST"}, Auth: "bear"},
		{Key: "job", Path: "/jobs/{target_hash}", Methods: []string{"GET"}, Auth: "bear"},
		{Key: "download", Path: "/jobs/{target_hash}/download", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "retry", Path: "/jobs/{target_hash}/retry", Methods: []string{"POST"}, Auth: "bear"},
		{Key: "ignore", Path: "/jobs/{target_hash}/ignore", Methods: []string{"POST"}, Auth: "bear"},
	}
}

func (p *Plugin) http(ctx context.Context, m *Machine, raw json.RawMessage) (json.RawMessage, error) {
	var request sdk.HTTPRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var result Response
	var err error
	hash := request.Params["target_hash"]
	switch request.Key {
	case "status":
		if len(request.Query["view"]) != 0 && request.LastQuery("view") == "" {
			result = rejected("无效的 RSS 列表类型")
			break
		}
		filter, invalid := statusFilter(request)
		if invalid != "" {
			return validationResponse("query", invalid)
		}
		result, err = m.GetStatus(ctx, filter)
	case "scan":
		id := p.nextScan.Add(1)
		result, err = m.ScanNow(ctx, id, func() error {
			return (sdk.Manager{Host: invocation(ctx).caller}).SubmitPrivateScheduler(ctx, "scan_background", id)
		})
	case "job":
		result, err = m.GetJob(ctx, hash)
	case "download":
		selection, invalid, ok := parseSelection(request.Body)
		if !ok {
			return validationResponse("body", invalid)
		}
		result, err = m.DownloadCandidate(ctx, hash, selection)
	case "retry":
		result, err = m.RetryJob(ctx, hash)
	case "ignore":
		result, err = m.IgnoreJob(ctx, hash)
	default:
		return nil, errors.New("unsupported SeedRescue route")
	}
	if err != nil {
		return nil, err
	}
	data := result.Data
	if data == nil {
		data = Object{}
	}
	return sdk.JSONHTTPResponse(200, Object{"success": result.Success, "message": result.Message,
		"message_i18n": result.Message, "data": data})
}

func statusFilter(request sdk.HTTPRequest) (StatusFilter, string) {
	filter := StatusFilter{View: request.LastQuery("view"), Page: 1, PageSize: 20,
		State: request.LastQuery("state"), Promotion: request.LastQuery("promotion"), SourcePromotion: request.LastQuery("source_promotion")}
	for _, field := range []struct {
		name string
		dest *int
	}{{"page", &filter.Page}, {"page_size", &filter.PageSize}} {
		if len(request.Query[field.name]) == 0 {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(request.LastQuery(field.name)))
		if err != nil {
			return filter, field.name
		}
		*field.dest = value
	}
	for _, field := range []struct {
		name string
		dest **int
	}{{"source_site_id", &filter.SourceSiteID}, {"min_seeders", &filter.MinSeeders}, {"max_seeders", &filter.MaxSeeders}} {
		if len(request.Query[field.name]) == 0 {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(request.LastQuery(field.name)))
		if err != nil {
			return filter, field.name
		}
		*field.dest = &value
	}
	return filter, ""
}

func parseSelection(raw []byte) (Selection, string, bool) {
	var fields map[string]json.RawMessage
	var result Selection
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return result, "", false
	}
	if json.Unmarshal(fields["candidate_id"], &result.CandidateID) != nil || result.CandidateID == "" || utf8.RuneCountInString(result.CandidateID) > 160 {
		return result, "candidate_id", false
	}
	if raw := fields["confirmed_download_factor"]; len(raw) != 0 && string(raw) != "null" {
		var value float64
		if json.Unmarshal(raw, &value) != nil {
			if string(raw) == "true" {
				value = 1
			} else if string(raw) != "false" {
				var numericText string
				if json.Unmarshal(raw, &numericText) != nil {
					return result, "confirmed_download_factor", false
				}
				var err error
				value, err = strconv.ParseFloat(strings.TrimSpace(numericText), 64)
				if err != nil {
					return result, "confirmed_download_factor", false
				}
			}
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return result, "confirmed_download_factor", false
		}
		result.ConfirmedDownloadFactor = &value
	}
	for _, field := range []struct {
		name string
		dest *bool
	}{{"confirm_unknown", &result.ConfirmUnknown}, {"skip_future_risk_confirmation", &result.SkipFutureRiskConfirmation}} {
		if raw := fields[field.name]; len(raw) != 0 {
			var value any
			if json.Unmarshal(raw, &value) != nil {
				return result, field.name, false
			}
			switch value := value.(type) {
			case bool:
				*field.dest = value
			case float64:
				if value != 0 && value != 1 {
					return result, field.name, false
				}
				*field.dest = value == 1
			case string:
				switch strings.ToLower(value) {
				case "1", "on", "t", "true", "y", "yes":
					*field.dest = true
				case "0", "off", "f", "false", "n", "no":
					*field.dest = false
				default:
					return result, field.name, false
				}
			default:
				return result, field.name, false
			}
		}
	}
	return result, "", true
}

func validationResponse(location, field string) (json.RawMessage, error) {
	path := []string{location}
	if field != "" {
		path = append(path, field)
	}
	return sdk.JSONHTTPResponse(422, Object{"detail": []Object{{"type": "value_error", "loc": path, "msg": "Invalid input"}}})
}
