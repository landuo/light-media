package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const DownloaderTrackersCapability = "downloader.trackers.read"

type DownloaderTrackersRequestV1 struct {
	DownloaderID  string   `json:"downloader_id"`
	Hash          string   `json:"hash"`
	ErrorMessages []string `json:"error_messages"`
}

// ComparisonKey retains the source's equality grouping, including its HTTP
// netloc/non-HTTP distinction. Display is safe for pages/logs; the private URI
// is never returned. Invalid source URL primitives are reported per endpoint.
type DownloaderTrackerEndpointV1 struct {
	Display       string `json:"display"`
	ComparisonKey string `json:"comparison_key"`
	SourceValid   bool   `json:"source_valid"`
}
type DownloaderTrackerTextV1 struct {
	Display         string `json:"display"`
	NonEmpty        bool   `json:"nonempty"`
	NonBlank        bool   `json:"nonblank"`
	Matches         []bool `json:"matches"`
	StrippedMatches []bool `json:"stripped_matches"`
}
type DownloaderTrackerV1 struct {
	Endpoint              DownloaderTrackerEndpointV1 `json:"endpoint"`
	Status                *json.Number                `json:"status,omitempty"`
	Tier                  *json.Number                `json:"tier,omitempty"`
	Message               *DownloaderTrackerTextV1    `json:"message,omitempty"`
	LastAnnounceSucceeded *bool                       `json:"last_announce_succeeded,omitempty"`
	HasAnnounced          *bool                       `json:"has_announced,omitempty"`
}
type DownloaderTrackersV1 struct {
	SchemaVersion        int                           `json:"schema_version"`
	DownloaderID         string                        `json:"downloader_id"`
	Hash                 string                        `json:"hash"`
	Kind                 string                        `json:"kind"`
	TaskFound            bool                          `json:"task_found"`
	TaskErrorCode        *json.Number                  `json:"task_error_code,omitempty"`
	TaskErrorText        *DownloaderTrackerTextV1      `json:"task_error_text,omitempty"`
	TrackerStatsPresent  bool                          `json:"tracker_stats_present"`
	TrackerStats         []DownloaderTrackerV1         `json:"tracker_stats"`
	TrackersPresent      bool                          `json:"trackers_present"`
	Trackers             []DownloaderTrackerV1         `json:"trackers"`
	TrackerListPresent   bool                          `json:"tracker_list_present"`
	TrackerListWasString bool                          `json:"tracker_list_was_string"`
	TrackerList          []DownloaderTrackerEndpointV1 `json:"tracker_list"`
}

func (d Downloaders) Trackers(ctx context.Context, request DownloaderTrackersRequestV1) (DownloaderTrackersV1, error) {
	var out DownloaderTrackersV1
	if !downloaderHostIDValid(request.DownloaderID) || !DownloaderTaskHashValid(request.Hash) || request.ErrorMessages == nil {
		return out, errors.New("exact task and explicit error-message array required")
	}
	request.Hash = strings.ToLower(request.Hash)
	if err := d.hostRequest(ctx, DownloaderTrackersCapability, request, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Hash != request.Hash || out.Kind != "qbittorrent" && out.Kind != "transmission" {
		return DownloaderTrackersV1{}, errors.New("invalid tracker resource identity")
	}
	texts := []*DownloaderTrackerTextV1{out.TaskErrorText}
	for _, group := range [][]DownloaderTrackerV1{out.TrackerStats, out.Trackers} {
		for _, row := range group {
			if row.Endpoint.SourceValid && len(row.Endpoint.ComparisonKey) != 64 {
				return DownloaderTrackersV1{}, errors.New("invalid tracker comparison key")
			}
			texts = append(texts, row.Message)
		}
	}
	for _, row := range out.TrackerList {
		if row.SourceValid && len(row.ComparisonKey) != 64 {
			return DownloaderTrackersV1{}, errors.New("invalid tracker-list comparison key")
		}
	}
	for _, text := range texts {
		if text != nil && (len(text.Matches) != len(request.ErrorMessages) || len(text.StrippedMatches) != len(request.ErrorMessages)) {
			return DownloaderTrackersV1{}, errors.New("invalid tracker message comparison")
		}
	}
	return out, nil
}
